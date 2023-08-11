package connector

import (
	"context"

	"github.com/bwmarrin/discordgo"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	resource_sdk "github.com/conductorone/baton-sdk/pkg/types/resource"
)

var userResourceTypeID = "user"

// The user resource type is for all user objects from the database.
var userResourceType = &v2.ResourceType{
	Id:          userResourceTypeID,
	DisplayName: "User",
	Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_USER},
}

type userBuilder struct {
	conn *discordgo.Session
}

func newUserResource(user *discordgo.User, guild *discordgo.Guild) (*v2.Resource, error) {
	guildResource, err := resource_sdk.NewResourceID(guildResourceType, guild.ID)
	if err != nil {
		return nil, err
	}

	options := []resource_sdk.UserTraitOption{}
	if user.Bot {
		options = append(options, resource_sdk.WithAccountType(v2.UserTrait_ACCOUNT_TYPE_SERVICE))
	} else {
		options = append(options, resource_sdk.WithAccountType(v2.UserTrait_ACCOUNT_TYPE_HUMAN))
	}

	return resource_sdk.NewUserResource(
		user.Username,
		userResourceType,
		user.ID,
		options,
		resource_sdk.WithParentResourceID(guildResource),
	)
}

func (o *userBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return userResourceType
}

// List returns all the users from the database as resource objects.
// Users include a UserTrait because they are the 'shape' of a standard user.
func (o *userBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	resources := []*v2.Resource{}

	for _, guild := range o.conn.State.Guilds {
		nextPageToken := ""
		guild, err := o.conn.Guild(guild.ID)
		if err != nil {
			return nil, "", nil, err
		}

		for {
			members, err := o.conn.GuildMembers(guild.ID, nextPageToken, 1000)
			if err != nil {
				return nil, "", nil, err
			}
			for _, user := range members {
				resource, err := newUserResource(user.User, guild)
				if err != nil {
					return nil, "", nil, err
				}

				resources = append(resources, resource)
			}
			nextPageToken = ""
			if len(members) > 0 {
				nextPageToken = members[len(members)-1].User.ID
			}
			if nextPageToken == "" {
				break
			}
		}
	}

	return resources, "", nil, nil
}

// Entitlements always returns an empty slice for users.
func (o *userBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

// Grants always returns an empty slice for users since they don't have any entitlements.
func (o *userBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func newUserBuilder(s *discordgo.Session) *userBuilder {
	return &userBuilder{conn: s}
}
