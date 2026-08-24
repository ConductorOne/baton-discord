package connector

import (
	"context"
	"fmt"
	"io"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"

	"github.com/ConductorOne/baton-discord/pkg/client"
)

// Connector syncs Discord servers, members, roles, and channel permissions.
type Connector struct {
	client *client.Client
}

// ResourceSyncers returns a ResourceSyncer for each resource type that should
// be synced from Discord.
func (d *Connector) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncerV2 {
	return []connectorbuilder.ResourceSyncerV2{
		newGuildBuilder(d.client),
		newUserBuilder(d.client),
		newRoleBuilder(d.client),
		newChannelBuilder(d.client),
	}
}

// Asset is unused: Discord avatars and icons are public CDN URLs, which are
// surfaced as trait icons rather than proxied through the connector.
func (d *Connector) Asset(ctx context.Context, asset *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

// Metadata returns metadata about the connector.
func (d *Connector) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "Discord",
		Description: "Syncs Discord servers, members, roles, and channel permissions.",
	}, nil
}

// Validate exercises the bot's credentials and the access the sync depends on.
//
// Three distinct misconfigurations are checked separately, because each one
// fails somewhere different at sync time:
//
//  1. an invalid bot token;
//  2. a bot that has not been added to any server, so there is nothing to sync;
//  3. a bot application without the privileged Server Members Intent, which
//     makes member listing return 403.
//
// The third is the one worth probing explicitly. Without it the connector would
// validate cleanly, sync servers, roles, and channels, and then report zero
// users and zero memberships with no visible cause.
func (d *Connector) Validate(ctx context.Context) (annotations.Annotations, error) {
	if _, err := d.client.CurrentUser(ctx); err != nil {
		return nil, fmt.Errorf("baton-discord: could not authenticate with the provided bot token: %w", err)
	}

	guilds, _, err := d.client.GuildsPage(ctx, "")
	if err != nil {
		return nil, err
	}
	if len(guilds) == 0 {
		return nil, fmt.Errorf(
			"baton-discord: the bot is not a member of any Discord server; " +
				"invite it to the servers you want to govern before syncing")
	}

	// The Server Members Intent is an application-wide setting, so probing one
	// server is enough to catch the common misconfiguration. Per-server
	// permission is not application-wide, though, so a gap in some other server
	// still validates clean here and shows up as that server syncing no
	// members. The probed server is named in the error so a failure is
	// attributable rather than looking like a global outage.
	probeGuild := guilds[0]
	if _, _, err := d.client.MembersPage(ctx, probeGuild.ID.String(), ""); err != nil {
		return nil, fmt.Errorf(
			"baton-discord: member listing failed for server %q (%s), used here as the "+
				"sample server for validation: %w", probeGuild.Name, probeGuild.ID, err)
	}

	return nil, nil
}

// Close releases the connector's resources.
func (d *Connector) Close() error {
	if d.client != nil {
		return d.client.Close()
	}
	return nil
}

// New returns a new instance of the connector. baseURL is only for tests; the
// empty string selects the public Discord API.
func New(ctx context.Context, token string, baseURL string) (*Connector, error) {
	discordClient, err := client.New(ctx, token, baseURL)
	if err != nil {
		return nil, err
	}

	return &Connector{client: discordClient}, nil
}
