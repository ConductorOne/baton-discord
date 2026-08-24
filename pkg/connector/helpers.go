package connector

import (
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	resource_sdk "github.com/conductorone/baton-sdk/pkg/types/resource"
)

// Shared resource-profile keys.
const (
	profileKeyGuildID = "guild_id"
	profileKeyUserID  = "user_id"
)

// parsePageToken restores a pagination bag from an incoming page token,
// seeding it on the first call.
func parsePageToken(token string, resourceTypeID string) (*pagination.Bag, error) {
	bag := &pagination.Bag{}

	if err := bag.Unmarshal(token); err != nil {
		return nil, fmt.Errorf("baton-discord: failed to parse page token: %w", err)
	}

	if bag.Current() == nil {
		bag.Push(pagination.PageState{ResourceTypeID: resourceTypeID})
	}

	return bag, nil
}

// syncResults wraps a next-page token in the result shape the V2 syncer
// interface returns.
func syncResults(nextPageToken string) *resource_sdk.SyncOpResults {
	return &resource_sdk.SyncOpResults{NextPageToken: nextPageToken}
}

// entitlementSlug recovers the slug from an entitlement.
//
// Baton entitlement IDs are "<resourceType>:<resourceID>:<slug>". The Slug field
// is populated when the connector built the entitlement, but a provisioning
// request arrives from C1 and is not guaranteed to carry it, so the ID is the
// reliable source. Splitting on only the first two separators keeps slugs that
// themselves contain a colon intact.
func entitlementSlug(ent *v2.Entitlement) string {
	if ent == nil {
		return ""
	}
	if ent.Slug != "" {
		return ent.Slug
	}

	parts := strings.SplitN(ent.Id, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// requireEntitlementSlug checks that a provisioning request targets an
// entitlement this connector knows how to act on.
func requireEntitlementSlug(ent *v2.Entitlement, want string) error {
	got := entitlementSlug(ent)
	if got != want {
		return fmt.Errorf("baton-discord: unsupported entitlement %q, expected %q", got, want)
	}
	return nil
}

// requireResourceType checks a resource's type before its ID is used as a
// Discord snowflake for that kind of object.
func requireResourceType(resource *v2.Resource, want string) error {
	if resource == nil || resource.Id == nil {
		return fmt.Errorf("baton-discord: missing %s resource", want)
	}
	if resource.Id.ResourceType != want {
		return fmt.Errorf("baton-discord: expected a %s resource, got %q", want, resource.Id.ResourceType)
	}
	return nil
}

// parentGuildID returns the server a resource belongs to.
//
// Roles and channels each exist in exactly one server, so the resource's own
// parent is the authoritative scope. Reading the server from the principal
// instead would be wrong: a Discord account can be a member of many servers,
// and its parent is whichever one happened to emit it.
func parentGuildID(resource *v2.Resource) (string, error) {
	if resource == nil || resource.ParentResourceId == nil || resource.ParentResourceId.Resource == "" {
		return "", fmt.Errorf("baton-discord: resource %q has no parent server", resource.GetId().GetResource())
	}
	if resource.ParentResourceId.ResourceType != guildResourceTypeID {
		return "", fmt.Errorf(
			"baton-discord: resource %q has parent type %q, expected %q",
			resource.Id.Resource, resource.ParentResourceId.ResourceType, guildResourceTypeID)
	}
	return resource.ParentResourceId.Resource, nil
}

// guildIDForProvisioning resolves the server a grant or revoke acts on.
//
// The entitlement's resource is the authoritative source, per parentGuildID.
// The principal is only a fallback for a provisioning request that did not
// round-trip the entitlement resource's parent: an account can belong to many
// servers, so its parent is whichever server happened to emit it, and using it
// as the primary source could act on the wrong server. Falling back to it beats
// failing the operation outright when it is the only parent available.
func guildIDForProvisioning(entitlementResource *v2.Resource, principal *v2.Resource) (string, error) {
	guildID, err := parentGuildID(entitlementResource)
	if err == nil {
		return guildID, nil
	}

	if principal != nil &&
		principal.ParentResourceId != nil &&
		principal.ParentResourceId.Resource != "" &&
		principal.ParentResourceId.ResourceType == guildResourceTypeID {
		return principal.ParentResourceId.Resource, nil
	}

	return "", err
}
