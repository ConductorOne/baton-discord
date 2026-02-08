package connector

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/bwmarrin/discordgo"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
)

// DefaultBaseURL is the default Discord API base URL.
const DefaultBaseURL = "https://discord.com/"

type Connector struct {
	conn *discordgo.Session
}

// ResourceSyncers returns a ResourceSyncer for each resource type that should be synced from the upstream service.
func (d *Connector) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncer {
	return []connectorbuilder.ResourceSyncer{
		newUserBuilder(d.conn),
		newGuildBuilder(d.conn),
		newRoleBuilder(d.conn),
		newChannelBuilder(d.conn),
	}
}

// Asset takes an input AssetRef and attempts to fetch it using the connector's authenticated http client
// It streams a response, always starting with a metadata object, following by chunked payloads for the asset.
func (d *Connector) Asset(ctx context.Context, asset *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

// Metadata returns metadata about the connector.
func (d *Connector) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "Discord Baton Connector",
		Description: "An implementation of a Discord connector using Baton.",
	}, nil
}

// Validate is called to ensure that the connector is properly configured. It should exercise any API credentials
// to be sure that they are valid.
func (d *Connector) Validate(ctx context.Context) (annotations.Annotations, error) {
	return nil, nil
}

// New returns a new instance of the connector.
func New(ctx context.Context, token string, baseURL string) (*Connector, error) {
	// Set the base URL if provided (must be done before creating the session)
	if baseURL != "" {
		// Ensure the base URL ends with a slash
		if !strings.HasSuffix(baseURL, "/") {
			baseURL += "/"
		}
		discordgo.EndpointDiscord = baseURL
		// Rebuild the API endpoint with the new base URL
		discordgo.EndpointAPI = discordgo.EndpointDiscord + "api/v" + discordgo.APIVersion + "/"
		// Rebuild dependent endpoints
		discordgo.EndpointGuilds = discordgo.EndpointAPI + "guilds/"
		discordgo.EndpointChannels = discordgo.EndpointAPI + "channels/"
		discordgo.EndpointUsers = discordgo.EndpointAPI + "users/"
		discordgo.EndpointGateway = discordgo.EndpointAPI + "gateway"
		discordgo.EndpointGatewayBot = discordgo.EndpointGateway + "/bot"
		discordgo.EndpointWebhooks = discordgo.EndpointAPI + "webhooks/"
		discordgo.EndpointStickers = discordgo.EndpointAPI + "stickers/"
		discordgo.EndpointStageInstances = discordgo.EndpointAPI + "stage-instances"
		discordgo.EndpointSKUs = discordgo.EndpointAPI + "skus"
		discordgo.EndpointVoice = discordgo.EndpointAPI + "/voice/"
		discordgo.EndpointVoiceRegions = discordgo.EndpointVoice + "regions"
		discordgo.EndpointNitroStickersPacks = discordgo.EndpointAPI + "/sticker-packs"
		discordgo.EndpointGuildCreate = discordgo.EndpointAPI + "guilds"
		discordgo.EndpointApplications = discordgo.EndpointAPI + "applications"
		discordgo.EndpointOAuth2 = discordgo.EndpointAPI + "oauth2/"
		discordgo.EndpointOAuth2Applications = discordgo.EndpointOAuth2 + "applications"
	}

	dcConn, err := discordgo.New(fmt.Sprintf("Bot %s", token))
	if err != nil {
		return nil, err
	}

	dcConn.Identify.Intents = discordgo.IntentsAllWithoutPrivileged | discordgo.IntentGuildMembers
	if err := dcConn.Open(); err != nil {
		return nil, err
	}

	return &Connector{conn: dcConn}, nil
}
