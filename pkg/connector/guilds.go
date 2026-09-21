package connector

import (
	"context"
	"fmt"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	resource_sdk "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"

	"github.com/ConductorOne/baton-discord/pkg/client"
)

const guildResourceTypeID = "guild"

// inviteMaxAge is how long a provisioned invite stays redeemable.
const inviteMaxAge = 3 * 24 * time.Hour

var guildResourceType = &v2.ResourceType{
	Id:          guildResourceTypeID,
	DisplayName: "Server",
	Description: "A Discord server.",
}

type guildBuilder struct {
	client *client.Client
}

func newGuildBuilder(c *client.Client) *guildBuilder {
	return &guildBuilder{client: c}
}

func (o *guildBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return guildResourceType
}

func newGuildResource(guildID, guildName string) (*v2.Resource, error) {
	return resource_sdk.NewResource(
		guildName,
		guildResourceType,
		guildID,
		resource_sdk.WithExternalID(&v2.ExternalId{Id: guildID}),
		// These annotations are what drive the per-server sync of members,
		// roles, and channels: the SDK enqueues a child listing for each of
		// them with this server as the parent.
		resource_sdk.WithAnnotation(
			&v2.ChildResourceType{ResourceTypeId: userResourceTypeID},
			&v2.ChildResourceType{ResourceTypeId: roleResourceTypeID},
			&v2.ChildResourceType{ResourceTypeId: channelResourceTypeID},
		),
	)
}

// List returns the servers the bot belongs to, one page at a time.
func (o *guildBuilder) List(
	ctx context.Context,
	parentResourceID *v2.ResourceId,
	opts resource_sdk.SyncOpAttrs,
) ([]*v2.Resource, *resource_sdk.SyncOpResults, error) {
	bag, err := parsePageToken(opts.PageToken.Token, guildResourceTypeID)
	if err != nil {
		return nil, nil, err
	}

	guilds, nextCursor, err := o.client.GuildsPage(ctx, bag.PageToken())
	if err != nil {
		return nil, nil, err
	}

	resources := make([]*v2.Resource, 0, len(guilds))
	for _, guild := range guilds {
		guildResource, err := newGuildResource(guild.ID.String(), guild.Name)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, guildResource)
	}

	nextPage, err := bag.NextToken(nextCursor)
	if err != nil {
		return nil, nil, err
	}

	return resources, syncResults(nextPage), nil
}

// Entitlements returns the single membership entitlement for a server.
func (o *guildBuilder) Entitlements(
	_ context.Context,
	resource *v2.Resource,
	_ resource_sdk.SyncOpAttrs,
) ([]*v2.Entitlement, *resource_sdk.SyncOpResults, error) {
	return []*v2.Entitlement{
		entitlement.NewAssignmentEntitlement(
			resource,
			guildAccessEntitlement,
			entitlement.WithDisplayName(fmt.Sprintf("%s Server Access", resource.DisplayName)),
			entitlement.WithDescription(fmt.Sprintf("Member of the %s Discord server", resource.DisplayName)),
			entitlement.WithGrantableTo(userResourceType),
		),
	}, nil, nil
}

// Grants returns one membership grant per server member, a page at a time.
func (o *guildBuilder) Grants(
	ctx context.Context,
	resource *v2.Resource,
	opts resource_sdk.SyncOpAttrs,
) ([]*v2.Grant, *resource_sdk.SyncOpResults, error) {
	bag, err := parsePageToken(opts.PageToken.Token, guildResourceTypeID)
	if err != nil {
		return nil, nil, err
	}

	members, nextCursor, err := o.client.MembersPage(ctx, resource.Id.Resource, bag.PageToken())
	if err != nil {
		return nil, nil, err
	}

	grants := make([]*v2.Grant, 0, len(members))
	for _, member := range members {
		principal, err := newMemberResource(member, resource.Id.Resource)
		if err != nil {
			return nil, nil, err
		}
		grants = append(grants, grant.NewGrant(resource, guildAccessEntitlement, principal))
	}

	nextPage, err := bag.NextToken(nextCursor)
	if err != nil {
		return nil, nil, err
	}

	return grants, syncResults(nextPage), nil
}

