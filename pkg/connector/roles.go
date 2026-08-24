package connector

import (
	"context"
	"fmt"
	"slices"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"

	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	resource_sdk "github.com/conductorone/baton-sdk/pkg/types/resource"

	"github.com/disgoorg/snowflake/v2"

	"github.com/ConductorOne/baton-discord/pkg/client"
)

const roleResourceTypeID = "role"

var roleResourceType = &v2.ResourceType{
	Id:          roleResourceTypeID,
	DisplayName: "Role",
	Description: "A role within a Discord server.",
	Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_ROLE},
}

type roleBuilder struct {
	client *client.Client
}

func newRoleBuilder(c *client.Client) *roleBuilder {
	return &roleBuilder{client: c}
}

func (r *roleBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return roleResourceType
}

// isEveryoneRole reports whether a role is the server's @everyone role.
//
// Discord gives @everyone the same snowflake as the server itself, and every
// member holds it implicitly — it never appears in a member's Roles list.
func isEveryoneRole(roleID, guildID string) bool {
	return roleID == guildID
}

// List returns the roles of one server. Discord returns this collection whole,
// so there is nothing to paginate.
func (r *roleBuilder) List(
	ctx context.Context,
	parentResourceID *v2.ResourceId,
	_ resource_sdk.SyncOpAttrs,
) ([]*v2.Resource, *resource_sdk.SyncOpResults, error) {
	// Roles are only reachable through a server; see the note in userBuilder.List.
	if parentResourceID == nil {
		return nil, nil, nil
	}

	guildID := parentResourceID.Resource
	guildResourceID, err := resource_sdk.NewResourceID(guildResourceType, guildID)
	if err != nil {
		return nil, nil, err
	}

	roles, err := r.client.Roles(ctx, guildID)
	if err != nil {
		return nil, nil, err
	}

	resources := make([]*v2.Resource, 0, len(roles))
	for _, role := range roles {
		profile := map[string]any{
			"role_id":         role.ID.String(),
			profileKeyGuildID: guildID,
			"position":        role.Position,
			// Integration-managed roles are owned by a bot or subscription and
			// cannot be assigned or removed through the API.
			"managed": role.Managed,
			// The role's server-wide permission bitmask. Kept as a string:
			// Discord permissions exceed 2^31 and formatting the int64 with a
			// float verb would render large values in scientific notation.
			"permissions": fmt.Sprintf("%d", role.Permissions),
		}

		roleResource, err := resource_sdk.NewRoleResource(
			role.Name,
			roleResourceType,
			role.ID.String(),
			nil,
			resource_sdk.WithParentResourceID(guildResourceID),
			resource_sdk.WithExternalID(&v2.ExternalId{Id: role.ID.String()}),
			resource_sdk.WithResourceProfile(profile),
		)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, roleResource)
	}

	return resources, nil, nil
}

// Entitlements returns the single assignment entitlement for a role.
//
// Note that no API call is needed. The previous implementation fetched the role
// purely to build a display-name-derived slug; with a stable slug the role
// resource already carries everything required.
func (r *roleBuilder) Entitlements(
	_ context.Context,
	resource *v2.Resource,
	_ resource_sdk.SyncOpAttrs,
) ([]*v2.Entitlement, *resource_sdk.SyncOpResults, error) {
	return []*v2.Entitlement{
		entitlement.NewAssignmentEntitlement(
			resource,
			roleMemberEntitlement,
			entitlement.WithDisplayName(fmt.Sprintf("%s Role Member", resource.DisplayName)),
			entitlement.WithDescription(fmt.Sprintf("Assigned the %s role in Discord", resource.DisplayName)),
			entitlement.WithGrantableTo(userResourceType),
		),
	}, nil, nil
}

