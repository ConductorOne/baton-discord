package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	resource_sdk "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/disgoorg/disgo/discord"

	"github.com/ConductorOne/baton-discord/pkg/client"
)

const channelResourceTypeID = "channel"

var channelResourceType = &v2.ResourceType{
	Id:          channelResourceTypeID,
	DisplayName: "Channel",
	Description: "A text, announcement, forum, voice, or stage channel in a Discord server.",
}

type channelBuilder struct {
	client *client.Client
}

func newChannelBuilder(c *client.Client) *channelBuilder {
	return &channelBuilder{client: c}
}

func (c *channelBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return channelResourceType
}

// channelTopic returns a channel's topic, which only message-bearing channel
// types carry.
func channelTopic(channel discord.GuildChannel) string {
	messageChannel, ok := channel.(discord.GuildMessageChannel)
	if !ok {
		return ""
	}
	if topic := messageChannel.Topic(); topic != nil {
		return *topic
	}
	return ""
}

func newChannelResource(channel discord.GuildChannel, guildID string) (*v2.Resource, error) {
	guildResourceID, err := resource_sdk.NewResourceID(guildResourceType, guildID)
	if err != nil {
		return nil, err
	}

	options := []resource_sdk.ResourceOption{
		resource_sdk.WithParentResourceID(guildResourceID),
		resource_sdk.WithExternalID(&v2.ExternalId{Id: channel.ID().String()}),
		resource_sdk.WithResourceProfile(map[string]any{
			"channel_id":      channel.ID().String(),
			profileKeyGuildID: guildID,
			"channel_type":    int(channel.Type()),
			"position":        channel.Position(),
		}),
	}
	if topic := channelTopic(channel); topic != "" {
		options = append(options, resource_sdk.WithDescription(topic))
	}

	return resource_sdk.NewResource(
		channel.Name(),
		channelResourceType,
		channel.ID().String(),
		options...,
	)
}

// List returns the governed channels of one server. Discord returns this
// collection whole, so there is nothing to paginate.
func (c *channelBuilder) List(
	ctx context.Context,
	parentResourceID *v2.ResourceId,
	_ resource_sdk.SyncOpAttrs,
) ([]*v2.Resource, *resource_sdk.SyncOpResults, error) {
	// Channels are only reachable through a server; see userBuilder.List.
	if parentResourceID == nil {
		return nil, nil, nil
	}

	guildID := parentResourceID.Resource
	channels, err := c.client.Channels(ctx, guildID)
	if err != nil {
		return nil, nil, err
	}

	resources := make([]*v2.Resource, 0, len(channels))
	for _, channel := range channels {
		if !isSyncableChannel(channel.Type()) {
			continue
		}
		channelResource, err := newChannelResource(channel, guildID)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, channelResource)
	}

	return resources, nil, nil
}

// Entitlements returns one permission entitlement per governable permission on
// the channel. Which permissions apply depends on the channel's type, so the
// channel has to be read.
func (c *channelBuilder) Entitlements(
	ctx context.Context,
	resource *v2.Resource,
	_ resource_sdk.SyncOpAttrs,
) ([]*v2.Entitlement, *resource_sdk.SyncOpResults, error) {
	channel, err := c.client.Channel(ctx, resource.Id.Resource)
	if err != nil {
		return nil, nil, err
	}

	permissions := permissionsForChannel(channel.Type())
	entitlements := make([]*v2.Entitlement, 0, len(permissions))
	for _, permission := range permissions {
		entitlements = append(entitlements, entitlement.NewPermissionEntitlement(
			resource,
			permission.Slug,
			entitlement.WithDisplayName(fmt.Sprintf("%s: %s", resource.DisplayName, permission.DisplayName)),
			entitlement.WithDescription(fmt.Sprintf("%s #%s", permission.Description, channel.Name())),
			// A channel overwrite can target either a role or an individual
			// member, so both are grantable principals.
			entitlement.WithGrantableTo(userResourceType, roleResourceType),
		))
	}

	return entitlements, nil, nil
}

// overwriteTarget describes the principal a permission overwrite applies to,
// flattened out of Discord's separate role and member overwrite types.
type overwriteTarget struct {
	// ResourceTypeID is the Baton resource type of the principal.
	ResourceTypeID string
	ID             string
	Allow          discord.Permissions
	Deny           discord.Permissions
	// IsRole distinguishes the two overwrite kinds when writing one back.
	IsRole bool
}

