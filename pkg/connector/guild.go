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