// Grants returns the members holding a role, a page at a time.
//
// Discord has no "members with role X" endpoint, so this pages the server's
// members and filters locally. Paging rather than caching every member of the
// server is deliberate: the previous implementation held every member of every
// synced server in memory for the life of the process, which is unbounded in
// server size.
func (r *roleBuilder) Grants(
	ctx context.Context,
	resource *v2.Resource,
	opts resource_sdk.SyncOpAttrs,
) ([]*v2.Grant, *resource_sdk.SyncOpResults, error) {
	guildID, err := parentGuildID(resource)
	if err != nil {
		return nil, nil, err
	}

	bag, err := parsePageToken(opts.PageToken.Token, roleResourceTypeID)
	if err != nil {
		return nil, nil, err
	}

	members, nextCursor, err := r.client.MembersPage(ctx, guildID, bag.PageToken())
	if err != nil {
		return nil, nil, err
	}

	roleID := resource.Id.Resource
	everyone := isEveryoneRole(roleID, guildID)

	var grants []*v2.Grant
	for _, member := range members {
		if !everyone && !memberHasRole(member.RoleIDs, roleID) {
			continue
		}

		principal, err := newMemberResource(member, guildID)
		if err != nil {
			return nil, nil, err
		}
		grants = append(grants, grant.NewGrant(resource, roleMemberEntitlement, principal))
	}

	nextPage, err := bag.NextToken(nextCursor)
	if err != nil {
		return nil, nil, err
	}

	return grants, syncResults(nextPage), nil
}

func memberHasRole(roleIDs []snowflake.ID, roleID string) bool {
	return slices.ContainsFunc(roleIDs, func(id snowflake.ID) bool {
		return id.String() == roleID
	})
}

// Grant assigns a role to a member.
func (r *roleBuilder) Grant(
	ctx context.Context,
	principal *v2.Resource,
	ent *v2.Entitlement,
) ([]*v2.Grant, annotations.Annotations, error) {
	if err := requireEntitlementSlug(ent, roleMemberEntitlement); err != nil {
		return nil, nil, err
	}
	if err := requireResourceType(ent.Resource, roleResourceTypeID); err != nil {
		return nil, nil, err
	}
	if err := requireResourceType(principal, userResourceTypeID); err != nil {
		return nil, nil, err
	}

	guildID, err := guildIDForProvisioning(ent.Resource, principal)
	if err != nil {
		return nil, nil, err
	}

	roleID := ent.Resource.Id.Resource
	if isEveryoneRole(roleID, guildID) {
		return nil, nil, fmt.Errorf(
			"baton-discord: the @everyone role is held implicitly by all members and cannot be assigned")
	}

	err = r.client.AddMemberRole(ctx, guildID, principal.Id.Resource, roleID,
		"Role granted by ConductorOne")
	if err != nil {
		return nil, nil, err
	}

	// Adding a role the member already holds answers 204, so the operation is
	// idempotent and needs no already-exists special case.
	//
	// Returning the grant lets C1 record the new access immediately instead of
	// waiting for the next sync to discover it. The ID matches what the sync
	// path emits, so the two cannot disagree.
	return []*v2.Grant{
		grant.NewGrant(ent.Resource, roleMemberEntitlement, principal),
	}, nil, nil
}

// Revoke removes a role from a member.
func (r *roleBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	if err := requireEntitlementSlug(g.Entitlement, roleMemberEntitlement); err != nil {
		return nil, err
	}
	if err := requireResourceType(g.Entitlement.Resource, roleResourceTypeID); err != nil {
		return nil, err
	}

	guildID, err := guildIDForProvisioning(g.Entitlement.Resource, g.Principal)
	if err != nil {
		return nil, err
	}

	roleID := g.Entitlement.Resource.Id.Resource
	if isEveryoneRole(roleID, guildID) {
		return nil, fmt.Errorf(
			"baton-discord: the @everyone role is held implicitly by all members and cannot be removed")
	}

	err = r.client.RemoveMemberRole(ctx, guildID, g.Principal.Id.Resource, roleID,
		"Role revoked by ConductorOne")
	if err != nil {
		// The member or the assignment already being gone is the desired end state.
		if client.IsNotFound(err) {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		return nil, err
	}

	return nil, nil
}
