package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var TokenField = field.StringField(
	"token",
	field.WithIsSecret(true),
	field.WithDescription("Bot token used to authenticate to discord."),
)

//go:generate go run ./gen

var Config = field.NewConfiguration([]field.SchemaField{
	TokenField,
})
