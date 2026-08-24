package connector

import (
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
)

// Compile-time checks that each builder implements the interfaces the SDK
// discovers by type assertion at runtime.
//
// Without these, dropping or mistyping a Grant or Revoke method does not fail
// the build — the SDK simply stops advertising provisioning for that resource
// type, and the loss only shows up as a capability quietly missing in C1.
var (
	_ connectorbuilder.ConnectorBuilderV2 = (*Connector)(nil)

	_ connectorbuilder.ResourceProvisionerV2 = (*guildBuilder)(nil)
	_ connectorbuilder.ResourceProvisionerV2 = (*roleBuilder)(nil)
	_ connectorbuilder.ResourceProvisionerV2 = (*channelBuilder)(nil)

	// Users are sync-only: Discord accounts are not created or managed here.
	_ connectorbuilder.ResourceSyncerV2 = (*userBuilder)(nil)
)
