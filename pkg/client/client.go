// Package client wraps disgo's REST layer with the pieces this connector
// needs: REST-only construction, context and audit-reason plumbing, base-URL
// override for tests, and paginated iteration over the collections Discord
// exposes as snowflake-cursor lists.
package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// Discord's maximum page sizes for the cursor-paginated collections used here.
const (
	GuildPageSize  = 200
	MemberPageSize = 1000
)

// requestTimeout bounds a single Discord API call.
const requestTimeout = 60 * time.Second

// Client is a REST-only Discord API client.
//
// No gateway connection is opened. Discord's gateway populates its state cache
// asynchronously from READY and GUILD_CREATE events, so a sync that began
// before those arrived would silently observe an empty or partial guild list.
// Every read here goes through the REST API instead, which is synchronous and
// paginated.
type Client struct {
	rest       rest.Rest
	httpClient *http.Client
}

// New builds a Discord client from a bot token. baseURL is only for tests; the
// empty string selects the public API.
func New(ctx context.Context, token string, baseURL string) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("baton-discord: bot token is required")
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	opts := []rest.ClientConfigOpt{rest.WithHTTPClient(httpClient)}

	if baseURL != "" {
		// disgo addresses the API through a single configurable base, so a test
		// server is pointed at per client rather than by mutating package
		// state. The version segment is taken from disgo so this keeps matching
		// whatever version it targets.
		opts = append(opts, rest.WithURL(
			fmt.Sprintf("%s/api/v%d", strings.TrimSuffix(baseURL, "/"), rest.Version)))
	}

	return &Client{
		rest:       rest.New(rest.NewClient(token, opts...)),
		httpClient: httpClient,
	}, nil
}

// Close releases the client's resources.
func (c *Client) Close() error {
	if c == nil || c.httpClient == nil {
		return nil
	}
	c.httpClient.CloseIdleConnections()
	return nil
}

// parseID converts a Baton resource ID to a Discord snowflake.
//
// Baton resource IDs are strings, and Discord snowflakes are 64-bit. Parsing at
// this boundary turns a malformed ID into a clear error instead of an opaque
// 404 from Discord.
func parseID(kind string, id string) (snowflake.ID, error) {
	parsed, err := snowflake.Parse(id)
	if err != nil {
		return 0, fmt.Errorf("baton-discord: %s ID %q is not a valid Discord snowflake: %w", kind, id, err)
	}
	return parsed, nil
}

// CurrentUser returns the bot's own user, the cheapest proof that a token is
// valid. Passing an empty bearer token leaves the client's bot authorization in
// place.
func (c *Client) CurrentUser(ctx context.Context) (*discord.OAuth2User, error) {
	user, err := c.rest.GetCurrentUser("", rest.WithCtx(ctx))
	if err != nil {
		return nil, fmt.Errorf("baton-discord: failed to identify the bot: %w", err)
	}
	return user, nil
}

// Guild returns a single guild.
func (c *Client) Guild(ctx context.Context, guildID string) (*discord.RestGuild, error) {
	id, err := parseID("guild", guildID)
	if err != nil {
		return nil, err
	}
	guild, err := c.rest.GetGuild(id, false, rest.WithCtx(ctx))
	if err != nil {
		return nil, fmt.Errorf("baton-discord: failed to get guild %s: %w", guildID, err)
	}
	return guild, nil
}

// GuildsPage returns one page of the guilds the bot belongs to, plus the cursor
// for the next page. An empty next cursor means the collection is exhausted.
//
// Discord pages this collection by `after=<snowflake of the last guild
// returned>`, and signals "more to come" only by filling the page.
func (c *Client) GuildsPage(ctx context.Context, after string) ([]discord.OAuth2Guild, string, error) {
	var afterID snowflake.ID
	if after != "" {
		parsed, err := parseID("guild", after)
		if err != nil {
			return nil, "", err
		}
		afterID = parsed
	}

	guilds, err := c.rest.GetCurrentUserGuilds("", 0, afterID, GuildPageSize, false, rest.WithCtx(ctx))
	if err != nil {
		return nil, "", fmt.Errorf("baton-discord: failed to list guilds: %w", err)
	}

	next := ""
	if len(guilds) == GuildPageSize {
		next = guilds[len(guilds)-1].ID.String()
	}
	return guilds, next, nil
}