// describeOverwrite flattens a permission overwrite. The second result is false
// for an overwrite kind this connector does not model.
func describeOverwrite(overwrite discord.PermissionOverwrite) (overwriteTarget, bool) {
	switch typed := overwrite.(type) {
	case discord.RolePermissionOverwrite:
		return overwriteTarget{
			ResourceTypeID: roleResourceTypeID,
			ID:             typed.RoleID.String(),
			Allow:          typed.Allow,
			Deny:           typed.Deny,
			IsRole:         true,
		}, true
	case discord.MemberPermissionOverwrite:
		return overwriteTarget{
			ResourceTypeID: userResourceTypeID,
			ID:             typed.UserID.String(),
			Allow:          typed.Allow,
			Deny:           typed.Deny,
		}, true
	default:
		return overwriteTarget{}, false
	}
}

// Grants returns the channel's explicit permission grants.
//
// Access to a Discord channel is expressed as permission overwrites: each
// overwrite names a role or a member and carries allow and deny bitmasks. A
// permission that is explicitly allowed for a target is a grant of that
// entitlement to that principal.
//
// Only explicit allows are reported. Effective access in Discord is the result
// of layering @everyone, role, and member overwrites over a member's
// server-wide role permissions; reporting that computed union as a direct grant
// on the channel would misattribute inherited access. The previous
// implementation did exactly that — and worse, tested the *role's server-wide*
// permission bitmask rather than the overwrite's allow mask, so it reported
// channel grants that the channel's own configuration never conferred.
func (c *channelBuilder) Grants(
	ctx context.Context,
	resource *v2.Resource,
	_ resource_sdk.SyncOpAttrs,
) ([]*v2.Grant, *resource_sdk.SyncOpResults, error) {
	channel, err := c.client.Channel(ctx, resource.Id.Resource)
	if err != nil {
		return nil, nil, err
	}

	permissions := permissionsForChannel(channel.Type())

	// Member overwrites outlive the membership they were created for: Discord
	// keeps them when the targeted member leaves the server. Emitting those
	// would produce grants pointing at users that the user listing never
	// returned, which the SDK reports as dangling principals. Role overwrites
	// need no such check, because a deleted role takes its overwrites with it.
	guildID, err := parentGuildID(resource)
	if err != nil {
		return nil, nil, err
	}

	var grants []*v2.Grant
	for _, overwrite := range channel.PermissionOverwrites() {
		target, ok := describeOverwrite(overwrite)
		if !ok {
			continue
		}

		if target.ResourceTypeID == userResourceTypeID {
			_, stillAMember, err := c.client.Member(ctx, guildID, target.ID)
			if err != nil {
				return nil, nil, err
			}
			if !stillAMember {
				continue
			}
		}

		principal, err := resource_sdk.NewResourceID(
			resourceTypeFor(target.ResourceTypeID), target.ID)
		if err != nil {
			return nil, nil, err
		}

		for _, permission := range permissions {
			if !target.Allow.Has(permission.Value) {
				continue
			}
			grants = append(grants, grant.NewGrant(resource, permission.Slug, principal))
		}
	}

	return grants, nil, nil
}

// resourceTypeFor maps a Baton resource type ID to its declaration.
func resourceTypeFor(resourceTypeID string) *v2.ResourceType {
	if resourceTypeID == roleResourceTypeID {
		return roleResourceType
	}
	return userResourceType
}

// --- Provisioning -----------------------------------------------------------
//
// `PUT /channels/{channel}/permissions/{overwrite}` replaces the whole
// overwrite rather than patching it, so both grant and revoke are
// read-modify-write: read the channel's current overwrites, flip one bit, write
// the result back. The read deliberately does not use any cached value — a
// provisioning write must be computed from the channel's current state.

