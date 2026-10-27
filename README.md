# Pakka

OpenPak's planned AI assistant for players and network maintainers, powered by Meta models.

[OpenPak](https://github.com/openpak) is an open-source, multi-console network where players can keep their identity, friends and community across devices. Multiplayer compatibility depends on each game.

**Status: project preparation and design. No running agent, model integration, console client or deployment is implemented yet.**

## What we are building

Pakka will investigate connection problems, collect diagnostic evidence, suggest repairs and verify the outcome. Console homebrew apps and dashboards are intended entry points, starting with selected platforms.

Players will also be able to invite their own AI agent into a shared troubleshooting session. Pakka checks OpenPak services while the player's agent investigates the local setup and carries out permitted repairs. Nexus Chat is a candidate transport; its room creation, access controls and integration terms still need validation. A chat invitation does not authorize changes to a player's device.

For maintainers, Pakka will combine network health signals, player feedback and consented diagnostic results to identify recurring problems, prepare bug reports and propose fixes for human review.

## Initial foundation: server-side Go harness

Pakka starts as a Go agent harness deployed on an OpenPak server. It will connect Meta model reasoning to a registered tool set for OpenPak services, with Discord, website and app adapters sharing the same session and authorization layer. Model credentials stay on the server.

The first vertical slice is a request from one entry point, an authorized read-only service check, and an evidence-backed response. Implement the website/app API and Discord entry points around the same core rather than separate agents. Console clients and personal-agent collaboration follow this foundation.

## First milestone

One supported platform, one reproducible connection failure, one reversible repair and a verified retest. Include a service outage scenario where Pakka correctly avoids unnecessary local changes. Broader console support follows working evidence rather than a blanket compatibility claim.

## Project documents

- [Architecture](docs/architecture.md)
- [Implementation plan](docs/plan.md)
- [M1 evidence proposal](docs/m1-proposal.md)
- [Hackathon contribution record](HACKATHON.md)
- [Contributing](CONTRIBUTING.md)
- [Agent instructions](AGENTS.md)

## Development

Go 1.26+, standard library only (no external dependencies). The repo builds
standalone; the workspace `go.work` is ignored:

```sh
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
gofmt -l .
```

Run the harness (listens on `127.0.0.1:24800` by default):

```sh
GOWORK=off go run .
curl localhost:24800/healthz
curl -X POST localhost:24800/v1/runs -d '{"mode":"fixture","scenario":"local-misconfig"}'
curl -X POST localhost:24800/v1/investigations -d '{"mode":"fixture","scenario":"service-outage","symptoms":"cannot connect"}'
```

Configuration is environment-only (see `.env.example`; copy to `.env`,
which is git-ignored, for local secrets — plain environment wins):
`PAKKA_ADDR`, `PAKKA_DNS_HOST`, `PAKKA_TCP_ADDR`, `PAKKA_HEALTH_URL`,
`PAKKA_CHECK_TIMEOUT`, `PAKKA_MAX_RUNS`, `PAKKA_META_API_KEY` (or
`META_API_KEY`), `PAKKA_META_API_BASE`, `PAKKA_META_MODEL` (default
`muse-spark-1.3-contributor`), `PAKKA_INVESTIGATE_STEPS`.
Live-check targets come only from the environment, never from requests.
Live investigations need a Meta API key; fixture mode works without one.

Never commit credentials, player logs, account data or private research. Use synthetic diagnostic fixtures in public examples.

## Discord

The harness doubles as a Discord bot when `PAKKA_DISCORD_TOKEN` is set. The
bot runs the same in-process checks and investigations as the HTTP API.

Setup: create an application at
[discord.com/developers/applications](https://discord.com/developers/applications),
add a Bot, copy its token, and enable the **Message Content Intent** (without
it the bot cannot read commands). Invite it via OAuth2 URL Generator with the
`bot` scope and Send Messages + Read Messages/View Channels permissions.

Commands (prefix `!pakka`, configurable via `PAKKA_DISCORD_PREFIX`):

- `!pakka help` — usage
- `!pakka run [scenario] [fixture|live]` — run checks (default: all-pass fixture)
- `!pakka investigate [fixture [scenario]|live] <symptoms>` — diagnose a problem
- `!pakka eshop` — eShop update watch status (tracked/behind titles, last sync)

Mentions: `@pakka <question>` talks to the moderator agent — the model
answers in chat using live diagnostics, service status, and recent log
alerts, and can post a notice to a staff channel when asked. Mentions can
be limited to moderator roles with `PAKKA_DISCORD_MOD_ROLES` (the guild
owner always passes); both user and role mentions trigger. All chat-bound
output is redacted (IPs, tokens, secrets, paths never reach Discord).

Set `PAKKA_DISCORD_GUILDS` to a comma-separated guild allowlist to restrict
where the bot answers (empty = everywhere it can read). Live investigations
need `PAKKA_META_API_KEY` too. Discord identity is never treated as an
OpenPak account.

Channel gating: `PAKKA_DISCORD_CHANNELS` limits where commands work (empty =
everywhere). `investigate` can be further restricted with
`PAKKA_DISCORD_OPS_CHANNELS` (empty = unrestricted).

Status monitor: with `PAKKA_DISCORD_STATUS_CHANNEL` and
`PAKKA_STATUS_TARGETS` (`name=https://...` or `name=tcp://host:port`,
comma-separated) set, the bot polls every `PAKKA_STATUS_INTERVAL` (default
60s) and posts up/down transitions to the status channel. The first poll
only baselines, so restarts never flood. With
`PAKKA_DISCORD_ALERTS_CHANNEL` set, transitions are also mirrored there as
tagged staff alerts (`[Area - service] ...` for targets named
`Area/service`, `[Status - name]` otherwise).

Welcomer: with `PAKKA_DISCORD_WELCOME_CHANNEL` set, the bot tags members who
joined since the previous pass every `PAKKA_DISCORD_WELCOME_INTERVAL`
(default 15m). Needs the Server Members intent enabled on the bot page.

Support routing: with `PAKKA_DISCORD_SETUP_CHANNEL` (help) and
`PAKKA_DISCORD_BUG_CHANNEL` set, the bot classifies messages in those
channels with the model (keywords cover API failures) and redirects
out-of-scope questions to the right one (one redirect per user per hour).
`PAKKA_DISCORD_CONNECTION_CHANNEL` is optional: set it for three-channel
routing, empty merges connection questions into help. Unsure cases stay
silent rather than misrouting.

Log watching: each monitored host runs a journal shipper (a user service
piping `journalctl --user --follow -o json` into `POST /v1/log-events`).
Pakka filters by `PAKKA_LOG_WATCH_UNITS` (glob list, default `openpak-*`),
posts priority warning+ to `PAKKA_DISCORD_OPS_ALERTS_CHANNEL` and
moderation signals to `PAKKA_DISCORD_MODLOG_CHANNEL`, with a 30-minute
cooldown per distinct message. Point both at one #logs channel to merge
the streams; actionable `[Area - service]` alerts go to
`PAKKA_DISCORD_ALERTS_CHANNEL` instead. The ingest endpoint is reachable
only over localhost and the private ZeroTier network — never expose it
publicly.

Multi-host alerts: additional hosts run the same image with
`PAKKA_DISCORD_GATEWAY=false` (REST-only, no second gateway session) plus a
local shipper posting to their own localhost ingest. Each instance announces
with its own hostname; run the status monitor, welcomer, and eShop watcher
on one host only to avoid duplicates.

eShop watch: with `PAKKA_ESHOP_API_BASE` (the store admin API root),
`PAKKA_DISCORD_ALERTS_CHANNEL` (staff-only) and
`PAKKA_DISCORD_ANNOUNCE_CHANNEL` (#announcements) set, Pakka polls the
eShop catalog every `PAKKA_ESHOP_INTERVAL` (default 1h) plus each tracked
title's upstream GitHub releases. Titles whose catalog source is a GitHub
release URL track that repo directly; `PAKKA_ESHOP_SOURCES` (sources.json
format, inline or `@path`) covers the rest. `PAKKA_GITHUB_TOKEN` lifts the
releases rate limit. A new upstream release posts `[Switch - eshop] ...`
to alerts and, with `PAKKA_ESHOP_SYNC_COMMAND` set (e.g. hbstore-tools
`./run-hb.sh sync` on a host with the toolchain and keys), triggers the
pipeline that builds the new NSP and publishes it — single-flight with
`PAKKA_ESHOP_SYNC_TIMEOUT` and `PAKKA_ESHOP_SYNC_COOLDOWN`. When new
versions land in the catalog, Pakka posts to #announcements: one update
keeps its own notice, several landing in the same poll share a single
digest listing. A drifted title that still hasn't landed six hours after
its release was announced gets one `[Switch - eshop] … failed to update`
staff alert instead of public silence. The first poll only baselines, so
restarts never flood.

## Community

- [OpenPak](https://openpak.org)
- [Discord](https://discord.gg/U8S8eApwq)

## License

AGPL-3.0-only. See [LICENSE](LICENSE).
