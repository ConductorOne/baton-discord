package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/disgoorg/disgo/discord"

	resource_sdk "github.com/conductorone/baton-sdk/pkg/types/resource"

	"github.com/ConductorOne/baton-discord/pkg/client"
)

const userResourceTypeID = "user"

var userResourceType = &v2.ResourceType{
	Id:          userResourceTypeID,
	DisplayName: "User",
	Description: "A Discord account that is a member of a synced server.",
	Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_USER},
}

type userBuilder struct {
	client *client.Client
}

func newUserBuilder(c *client.Client) *userBuilder {
	return &userBuilder{client: c}
}

func (o *userBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return userResourceType
}

// memberDisplayName mirrors Discord's own precedence: the server nickname, then
// the account's display name, then its username.
func memberDisplayName(member discord.Member) string {
	if member.Nick != nil && *member.Nick != "" {
		return *member.Nick
	}
	if member.User.GlobalName != nil && *member.User.GlobalName != "" {
		return *member.User.GlobalName
	}
	return member.User.Username
}

// newMemberResource builds a user resource for a member of a server.
func newMemberResource(member discord.Member, guildID string) (*v2.Resource, error) {
	if member.User.ID == 0 {
		// Discord omits the user object when the bot application lacks the
		// Server Members Intent. The client rejects such a page before it gets
		// here; this is the belt-and-braces guard for any other caller.
		return nil, fmt.Errorf(
			"baton-discord: server %s returned a member with no user object; "+
				"enable the Server Members Intent on the bot application", guildID)
	}

	guildResourceID, err := resource_sdk.NewResourceID(guildResourceType, guildID)
	if err != nil {
		return nil, err
	}

	accountType := v2.UserTrait_ACCOUNT_TYPE_HUMAN
	if member.User.Bot {
		accountType = v2.UserTrait_ACCOUNT_TYPE_SERVICE
	}

	profile := map[string]any{
		profileKeyUserID: member.User.ID.String(),
		"username":       member.User.Username,
		"is_bot":         member.User.Bot,
	}
	if member.Nick != nil && *member.Nick != "" {
		profile["nickname"] = *member.Nick
	}
	if member.User.GlobalName != nil && *member.User.GlobalName != "" {
		profile["global_name"] = *member.User.GlobalName
	}

	userTraits := []resource_sdk.UserTraitOption{
		resource_sdk.WithAccountType(accountType),
		resource_sdk.WithUserLogin(member.User.Username),
	}

	// Profile, status, icon, and created-at moved from the user trait onto the
	// resource itself in baton-sdk v0.24; the trait-scoped options for these
	// are deprecated.
	resourceOptions := []resource_sdk.ResourceOption{
		resource_sdk.WithParentResourceID(guildResourceID),
		resource_sdk.WithExternalID(&v2.ExternalId{Id: member.User.ID.String()}),
		resource_sdk.WithResourceProfile(profile),
		// Discord exposes no per-member enabled/disabled state to a bot. A
		// member that appears in the listing is an active member; removals and
		// bans surface as the member disappearing.
		resource_sdk.WithResourceStatus(v2.Status_RESOURCE_STATUS_ENABLED, ""),
	}
	if avatarURL := member.User.EffectiveAvatarURL(); avatarURL != "" {
		resourceOptions = append(resourceOptions, resource_sdk.WithResourceIcon(&v2.AssetRef{Id: avatarURL}))
	}
	// Discord snowflakes encode their creation timestamp, so the account's
	// creation time is derivable and is a property of the account rather than of
	// any one server. The member's join date is per-server and would be
	// whichever server was written last on an account in several of them.
	resourceOptions = append(resourceOptions,
		resource_sdk.WithResourceCreatedAt(member.User.ID.Time()))

	return resource_sdk.NewUserResource(
		memberDisplayName(member),
		userResourceType,
		member.User.ID.String(),
		userTraits,
		resourceOptions...,
	)
}

// List returns the members of one server, a page at a time.
//
// A Discord account can belong to many servers, so the same user:<snowflake>
// resource is emitted once per server the bot can see, parented to that server.
// The resource ID stays the account snowflake, which is globally stable.
func (o *userBuilder) List(
	ctx context.Context,
	parentResourceID *v2.ResourceId,
	opts resource_sdk.SyncOpAttrs,
) ([]*v2.Resource, *resource_sdk.SyncOpResults, error) {
	// Users are only reachable through a server. The SDK also lists every
	// resource type once with no parent, and that pass has nothing to do here;
	// the per-server listings are driven by the guild's ChildResourceType
	// annotations.
	if parentResourceID == nil {
		return nil, nil, nil
	}

	bag, err := parsePageToken(opts.PageToken.Token, userResourceTypeID)
	if err != nil {
		return nil, nil, err
	}

	guildID := parentResourceID.Resource
	members, nextCursor, err := o.client.MembersPage(ctx, guildID, bag.PageToken())
	if err != nil {
		return nil, nil, err
	}

	resources := make([]*v2.Resource, 0, len(members))
	for _, member := range members {
		memberResource, err := newMemberResource(member, guildID)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, memberResource)
	}

	nextPage, err := bag.NextToken(nextCursor)
	if err != nil {
		return nil, nil, err
	}

	return resources, syncResults(nextPage), nil
}

// Entitlements is empty: user accounts carry no access of their own. Access is
// modelled on servers, roles, and channels.
func (o *userBuilder) Entitlements(
	_ context.Context,
	_ *v2.Resource,
	_ resource_sdk.SyncOpAttrs,
) ([]*v2.Entitlement, *resource_sdk.SyncOpResults, error) {
	return nil, nil, nil
}

// Grants is empty for the same reason as Entitlements.
func (o *userBuilder) Grants(
	_ context.Context,
	_ *v2.Resource,
	_ resource_sdk.SyncOpAttrs,
) ([]*v2.Grant, *resource_sdk.SyncOpResults, error) {
	return nil, nil, nil
}