// MembersPage returns one page of a guild's members, plus the cursor for the
// next page. An empty next cursor means the collection is exhausted.
//
// This endpoint requires the privileged Server Members Intent to be enabled on
// the bot's application, and answers 403 without it.
func (c *Client) MembersPage(ctx context.Context, guildID string, after string) ([]discord.Member, string, error) {
	id, err := parseID("guild", guildID)
	if err != nil {
		return nil, "", err
	}

	var afterID snowflake.ID
	if after != "" {
		parsed, err := parseID("user", after)
		if err != nil {
			return nil, "", err
		}
		afterID = parsed
	}

	members, err := c.rest.GetMembers(id, MemberPageSize, afterID, rest.WithCtx(ctx))
	if err != nil {
		if IsForbidden(err) {
			return nil, "", fmt.Errorf(
				"baton-discord: not allowed to list members of guild %s; enable the "+
					"Server Members Intent on the bot application: %w", guildID, err)
		}
		return nil, "", fmt.Errorf("baton-discord: failed to list members of guild %s: %w", guildID, err)
	}

	// A member with no user is a misconfiguration, not a member to skip.
	// Rejecting it here rather than in the callers keeps the failure
	// attributable for all of them: roleBuilder.Grants filters by role before
	// building a resource, so a caller-side guard would let a page ending in
	// such a member end pagination silently and drop every remaining member of
	// the server from that role's grants.
	for _, member := range members {
		if member.User.ID == 0 {
			return nil, "", fmt.Errorf(
				"baton-discord: guild %s returned a member with no user object; "+
					"enable the Server Members Intent on the bot application", guildID)
		}
	}

	next := ""
	if len(members) == MemberPageSize {
		next = members[len(members)-1].User.ID.String()
	}
	return members, next, nil
}

// Roles returns every role in a guild. Discord returns this collection whole.
func (c *Client) Roles(ctx context.Context, guildID string) ([]discord.Role, error) {
	id, err := parseID("guild", guildID)
	if err != nil {
		return nil, err
	}
	roles, err := c.rest.GetRoles(id, rest.WithCtx(ctx))
	if err != nil {
		return nil, fmt.Errorf("baton-discord: failed to list roles of guild %s: %w", guildID, err)
	}
	return roles, nil
}

// Channels returns every channel in a guild. Discord returns this collection whole.
func (c *Client) Channels(ctx context.Context, guildID string) ([]discord.GuildChannel, error) {
	id, err := parseID("guild", guildID)
	if err != nil {
		return nil, err
	}
	channels, err := c.rest.GetGuildChannels(id, rest.WithCtx(ctx))
	if err != nil {
		return nil, fmt.Errorf("baton-discord: failed to list channels of guild %s: %w", guildID, err)
	}
	return channels, nil
}

// Channel returns a single channel, including its permission overwrites.
func (c *Client) Channel(ctx context.Context, channelID string) (discord.GuildChannel, error) {
	id, err := parseID("channel", channelID)
	if err != nil {
		return nil, err
	}
	channel, err := c.rest.GetChannel(id, rest.WithCtx(ctx))
	if err != nil {
		return nil, fmt.Errorf("baton-discord: failed to get channel %s: %w", channelID, err)
	}

	guildChannel, ok := channel.(discord.GuildChannel)
	if !ok {
		return nil, fmt.Errorf("baton-discord: channel %s is not a guild channel", channelID)
	}
	return guildChannel, nil
}

