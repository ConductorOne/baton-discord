![Baton Logo](./docs/images/baton-logo.png)

# `baton-discord` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-discord.svg)](https://pkg.go.dev/github.com/conductorone/baton-discord) ![main ci](https://github.com/conductorone/baton-discord/actions/workflows/main.yaml/badge.svg)

`baton-discord` is a connector for discord built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It communicates with the discord API to sync data about which roles and users have access to guilds and channels that your DiscordBot is a member of.

Check out [Baton](https://github.com/conductorone/baton) to learn more the project in general.

# Getting Started

## brew

```
brew install conductorone/baton/baton conductorone/baton/baton-discord
baton-discord
baton resources
```

## docker

```
docker run --rm -v $(pwd):/out -e BATON_TOKEN=discordAppToken -e ghcr.io/conductorone/baton-discord:latest -f "/out/sync.c1z"
docker run --rm -v $(pwd):/out ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-discord/cmd/baton-discord@main

BATON_TOKEN=discordAppToken 
baton resources
```

# Data Model

`baton-discord` will pull down information about the following discord resources:

* Guilds
* Roles
* Channels
* Users

# Contributing, Support and Issues

We started Baton because we were tired of taking screenshots and manually building spreadsheets. We welcome contributions, and ideas, no matter how small -- our goal is to make identity and permissions sprawl less painful for everyone. If you have questions, problems, or ideas: Please open a GitHub Issue!

See [CONTRIBUTING.md](https://github.com/ConductorOne/baton/blob/main/CONTRIBUTING.md) for more details.

# `baton-discord` Command Line Usage

```
baton-discord

Usage:
  baton-discord [flags]
  baton-discord [command]

Available Commands:
  capabilities       Get connector capabilities
  completion         Generate the autocompletion script for the specified shell
  help               Help about any command

Flags:
      --client-id string       The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string   The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
  -f, --file string            The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
  -h, --help                   help for baton-discord
      --log-format string      The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string       The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
  -p, --provisioning           This must be set in order for provisioning actions to be enabled. ($BATON_PROVISIONING)
      --token string           The discord bot token. ($BATON_TOKEN)
  -v, --version                version for baton-discord
```
