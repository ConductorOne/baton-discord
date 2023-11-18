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

	guildCache map[string]*discordgo.Guild
	userCache  map[string]map[string]*discordgo.Member
	roleCache  map[string]map[string]*discordgo.Role
}

func (o *roleBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return roleResourceType
}

func newRoleResource(role *discordgo.Role, guild *discordgo.Guild) (*v2.Resource, error) {
	guildResource, err := resource.NewResourceID(guildResourceType, guild.ID)
	if err != nil {
		return nil, err
	}

	group, err := resource.NewRoleResource(
		role.Name,
		roleResourceType,
		role.ID,
		nil,
		resource.WithParentResourceID(guildResource),
	)
	if err != nil {
		return nil, err
	}

	return group, nil
}

// List returns all the guilds from the database as resource objects.
func (o *roleBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	resources := []*v2.Resource{}

	for _, guild := range o.conn.State.Guilds {
		roles, err := o.conn.GuildRoles(guild.ID)
		if err != nil {
			return nil, "", nil, err
		}

		for _, role := range roles {
			group, err := newRoleResource(role, guild)
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

func (c *roleBuilder) getGuild(guildID string) (*discordgo.Guild, error) {
	guild, ok := c.guildCache[guildID]
	if !ok {
		var err error
		guild, err = c.conn.Guild(guildID)
		if err != nil {
			return nil, err
		}
		c.guildCache[guildID] = guild
	}

	return guild, nil
}

func (c *roleBuilder) getMembers(guildID string) (map[string]*discordgo.Member, error) {
	userCache, ok := c.userCache[guildID]
	if !ok {
		userCache = make(map[string]*discordgo.Member)
		c.userCache[guildID] = userCache

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

	user, ok := c.userCache[guildID]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

func (c *roleBuilder) getRole(guildID string, roleID string) (*discordgo.Role, error) {
	roleCache, ok := c.roleCache[guildID]
	if !ok {
		roleCache = make(map[string]*discordgo.Role)
		c.roleCache[guildID] = roleCache

		roles, err := c.conn.GuildRoles(guildID)
		if err != nil {
			return nil, err
		}

		for _, role := range roles {
			roleCache[role.ID] = role
		}
	}

	role, ok := c.roleCache[guildID][roleID]
	if !ok {
		return nil, errors.New("role not found")
	}
	return role, nil
}

func (c *roleBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant

	debugLog(fmt.Sprintf("roleBuilder.Grants: %+v", resource))
	guildID := resource.ParentResourceId.Resource
	guild, err := c.getGuild(guildID)
	if err != nil {
		return nil, "", nil, err
	}

	members, err := c.getMembers(guild.ID)
	if err != nil {
		return nil, "", nil, err
	}

	for _, member := range members {
		userPrincipal, err := newUserResource(member.User, guild)
		if err != nil {
			return nil, "", nil, err
		}
		for _, role := range member.Roles {
			if role != resource.Id.Resource {
				continue
			}
			discordRole, err := c.getRole(guildID, role)
			if err != nil {
				return nil, "", nil, err
			}

			grants = append(
				grants,
				grant.NewGrant(
					resource,
					newRoleAssignmentEntitlement(resource, discordRole.Name).DisplayName,
					userPrincipal,
				),
			)
		}
	}

	return grants, "", nil, nil
}

func newRoleBuilder(s *discordgo.Session) *roleBuilder {
	return &roleBuilder{
		conn:       s,
		guildCache: make(map[string]*discordgo.Guild),
		userCache:  make(map[string]map[string]*discordgo.Member),
		roleCache:  make(map[string]map[string]*discordgo.Role),
	}
}