// AddMemberRole assigns a role to a guild member. Discord treats a repeated
// assignment as a success, so this is idempotent.
func (c *Client) AddMemberRole(ctx context.Context, guildID, userID, roleID, reason string) error {
	guild, user, role, err := parseMemberRoleIDs(guildID, userID, roleID)
	if err != nil {
		return err
	}
	if err := c.rest.AddMemberRole(guild, user, role,
		rest.WithCtx(ctx), rest.WithReason(reason)); err != nil {
		return fmt.Errorf("baton-discord: failed to add role %s to user %s in guild %s: %w",
			roleID, userID, guildID, err)
	}
	return nil
}

// RemoveMemberRole removes a role from a guild member.
func (c *Client) RemoveMemberRole(ctx context.Context, guildID, userID, roleID, reason string) error {
	guild, user, role, err := parseMemberRoleIDs(guildID, userID, roleID)
	if err != nil {
		return err
	}
	if err := c.rest.RemoveMemberRole(guild, user, role,
		rest.WithCtx(ctx), rest.WithReason(reason)); err != nil {
		return fmt.Errorf("baton-discord: failed to remove role %s from user %s in guild %s: %w",
			roleID, userID, guildID, err)
	}
	return nil
}

func parseMemberRoleIDs(guildID, userID, roleID string) (snowflake.ID, snowflake.ID, snowflake.ID, error) {
	guild, err := parseID("guild", guildID)
	if err != nil {
		return 0, 0, 0, err
	}
	user, err := parseID("user", userID)
	if err != nil {
		return 0, 0, 0, err
	}
	role, err := parseID("role", roleID)
	if err != nil {
		return 0, 0, 0, err
	}
	return guild, user, role, nil
}

// RemoveGuildMember kicks a member out of a guild.
func (c *Client) RemoveGuildMember(ctx context.Context, guildID, userID, reason string) error {
	guild, err := parseID("guild", guildID)
	if err != nil {
		return err
	}
	user, err := parseID("user", userID)
	if err != nil {
		return err
	}
	if err := c.rest.RemoveMember(guild, user, rest.WithCtx(ctx), rest.WithReason(reason)); err != nil {
		return fmt.Errorf("baton-discord: failed to remove user %s from guild %s: %w", userID, guildID, err)
	}
	return nil
}

// SetChannelOverwrite replaces a channel permission overwrite. Discord has no
// partial update for overwrites, so callers must supply the complete allow and
// deny bitmasks.
//
// The role and member variants are distinct types in the Discord model, so the
// caller states which it means rather than passing a discriminator.
func (c *Client) SetChannelOverwrite(
	ctx context.Context,
	channelID, targetID string,
	forRole bool,
	allow, deny discord.Permissions,
	reason string,
) error {
	channel, err := parseID("channel", channelID)
	if err != nil {
		return err
	}
	target, err := parseID("overwrite target", targetID)
	if err != nil {
		return err
	}

	var update discord.PermissionOverwriteUpdate = discord.MemberPermissionOverwriteUpdate{
		Allow: &allow,
		Deny:  &deny,
	}
	if forRole {
		update = discord.RolePermissionOverwriteUpdate{Allow: &allow, Deny: &deny}
	}

	if err := c.rest.UpdatePermissionOverwrite(channel, target, update,
		rest.WithCtx(ctx), rest.WithReason(reason)); err != nil {
		return fmt.Errorf("baton-discord: failed to set permission overwrite for %s on channel %s: %w",
			targetID, channelID, err)
	}
	return nil
}

// DeleteChannelOverwrite removes a channel permission overwrite entirely.
func (c *Client) DeleteChannelOverwrite(ctx context.Context, channelID, targetID, reason string) error {
	channel, err := parseID("channel", channelID)
	if err != nil {
		return err
	}
	target, err := parseID("overwrite target", targetID)
	if err != nil {
		return err
	}
	if err := c.rest.DeletePermissionOverwrite(channel, target,
		rest.WithCtx(ctx), rest.WithReason(reason)); err != nil {
		return fmt.Errorf("baton-discord: failed to delete permission overwrite for %s on channel %s: %w",
			targetID, channelID, err)
	}
	return nil
}

