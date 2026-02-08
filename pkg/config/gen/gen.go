package main

import (
	"github.com/conductorone/baton-sdk/pkg/config"
	cfg "github.com/ConductorOne/baton-discord/pkg/config"
)

func main() {
	config.Generate("discord", cfg.Configuration)
}
