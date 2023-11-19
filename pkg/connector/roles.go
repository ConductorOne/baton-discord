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
	"slices"
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

func (r *roleBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
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
func (r *roleBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	resources := []*v2.Resource{}

	for _, guild := range r.conn.State.Guilds {
		roles, err := r.conn.GuildRoles(guild.ID)
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

func (r *roleBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	role, err := r.getRole(resource.ParentResourceId.Resource, resource.Id.Resource)
	if err != nil {
		return nil, "", nil, fmt.Errorf("role not found: %w", err)
	}

	entitlements := []*v2.Entitlement{
		newRoleAssignmentEntitlement(resource, role.Name),
	}
	for _, permission := range append(channelPermissions, guildPermissions...) {
		entitlements = append(
			entitlements,
			newRolePermissionEntitlement(
				resource,
				role.Name,
				permission,
			),
		)
	}

	return entitlements, "", nil, nil
}

func newRoleAssignmentEntitlement(resource *v2.Resource, name string) *v2.Entitlement {
	return entitlement.NewAssignmentEntitlement(
		resource,
		fmt.Sprintf("Member of %s", name),
		entitlement.WithGrantableTo(userResourceType),
	)
}
func newRolePermissionEntitlement(resource *v2.Resource, name string, permission int64) *v2.Entitlement {
	return entitlement.NewAssignmentEntitlement(
		resource,
		fmt.Sprintf("%s for %s", permNameFromVal[permission], name),
		entitlement.WithGrantableTo(userResourceType),
	)
}

func (r *roleBuilder) getGuild(guildID string) (*discordgo.Guild, error) {
	guild, ok := r.guildCache[guildID]
	if !ok {
		var err error
		guild, err = r.conn.Guild(guildID)
		if err != nil {
			return nil, err
		}
		r.guildCache[guildID] = guild
	}

	return guild, nil
}

func (r *roleBuilder) getMembers(guildID string) (map[string]*discordgo.Member, error) {
	userCache, ok := r.userCache[guildID]
	if !ok {
		userCache = make(map[string]*discordgo.Member)
		r.userCache[guildID] = userCache

		token := ""
		for {
			guildMembers, err := r.conn.GuildMembers(guildID, token, 1000)
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

	user, ok := r.userCache[guildID]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

func (r *roleBuilder) getRole(guildID string, roleID string) (*discordgo.Role, error) {
	roleCache, ok := r.roleCache[guildID]
	if !ok {
		roleCache = make(map[string]*discordgo.Role)
		r.roleCache[guildID] = roleCache

		roles, err := r.conn.GuildRoles(guildID)
		if err != nil {
			return nil, err
		}

		for _, role := range roles {
			roleCache[role.ID] = role
		}
	}

	role, ok := r.roleCache[guildID][roleID]
	if !ok {
		return nil, errors.New("role not found")
	}
	return role, nil
}

func newRolePermissionGrant(resource *v2.Resource, guild *discordgo.Guild, role *discordgo.Role, permission int64) (*v2.Grant, error) {
	rolePrincipal, err := newRoleResource(role, guild)
	if err != nil {
		return nil, err
	}

	return grant.NewGrant(
		resource,
		newRolePermissionEntitlement(resource, role.Name, permission).DisplayName,
		rolePrincipal,
	), nil
}

func (r *roleBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant

	debugLog(fmt.Sprintf("roleBuilder.Grants: %+v", resource))
	guildID := resource.ParentResourceId.Resource
	guild, err := r.getGuild(guildID)
	if err != nil {
		return nil, "", nil, err
	}

	members, err := r.getMembers(guild.ID)
	if err != nil {
		return nil, "", nil, err
	}

	discordRole, err := r.getRole(guildID, resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	for _, permission := range channelPermissions {
		if discordRole.Permissions&permission != permission {
			continue
		}

		role, err := newRolePermissionGrant(
			resource,
			guild,
			discordRole,
			permission,
		)
		if err != nil {
			return nil, "", nil, err
		}

		grants = append(grants, role)
	}

	for _, member := range members {
		userPrincipal, err := newMemberResource(member, guild)
		if err != nil {
			return nil, "", nil, err
		}

		if !slices.Contains(member.Roles, resource.Id.Resource) {
			continue
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
