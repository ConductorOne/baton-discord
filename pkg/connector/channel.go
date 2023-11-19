package connector

import (
	"context"
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
func (c *channelBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant

	debugLog(fmt.Sprintf("channelBuilder.Grants: %+v", resource))

	guild, err := c.conn.Guild(resource.ParentResourceId.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	channel, err := c.conn.Channel(resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}
	for _, permissionOverrideMember := range channel.PermissionOverwrites {
		if permissionOverrideMember.Type != discordgo.PermissionOverwriteTypeMember {
			continue
		}
		member, err := c.conn.GuildMember(guild.ID, permissionOverrideMember.ID)
		if err != nil {
			return nil, "", nil, err
		}
		userPermissionsBitmask, err := c.conn.UserChannelPermissions(member.User.ID, channel.ID)
		if err != nil {
			return nil, "", nil, err
		}
		for _, permission := range channelPermissions {
			if userPermissionsBitmask&permission != permission {
				continue
			}

			grant, err := newChannelUserPermissionGrant(resource, guild, member, channel, permission)
			if err != nil {
				return nil, "", nil, err
			}

			grants = append(grants, grant)
		}
	}

	return grants, "", nil, nil
}

func newChannelBuilder(s *discordgo.Session) *channelBuilder {
	return &channelBuilder{conn: s}
}
