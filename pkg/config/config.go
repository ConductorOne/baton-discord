package config

//go:generate go run ./gen

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	TokenField = field.StringField(
		"token",
		field.WithDisplayName("Bot Token"),
		field.WithDescription("Bot token used to authenticate to Discord."),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)
	BaseURLField = field.StringField(
		"base-url",
		field.WithDisplayName("Base URL"),
		field.WithDescription("Override the Discord API URL (for testing)"),
	)

	// ConfigurationFields defines the external configuration required for the
	// connector to run.
	ConfigurationFields = []field.SchemaField{
		TokenField,
		BaseURLField,
	}

	Configuration = field.NewConfiguration(
		ConfigurationFields,
		field.WithConnectorDisplayName("Discord"),
	)
)