// CreateInvite mints a single-use, time-limited invite to a channel.
func (c *Client) CreateInvite(ctx context.Context, channelID string, maxAgeSeconds int) (*discord.Invite, error) {
	channel, err := parseID("channel", channelID)
	if err != nil {
		return nil, err
	}

	maxUses := 1
	invite, err := c.rest.CreateInvite(channel, discord.InviteCreate{
		MaxAge:  &maxAgeSeconds,
		MaxUses: &maxUses,
		// Never reuse an existing invite: sharing one across grants would let a
		// single redemption consume another user's invitation.
		Unique: true,
	}, rest.WithCtx(ctx))
	if err != nil {
		return nil, fmt.Errorf("baton-discord: failed to create an invite for channel %s: %w", channelID, err)
	}
	return invite, nil
}

// DeleteInvite revokes an invite so it can no longer be redeemed.
func (c *Client) DeleteInvite(ctx context.Context, code string) error {
	if _, err := c.rest.DeleteInvite(code, rest.WithCtx(ctx)); err != nil {
		return fmt.Errorf("baton-discord: failed to delete invite: %w", err)
	}
	return nil
}

// SendDirectMessage opens a DM channel with a user and sends them a message.
func (c *Client) SendDirectMessage(ctx context.Context, userID, content string) error {
	user, err := parseID("user", userID)
	if err != nil {
		return err
	}

	channel, err := c.rest.CreateDMChannel(user, rest.WithCtx(ctx))
	if err != nil {
		return fmt.Errorf("baton-discord: failed to open a DM channel with user %s: %w", userID, err)
	}

	if _, err := c.rest.CreateMessage(channel.ID(), discord.MessageCreate{Content: content},
		rest.WithCtx(ctx)); err != nil {
		return fmt.Errorf("baton-discord: failed to send a DM to user %s: %w", userID, err)
	}
	return nil
}

// restError extracts disgo's REST error from an error chain.
func restError(err error) *rest.Error {
	var restErr *rest.Error
	if errors.As(err, &restErr) {
		return restErr
	}
	return nil
}

// httpStatus returns the HTTP status carried by a Discord API error, or 0.
func httpStatus(err error) int {
	restErr := restError(err)
	if restErr == nil || restErr.Response == nil {
		return 0
	}
	return restErr.Response.StatusCode
}

// IsNotFound reports whether err is Discord's "does not exist" answer.
//
// For a revoke this is the desired end state rather than a failure: the member,
// role assignment, or overwrite being absent is exactly what was asked for.
func IsNotFound(err error) bool {
	if httpStatus(err) == http.StatusNotFound {
		return true
	}
	restErr := restError(err)
	if restErr == nil {
		return false
	}
	switch restErr.Code {
	case rest.JSONErrorCodeUnknownGuild,
		rest.JSONErrorCodeUnknownMember,
		rest.JSONErrorCodeUnknownUser,
		rest.JSONErrorCodeUnknownRole,
		rest.JSONErrorCodeUnknownChannel:
		return true
	default:
		// Every other code is a genuine failure. Only the "this object does not
		// exist" family means a revoke already reached its desired end state.
		return false
	}
}

// IsForbidden reports whether Discord refused the call on permission grounds.
func IsForbidden(err error) bool {
	return httpStatus(err) == http.StatusForbidden
}

// CannotMessageUser reports whether Discord refused to deliver a DM because the
// recipient does not accept messages from the bot. That is a recipient-side
// privacy setting rather than a connector fault.
func CannotMessageUser(err error) bool {
	restErr := restError(err)
	if restErr == nil {
		return false
	}
	return restErr.Code == rest.JSONErrorCodeCannotSendMessagesToThisUser
}
