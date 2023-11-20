package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	resource_sdk "github.com/conductorone/baton-sdk/pkg/types/resource"
)

var channelResourceTypeID = "channel"

var channelResourceType = &v2.ResourceType{
	Id:          channelResourceTypeID,
	DisplayName: "Channel",
}

type channelBuilder struct {
	conn *discordgo.Session

	memberCache  map[string]map[string]*discordgo.Member
	roleCache    map[string]map[string]*discordgo.Role
	channelCache map[string]map[string]*discordgo.Channel
}

func (o *channelBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return channelResourceType
}

func newChannelResource(channel *discordgo.Channel, guild *discordgo.Guild) (*v2.Resource, error) {
	guildResource, err := resource_sdk.NewResourceID(guildResourceType, guild.ID)
	if err != nil {
		return nil, err
	}

	return resource_sdk.NewResource(
		channel.Name,
		channelResourceType,
		channel.ID,
		resource_sdk.WithParentResourceID(guildResource),
		resource_sdk.WithDescription(channel.Topic),
	)
}

// List returns all the guilds from the database as resource objects.
func (o *channelBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	resources := []*v2.Resource{}

	for _, guild := range o.conn.State.Guilds {
		channels, err := o.conn.GuildChannels(guild.ID)
		if err != nil {
			return nil, "", nil, err
		}

		for _, channel := range channels {
			// Skip channels that aren't one of these
			if channel.Type != discordgo.ChannelTypeGuildText && channel.Type != discordgo.ChannelTypeGuildVoice {
				continue
			}

			channel, err := newChannelResource(channel, guild)
			if err != nil {
				return nil, "", nil, err
			}
			resources = append(resources, channel)
		}
	}

	return resources, "", nil, nil
}

func newChannelEntitlement(resource *v2.Resource, permission int64, channel *discordgo.Channel) *v2.Entitlement {
	return entitlement.NewPermissionEntitlement(
		resource,
		fmt.Sprintf("%s for %s", permNameFromVal[permission], channel.Name),
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithGrantableTo(roleResourceType),
	)
}

