package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
)

var guildResourceTypeID = "guild"

var guildResourceType = &v2.ResourceType{
	Id:          guildResourceTypeID,
	DisplayName: "Guild",
}

type guildBuilder struct {
	conn *discordgo.Session
}

func (o *guildBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return guildResourceType
}

// List returns all the guilds from the database as resource objects.
func (o *guildBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	resources := []*v2.Resource{}
	for _, guild := range o.conn.State.Guilds {
		resources = append(resources, &v2.Resource{
			Id: &v2.ResourceId{
				ResourceType: guildResourceTypeID,
				Resource:     guild.ID,
			},
			DisplayName: guild.Name,
		})
	}
	return resources, "", nil, nil
}

func newGuildAssignmentEntitlement(resource *v2.Resource, name, description string) *v2.Entitlement {
	return entitlement.NewAssignmentEntitlement(
		resource,
		fmt.Sprintf("Access to %s", name),
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDescription(description),
	)
}

// Entitlements always returns an empty slice for guilds.
func (o *guildBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	guild, err := o.conn.Guild(resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	return []*v2.Entitlement{
		newGuildAssignmentEntitlement(resource, guild.Name, guild.Description),
	}, "", nil, nil
}

func (o *guildBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant

	guild, err := o.conn.Guild(resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	guildMembers, err := o.conn.GuildMembers(resource.Id.Resource, pToken.Token, 1000)
	if err != nil {
		return nil, "", nil, err
	}

	for _, member := range guildMembers {
		userPrincipal, err := newMemberResource(member, guild)
		if err != nil {
			return nil, "", nil, err
		}
		grants = append(grants, grant.NewGrant(resource, fmt.Sprintf("Access to %s", guild.Name), userPrincipal))
	}

	nextPageToken := ""
	if len(guildMembers) > 0 {
		nextPageToken = guildMembers[len(guildMembers)-1].User.ID
	}

	return grants, nextPageToken, nil, nil
}

func newGuildBuilder(s *discordgo.Session) *guildBuilder {
	return &guildBuilder{conn: s}
}

func (o *guildBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	if entitlement.Resource.Id.ResourceType != guildResourceTypeID {
		return nil, errors.New("role has no guild parent")
	}

	guild, err := o.conn.Guild(entitlement.Resource.Id.Resource)
	if err != nil {
		return nil, err
	}
	channelID := ""
	// If the server has a rules channel set invite them directly to that
	if guild.RulesChannelID != "" {
		channelID = guild.RulesChannelID
	}

	if channelID == "" {
		// Find channel with the highest member count
		// that's probably the entry or main channel
		count := 0
		for _, channel := range guild.Channels {
			// Skip any channel that's not a guild text channel
			if channel.Type != discordgo.ChannelTypeGuildText {
				continue
			}
			if channel.MemberCount > count {
				channelID = channel.ID
				channel.MemberCount = count
			}
		}
	}

	invite, err := o.conn.ChannelInviteCreate(channelID, discordgo.Invite{
		MaxAge:  int((time.Hour * 24 * 3).Seconds()),
		MaxUses: 1,
		Unique:  true,
	})

	dm, err := o.conn.UserChannelCreate(principal.Id.Resource)
	if err != nil {
		return nil, err
	}

	_, err = o.conn.ChannelMessageSend(dm.ID, fmt.Sprintf("You've been invited to %s, https://discord.gg/%s", invite.Guild.Name, invite.Code))
	return nil, err
}

func (o *guildBuilder) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	guildResource := grant.Entitlement.Resource
	if guildResource.Id.ResourceType != guildResourceTypeID {
		return nil, errors.New("invalid resource type")
	}

	guild, err := o.conn.Guild(guildResource.Id.Resource)
	if err != nil {
		return nil, err
	}

	return nil, o.conn.GuildMemberDeleteWithReason(
		guildResource.Id.Resource,
		grant.Principal.Id.Resource,
		fmt.Sprintf("Access to %s was revoked.", guild.Name),
	)
}