// Grant allows a permission for a principal on a channel.
//
// The allow bit is set and the matching deny bit cleared, because an explicit
// deny outranks an allow in Discord's resolution order and would make the grant
// inert.
//
// The read-modify-write is not atomic, and Discord offers no compare-and-set on
// this endpoint. Two grants of different permissions to the same principal on
// the same channel that interleave between the read and the write will each
// send the full mask computed from its own read, so the later write drops the
// earlier bit. Provisioning tasks for one resource are not run concurrently in
// practice, and the next sync reports the true state, so this is recorded as a
// known limitation rather than papered over with a retry loop that would narrow
// the window without closing it.
func (c *channelBuilder) Grant(
	ctx context.Context,
	principal *v2.Resource,
	ent *v2.Entitlement,
) ([]*v2.Grant, annotations.Annotations, error) {
	if err := requireResourceType(ent.Resource, channelResourceTypeID); err != nil {
		return nil, nil, err
	}

	permission, err := channelPermissionForEntitlement(ent)
	if err != nil {
		return nil, nil, err
	}

	forRole, err := overwriteIsForRole(principal)
	if err != nil {
		return nil, nil, err
	}

	channelID := ent.Resource.Id.Resource
	channel, err := c.client.Channel(ctx, channelID)
	if err != nil {
		return nil, nil, err
	}

	// Entitlements only advertises the permissions that apply to this channel's
	// type, but a stale or hand-built grant task can still name one that does
	// not — a thread permission against a voice channel, say. Writing it would
	// put a meaningless bit into the overwrite, so refuse instead.
	if !permissionAppliesTo(permission, channel.Type()) {
		return nil, nil, fmt.Errorf(
			"baton-discord: permission %q does not apply to channel %s of type %d",
			permission.Slug, channelID, channel.Type())
	}

	allow := permission.Value
	var deny discord.Permissions
	if existing, ok := findOverwrite(channel, principal.Id.Resource); ok {
		allow = existing.Allow.Add(permission.Value)
		deny = existing.Deny.Remove(permission.Value)
		forRole = existing.IsRole
	}

	err = c.client.SetChannelOverwrite(ctx, channelID, principal.Id.Resource, forRole, allow, deny,
		"Channel permission granted by ConductorOne")
	if err != nil {
		return nil, nil, err
	}

	// Returning the grant lets C1 record the new access immediately rather than
	// waiting for the next sync. The ID matches what the sync path emits.
	return []*v2.Grant{
		grant.NewGrant(ent.Resource, permission.Slug, principal),
	}, nil, nil
}

// Revoke removes an explicit allow for a principal on a channel.
//
// The deny mask is deliberately left alone: revoking an explicit allow should
// fall back to whatever the principal inherits, not add an explicit deny. If
// clearing the bit leaves the overwrite allowing and denying nothing, the
// overwrite is deleted rather than left behind as a no-op.
func (c *channelBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	if err := requireResourceType(g.Entitlement.Resource, channelResourceTypeID); err != nil {
		return nil, err
	}
	// A channel overwrite targets a role or a member, so both are valid here,
	// but the principal still has to be one of them and has to exist. See
	// guildBuilder.Revoke.
	if _, err := overwriteIsForRole(g.Principal); err != nil {
		return nil, err
	}

	permission, err := channelPermissionForEntitlement(g.Entitlement)
	if err != nil {
		return nil, err
	}

	channelID := g.Entitlement.Resource.Id.Resource
	targetID := g.Principal.Id.Resource

	channel, err := c.client.Channel(ctx, channelID)
	if err != nil {
		return nil, err
	}

	existing, ok := findOverwrite(channel, targetID)
	if !ok {
		// No overwrite for this principal means the permission was never
		// explicitly allowed here, which is the desired end state.
		return annotations.New(&v2.GrantAlreadyRevoked{}), nil
	}

	allow := existing.Allow.Remove(permission.Value)
	if allow == existing.Allow {
		// The bit was not set, so there is nothing to revoke.
		return annotations.New(&v2.GrantAlreadyRevoked{}), nil
	}

	if allow == 0 && existing.Deny == 0 {
		err = c.client.DeleteChannelOverwrite(ctx, channelID, targetID,
			"Channel permission revoked by ConductorOne")
	} else {
		err = c.client.SetChannelOverwrite(ctx, channelID, targetID, existing.IsRole, allow, existing.Deny,
			"Channel permission revoked by ConductorOne")
	}
	if err != nil {
		if client.IsNotFound(err) {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		return nil, err
	}

	return nil, nil
}

// channelPermissionForEntitlement recovers the Discord permission bit an
// entitlement refers to.
func channelPermissionForEntitlement(ent *v2.Entitlement) (channelPermission, error) {
	slug := entitlementSlug(ent)
	permission, ok := channelPermissionsBySlug[slug]
	if !ok {
		return channelPermission{}, fmt.Errorf("baton-discord: unknown channel permission entitlement %q", slug)
	}
	return permission, nil
}

// overwriteIsForRole maps a Baton principal to the overwrite kind Discord
// expects.
func overwriteIsForRole(principal *v2.Resource) (bool, error) {
	if principal == nil || principal.Id == nil {
		return false, fmt.Errorf("baton-discord: missing principal")
	}
	switch principal.Id.ResourceType {
	case roleResourceTypeID:
		return true, nil
	case userResourceTypeID:
		return false, nil
	default:
		return false, fmt.Errorf(
			"baton-discord: a channel permission cannot be granted to a %q principal",
			principal.Id.ResourceType)
	}
}

func findOverwrite(channel discord.GuildChannel, targetID string) (overwriteTarget, bool) {
	for _, overwrite := range channel.PermissionOverwrites() {
		target, ok := describeOverwrite(overwrite)
		if ok && target.ID == targetID {
			return target, true
		}
	}
	return overwriteTarget{}, false
}