func (o *channelBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	entitlements := []*v2.Entitlement{}

	channel, err := o.conn.Channel(resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	perms := textChannelPermissions
	if channel.Type == discordgo.ChannelTypeGuildVoice {
		perms = channelPermissions
	}

	for _, permission := range perms {
		entitlements = append(
			entitlements,
			newChannelEntitlement(resource, permission, channel),
		)
	}

	return entitlements, "", nil, nil
}

func newChannelUserPermissionGrant(resource *v2.Resource, guild *discordgo.Guild, user *discordgo.Member, channel *discordgo.Channel, permission int64) (*v2.Grant, error) {
	userPrincipal, err := newMemberResource(user, guild)
	if err != nil {
		return nil, err
	}

	return grant.NewGrant(
		resource,
		newChannelEntitlement(resource, permission, channel).DisplayName,
		userPrincipal,
	), nil
}
func newChannelRolePermissionGrant(resource *v2.Resource, guild *discordgo.Guild, role *discordgo.Role, channel *discordgo.Channel, permission int64) (*v2.Grant, error) {
	rolePrincipal, err := newRoleResource(role, guild)
	if err != nil {
		return nil, err
	}

	return grant.NewGrant(
		resource,
		newChannelEntitlement(resource, permission, channel).DisplayName,
		rolePrincipal,
	), nil
}

func (c *channelBuilder) getChannel(guildID string, channelID string) (*discordgo.Channel, error) {
	channelCache, ok := c.channelCache[guildID]
	if !ok {
		channelCache = make(map[string]*discordgo.Channel)
		c.channelCache[guildID] = channelCache

		guildChannels, err := c.conn.GuildChannels(guildID)
		if err != nil {
			return nil, err
		}

		for _, channel := range guildChannels {
			channelCache[channel.ID] = channel
		}
	}

	channel, ok := channelCache[guildID]
	if !ok {
		return nil, errors.New("channel not found")
	}

	return channel, nil
}

func (c *channelBuilder) getMember(guildID string, memberID string) (*discordgo.Member, error) {
	userCache, ok := c.memberCache[guildID]
	if !ok {
		userCache = make(map[string]*discordgo.Member)
		c.memberCache[guildID] = userCache

		token := ""
		for {
			guildMembers, err := c.conn.GuildMembers(guildID, token, 1000)
			if err != nil {
				return nil, err
			}

			if len(guildMembers) == 0 {
				break
			}

			for _, member := range guildMembers {
				userCache[member.User.ID] = member
			}

			token = guildMembers[len(guildMembers)-1].User.ID
		}
	}

	user, ok := userCache[memberID]
	if !ok {
		return nil, errors.New("member not found")
	}

	return user, nil
}
func (c *channelBuilder) getRole(guildID string, roleID string) (*discordgo.Role, error) {
	roleCache, ok := c.roleCache[guildID]
	if !ok {
		roleCache = make(map[string]*discordgo.Role)
		c.roleCache[guildID] = roleCache

		guildRoles, err := c.conn.GuildRoles(guildID)
		if err != nil {
			return nil, err
		}

		for _, role := range guildRoles {
			roleCache[role.ID] = role
		}
	}

	role, ok := roleCache[roleID]
	if !ok {
		return nil, errors.New("role not found")
	}

	return role, nil
}

func (c *channelBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant

	debugLog(fmt.Sprintf("channelBuilder.Grants: %+v", resource))

	guild, err := c.conn.Guild(resource.ParentResourceId.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	channel, err := c.getChannel(guild.ID, resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}
	for _, permissionOverride := range channel.PermissionOverwrites {
		if permissionOverride.Type != discordgo.PermissionOverwriteTypeMember {
			continue
		}

		switch permissionOverride.Type {
		case discordgo.PermissionOverwriteTypeMember:
			memberGrants, err := c.getChannelGrantForMember(resource, guild, channel, permissionOverride)
			if err != nil {
				return nil, "", nil, err
			}
			grants = append(grants, memberGrants...)
		case discordgo.PermissionOverwriteTypeRole:
			roleGrants, err := c.getChannelGrantForRole(resource, guild, channel, permissionOverride)
			if err != nil {
				return nil, "", nil, err
			}
			grants = append(grants, roleGrants...)
		}

	}

	return grants, "", nil, nil
}

func (c *channelBuilder) getChannelGrantForRole(resource *v2.Resource, guild *discordgo.Guild, channel *discordgo.Channel, permission *discordgo.PermissionOverwrite) ([]*v2.Grant, error) {
	var grants []*v2.Grant
	role, err := c.getRole(guild.ID, permission.ID)
	if err != nil {
		return nil, err
	}

	for _, channelPerm := range channelPermissions {
		if role.Permissions&channelPerm != channelPerm {
			continue
		}

		grant, err := newChannelRolePermissionGrant(resource, guild, role, channel, channelPerm)
		if err != nil {
			return nil, err
		}

		grants = append(grants, grant)
	}
	return grants, nil
}
func (c *channelBuilder) getChannelGrantForMember(resource *v2.Resource, guild *discordgo.Guild, channel *discordgo.Channel, permission *discordgo.PermissionOverwrite) ([]*v2.Grant, error) {
	var grants []*v2.Grant
	member, err := c.getMember(guild.ID, permission.ID)
	if err != nil {
		return nil, err
	}
	userPermissionsBitmask, err := c.conn.UserChannelPermissions(member.User.ID, channel.ID)
	if err != nil {
		return nil, err
	}
	for _, channelPerm := range channelPermissions {
		if userPermissionsBitmask&channelPerm != channelPerm {
			continue
		}

		grant, err := newChannelUserPermissionGrant(resource, guild, member, channel, channelPerm)
		if err != nil {
			return nil, err
		}

		grants = append(grants, grant)
	}
	return grants, nil
}

func newChannelBuilder(s *discordgo.Session) *channelBuilder {
	return &channelBuilder{
		conn:         s,
		memberCache:  make(map[string]map[string]*discordgo.Member),
		roleCache:    make(map[string]map[string]*discordgo.Role),
		channelCache: make(map[string]map[string]*discordgo.Channel),
	}
}
