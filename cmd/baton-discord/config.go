package main

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var configuration = field.NewConfiguration([]field.SchemaField{
	field.StringField("token", field.WithIsSecret(true), field.WithDescription("Bot token used to authenticate to discord.")),
})
