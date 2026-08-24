![Baton Logo](./docs/images/baton-logo.png)

# `baton-discord` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-discord.svg)](https://pkg.go.dev/github.com/conductorone/baton-discord) ![ci](https://github.com/conductorone/baton-discord/actions/workflows/ci.yaml/badge.svg)

`baton-discord` is a connector for Discord built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It talks to the Discord REST API (via [disgo](https://github.com/disgoorg/disgo)) to sync which users and roles have access to the servers and channels your Discord bot belongs to, and to provision that access.

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

# Discord setup

The connector authenticates as a Discord **bot**. In the
[Discord developer portal](https://discord.com/developers/applications):

1. Create an application and add a bot to it.
2. On the bot's page, enable the **Server Members Intent** under *Privileged
   Gateway Intents*. This is required: without it Discord answers `403` when the
   connector lists server members, so no users, server memberships, or role
   assignments can be synced.
3. Copy the bot token and pass it as `BATON_TOKEN`.
4. Invite the bot to each server you want to govern. The connector only sees
   servers the bot is a member of.

For provisioning, the bot's own role also needs permissions in the target
server, and Discord's role hierarchy applies:

| Operation | Required permission |
|-|-|
| Assign or remove a role | `Manage Roles`, and the bot's highest role must be **above** the role being assigned |
| Remove a member | `Kick Members` |
| Grant or revoke a channel permission | `Manage Roles` on the channel |
| Invite a user to a server | `Create Instant Invite` |

The connector's validation step, which runs before a sync starts, checks the
token, that the bot belongs to at least one server, and that member listing is
permitted — so a missing intent fails loudly at startup with a message naming
the intent, rather than producing a sync with zero users.

# Data Model

`baton-discord` syncs the following Discord resources. Servers are the top-level
resource; users, roles, and channels are synced per server.

| Resource | Baton type | Entitlements | Grants |
|-|-|-|-|
| Server (guild) | `guild` | `access` | one per member |
| User | `user` (user trait) | none | none |
| Role | `role` (role trait) | `member` | members holding the role |
| Channel | `channel` | one per governable channel permission | channel permission overwrites |

Notes on the access model:

* **Users** are Discord accounts. A single account can belong to several
  servers, so the same `user:<snowflake>` resource is emitted once per server,
  parented to that server. Bot accounts are marked as service accounts.
* **Roles** include `@everyone`, which Discord gives the same snowflake as the
  server. Every member holds it implicitly, so it is reported as granted to all
  members even though it never appears in a member's role list. It cannot be
  assigned or revoked.
* **Channels** cover text, announcement, forum, voice, and stage channels.
  Categories are skipped (they hold no members) and so are threads (they inherit
  their parent channel's overwrites).
* **Channel grants come only from explicit `allow` bits** on the channel's
  permission overwrites. Effective access in Discord is the result of layering
  `@everyone`, role, and member overwrites over a member's server-wide role
  permissions; reporting that computed union as a direct channel grant would
  misattribute inherited access.
* **Entitlement slugs are stable identifiers** (`access`, `member`, and
  permission names such as `send_messages`), not display text. Baton derives
  entitlement and grant IDs from the slug, so a display-derived slug would
  re-identify every entitlement and grant whenever a server, role, or channel
  were renamed.

## Provisioning

| Resource | Grant | Revoke |
|-|-|-|
| `guild` | Creates a single-use invite and DMs it to the user | Removes the member from the server |
| `role` | Assigns the role to the member | Removes the role from the member |
| `channel` | Adds the permission to the principal's channel overwrite | Removes it, deleting the overwrite if it is left empty |

Granting server access is an **invitation**, not a join. Discord has no API that
adds a user to a server on a bot's authority — `PUT /guilds/{guild}/members/{user}`
requires an OAuth2 token that the user themselves granted with the `guilds.join`
scope. So the grant mints an invite and delivers it by DM, and the membership
appears on a later sync once the user accepts. If the user's privacy settings
block DMs from the bot, the grant fails rather than claiming success, and the
undeliverable invite is revoked so it is not left redeemable. The invite code is
deliberately kept out of the error, since errors reach logs where it would
outlive its expiry as a usable credential.

Channel permission grants are read-modify-write: Discord replaces a permission
overwrite wholesale rather than patching it, so the connector reads the current
overwrite, sets the one bit (clearing it from `deny`, which would otherwise
outrank the allow), and writes the result back.

Revokes are idempotent. A member who has already left, a role already removed,
or a permission that was never explicitly allowed all report success with a
`GrantAlreadyRevoked` annotation rather than failing.

## API usage

Discord has no "members with role X" endpoint, so role grants page through the
server's members and filter locally. On a large server with many roles this is
the connector's dominant cost. It is paginated rather than cached so memory
stays bounded by one page regardless of server size; disgo's rate limiter
handles the resulting request volume.

## Known limitations

**Concurrent channel permission changes.** Discord replaces a permission
overwrite wholesale and offers no compare-and-set on that endpoint, so granting
or revoking a channel permission is a read-modify-write that is not atomic. Two
provisioning operations against the same principal on the same channel that
overlap can drop one of the changes, because each writes the full mask it
computed from its own read. The next sync reports the true state.

**Accounts in more than one synced server.** A Discord account is global, but
resources carry a single parent, so an account in several synced servers is
emitted once per server with a different parent each time. The resource ID is
the account snowflake and is stable, and grants resolve by that ID, so access
data is correct — but the parent recorded for such an account depends on sync
order and should not be treated as "the" server for that account. Server
membership is authoritatively the `access` grant on each server.

**Rate limiting is handled inside disgo** rather than surfaced to the Baton
SDK, so a sync cannot pace itself against Discord's rate-limit headers or
checkpoint on a 429. disgo blocks and retries, which is correct but opaque to
the syncer.

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
  config             Get the connector config schema
  health-check       Check the health of a running connector
  help               Help about any command

Flags:
      --auth-method string                               ($BATON_AUTH_METHOD)
      --client-id string                                 The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string                             The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
      --external-resource-c1z string                     The path to the c1z file to sync external baton resources with ($BATON_EXTERNAL_RESOURCE_C1Z)
      --external-resource-entitlement-id-filter string   The entitlement that external users, groups must have access to sync external baton resources ($BATON_EXTERNAL_RESOURCE_ENTITLEMENT_ID_FILTER)
      --external-resource-traits strings                 Resource type traits (e.g. "user", "group", "app") to sync and match from the external resource c1z. When unset the matcher falls back to user and group; passing this flag replaces the full set rather than adding to it. ($BATON_EXTERNAL_RESOURCE_TRAITS)
  -f, --file string                                      The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
      --health-check                                     Enable the HTTP health check endpoint ($BATON_HEALTH_CHECK)
      --health-check-port int                            Port for the HTTP health check endpoint ($BATON_HEALTH_CHECK_PORT) (default 8081)
  -h, --help                                             help for baton-discord
      --http-timeout-seconds int                         HTTP client timeout in seconds (max 1800) ($BATON_HTTP_TIMEOUT_SECONDS) (default 300)
      --keep-previous-sync-c1z                           Keep the previously synced c1z on disk to enable ETag replay across service-mode syncs (requires a connector that supports ETag replay; costs one c1z of local disk) ($BATON_KEEP_PREVIOUS_SYNC_C1Z)
      --log-format string                                The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string                                 The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
      --log-level-debug-expires-at string                The timestamp indicating when debug-level logging should expire ($BATON_LOG_LEVEL_DEBUG_EXPIRES_AT)
      --log-path strings                                 The file path to write logs to ($BATON_LOG_PATH)
      --otel-collector-endpoint string                   The endpoint of the OpenTelemetry collector to send observability data to (used for both tracing and logging if specific endpoints are not provided) ($BATON_OTEL_COLLECTOR_ENDPOINT)
      --parallel-sync                                    Deprecated: use --workers instead. ($BATON_PARALLEL_SYNC)
  -p, --provisioning                                     This must be set in order for provisioning actions to be enabled ($BATON_PROVISIONING)
      --skip-entitlements-and-grants                     This must be set to skip syncing of entitlements and grants ($BATON_SKIP_ENTITLEMENTS_AND_GRANTS)
      --skip-full-sync                                   This must be set to skip a full sync ($BATON_SKIP_FULL_SYNC)
      --storage-engine string                            The storage engine to use when opening the sync c1z file: sqlite or pebble. Leave unset to use the baton-sdk default. ($BATON_STORAGE_ENGINE)
      --sync-resource-types strings                      The resource type IDs to sync ($BATON_SYNC_RESOURCE_TYPES)
      --sync-resources strings                           The resource IDs to sync ($BATON_SYNC_RESOURCES)
      --task-concurrency int                             The number of Baton tasks to run concurrently in service mode. Tasks may include sync, grant, revoke, and more. Minimum value is 1, maximum value is 100. ($BATON_TASK_CONCURRENCY) (default 3)
      --ticketing                                        This must be set to enable ticketing support ($BATON_TICKETING)
      --token string                                     required: Bot token used to authenticate to Discord. ($BATON_TOKEN)
  -v, --version                                          version for baton-discord
      --workers int                                      The number of sync workers to use. -1 for auto-detect, 0 for sequential, >0 for parallel ($BATON_WORKERS)

Use "baton-discord [command] --help" for more information about a command.
```