// Grant invites a user to the server.
//
// Discord has no API that adds a user to a server on the bot's authority.
// `PUT /guilds/{guild}/members/{user}` exists but requires an OAuth2 access
// token that the *user* granted with the guilds.join scope, which a governance
// integration holding only a bot token does not have. So the grant is an
// invitation: mint a single-use invite and DM it to the user.
//
// No grant is returned, because none exists yet — the user still has to accept.
// C1 observes the resulting membership on the next sync.
func (o *guildBuilder) Grant(
	ctx context.Context,
	principal *v2.Resource,
	ent *v2.Entitlement,
) ([]*v2.Grant, annotations.Annotations, error) {
	if err := requireEntitlementSlug(ent, guildAccessEntitlement); err != nil {
		return nil, nil, err
	}
	if err := requireResourceType(ent.Resource, guildResourceTypeID); err != nil {
		return nil, nil, err
	}
	if err := requireResourceType(principal, userResourceTypeID); err != nil {
		return nil, nil, err
	}

	guildID := ent.Resource.Id.Resource

	guild, err := o.client.Guild(ctx, guildID)
	if err != nil {
		return nil, nil, err
	}

	channelID, err := o.inviteChannelID(ctx, guild)
	if err != nil {
		return nil, nil, err
	}

	invite, err := o.client.CreateInvite(ctx, channelID, int(inviteMaxAge.Seconds()))
	if err != nil {
		return nil, nil, err
	}

	message := fmt.Sprintf("You've been invited to %s: https://discord.gg/%s", guild.Name, invite.Code)
	if err := o.client.SendDirectMessage(ctx, principal.Id.Resource, message); err != nil {
		// The invite was minted but could not be delivered, so it is now a
		// redeemable credential that nobody asked for. Revoke it rather than
		// leaving it live until its expiry, and keep the code out of the error:
		// errors reach connector logs and C1 task output, where anyone with
		// read access could redeem it.
		if deleteErr := o.client.DeleteInvite(ctx, invite.Code); deleteErr != nil {
			l := ctxzap.Extract(ctx)
			l.Warn("baton-discord: could not revoke an undeliverable invite",
				zap.String("guild_id", guildID),
				zap.Error(deleteErr))
		}

		// The recipient's privacy settings can block DMs from the bot. Silently
		// reporting success would leave the request looking fulfilled when the
		// user never received anything.
		if client.CannotMessageUser(err) {
			return nil, nil, fmt.Errorf(
				"baton-discord: user %s does not accept direct messages from this bot, so the "+
					"invitation to %s could not be delivered; ask them to allow direct messages "+
					"from server members and retry: %w",
				principal.Id.Resource, guild.Name, err)
		}
		return nil, nil, err
	}

	return nil, nil, nil
}

// optionalIDs returns the non-nil snowflakes among its arguments, as strings.
func optionalIDs(ids ...*snowflake.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != nil {
			out = append(out, id.String())
		}
	}
	return out
}

// inviteChannelID picks the channel a new member should be invited into.
//
// The server's own nomination is preferred — its rules channel, then its system
// channel — and only failing those does it fall back to the topmost text
// channel. The previous implementation chose by Channel.MemberCount, which the
// REST channel object does not populate, so it effectively always picked the
// last text channel it happened to see.
func (o *guildBuilder) inviteChannelID(ctx context.Context, guild *discord.RestGuild) (string, error) {
	channels, err := o.client.Channels(ctx, guild.ID.String())
	if err != nil {
		return "", err
	}

	textChannels := make(map[string]discord.GuildChannel, len(channels))
	var fallback discord.GuildChannel
	for _, channel := range channels {
		if channel.Type() != discord.ChannelTypeGuildText {
			continue
		}
		textChannels[channel.ID().String()] = channel
		if fallback == nil || channel.Position() < fallback.Position() {
			fallback = channel
		}
	}

	for _, preferred := range optionalIDs(guild.RulesChannelID, guild.SystemChannelID) {
		if _, ok := textChannels[preferred]; ok {
			return preferred, nil
		}
	}

	if fallback == nil {
		return "", fmt.Errorf("baton-discord: server %s has no text channel to invite into", guild.ID)
	}
	return fallback.ID().String(), nil
}

// Revoke removes a member from the server.
func (o *guildBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	if err := requireEntitlementSlug(g.Entitlement, guildAccessEntitlement); err != nil {
		return nil, err
	}
	if err := requireResourceType(g.Entitlement.Resource, guildResourceTypeID); err != nil {
		return nil, err
	}
	// The SDK guarantees the entitlement chain but passes Grant.Principal
	// through unvalidated. Without this an absent principal panics, and a
	// role-typed one would hand a role snowflake to RemoveGuildMember whose 404
	// is swallowed below as "already revoked" — reporting success for a revoke
	// that never happened.
	if err := requireResourceType(g.Principal, userResourceTypeID); err != nil {
		return nil, err
	}

	guildID := g.Entitlement.Resource.Id.Resource
	userID := g.Principal.Id.Resource

	err := o.client.RemoveGuildMember(ctx, guildID, userID, "Server access revoked by ConductorOne")
	if err != nil {
		// The member already being gone is the desired end state, not a failure.
		if client.IsNotFound(err) {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		return nil, err
	}

	return nil, nil
}
