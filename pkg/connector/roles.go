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
	"github.com/conductorone/baton-sdk/pkg/types/resource"
)

var roleResourceTypeID = "role"

var roleResourceType = &v2.ResourceType{
	Id:          roleResourceTypeID,
	DisplayName: "Role",
	Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_ROLE},
}

type roleBuilder struct {
	conn *discordgo.Session
}

func (o *roleBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return roleResourceType
}

// List returns all the guilds from the database as resource objects.
func (o *roleBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	resources := []*v2.Resource{}

	for _, guild := range o.conn.State.Guilds {
		groups, err := o.conn.GuildRoles(guild.ID)
		if err != nil {
			return nil, "", nil, err
		}

		for _, group := range groups {
			group, err := resource.NewRoleResource(group.Name, roleResourceType, group.ID, nil, resource.WithParentResourceID(&v2.ResourceId{
				ResourceType: guildResourceTypeID,
				Resource:     guild.ID,
			}))
			if err != nil {
				return nil, "", nil, err
			}
			resources = append(resources, group)
		}
	}

	return resources, "", nil, nil
}

func (o *roleBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	roles, err := o.conn.GuildRoles(resource.ParentResourceId.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	var role *discordgo.Role
	for _, thisRole := range roles {
		if thisRole.ID != resource.Id.Resource {
			continue
		}

		role = thisRole
		break
	}

	if role == nil {
		return nil, "", nil, errors.New("role not found")
	}

	return []*v2.Entitlement{
		newRoleAssignmentEntitlement(resource, role.Name),
	}, "", nil, nil
}

func newRoleAssignmentEntitlement(resource *v2.Resource, name string) *v2.Entitlement {
	return entitlement.NewAssignmentEntitlement(
		resource,
		fmt.Sprintf("Member of %s", name),
		entitlement.WithGrantableTo(userResourceType),
	)
}

func (c *roleBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant

	guild, err := c.conn.Guild(resource.ParentResourceId.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	guildMembers, err := c.conn.GuildMembers(resource.ParentResourceId.Resource, pToken.Token, pToken.Size)
	if err != nil {
		return nil, "", nil, err
	}

	roles, err := c.conn.GuildRoles(resource.ParentResourceId.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	var dgRole *discordgo.Role
	for _, thisRole := range roles {
		if thisRole.ID != resource.Id.Resource {
			continue
		}

		dgRole = thisRole
		break
	}

	for _, member := range guildMembers {
		userPrincipal, err := newUserResource(member.User, guild)
		if err != nil {
			return nil, "", nil, err
		}
		for _, role := range member.Roles {
			if role != resource.Id.Resource {
				continue
			}

			grants = append(
				grants,
				grant.NewGrant(
					resource,
					newRoleAssignmentEntitlement(resource, dgRole.Name).DisplayName,
					userPrincipal,
				),
			)

			break
		}
	}

	nextPageToken := ""
	if len(guildMembers) > 0 {
		nextPageToken = guildMembers[len(guildMembers)-1].User.ID
	}

	return grants, nextPageToken, nil, nil
}

func newRoleBuilder(s *discordgo.Session) *roleBuilder {
	return &roleBuilder{conn: s}
}
