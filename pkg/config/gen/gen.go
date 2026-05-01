package main

import (
	cfg "github.com/ConductorOne/baton-discord/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/config"
)

func main() {
	config.Generate("discord", cfg.Configuration)
}
