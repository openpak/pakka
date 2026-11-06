# Pakka

OpenPak's planned AI assistant for players and network maintainers, powered by Meta models.

[OpenPak](https://github.com/openpak) is an open-source, multi-console network where players can keep their identity, friends and community across devices. Multiplayer compatibility depends on each game.

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
60s) and posts up/down transitions to the status channel. DOWN pages only
after `PAKKA_STATUS_DOWN_AFTER` consecutive failures (default 3), and the
posts carry triage inline: failing duration and bad-poll count going down,
outage length coming back up. The first poll only baselines, so restarts
never flood. With `PAKKA_DISCORD_ALERTS_CHANNEL` set, transitions are also
mirrored there as tagged staff alerts (`[Area - service] ...` for targets
named `Area/service`, `[Status - name]` otherwise). `!pakka ack <service>
[duration]` silences a flapping target (default 1h, recovery clears
early); bare `!pakka ack` lists. `PAKKA_STATUS_DEPENDS`
(`child=parent,...`) suppresses cascade pages: a child failing while its
parent is posted-down stays silent, and pages if still failing once the
parent recovers.

Welcomer: with `PAKKA_DISCORD_WELCOME_CHANNEL` set, the bot greets members
who joined since the previous pass every `PAKKA_DISCORD_WELCOME_INTERVAL`
(default 15m). Needs the Server Members intent enabled on the bot page.
Each newcomer gets the patcher links and support pointers by DM; members
with DMs closed fall back to a channel tag.

Support routing: with `PAKKA_DISCORD_SETUP_CHANNEL` (help) and
`PAKKA_DISCORD_BUG_CHANNEL` set, the bot classifies messages in those
channels with the model (keywords cover API failures) and redirects
out-of-scope questions to the right one (one redirect per user per hour).
`PAKKA_DISCORD_CONNECTION_CHANNEL` is optional: set it for three-channel
routing, empty merges connection questions into help. Unsure cases stay
silent rather than misrouting. In-scope questions the router leaves alone
get a curated FAQ answer when one matches clearly (patcher install,
homebrew updates, connection error codes, finding players, supported
consoles); every answer invites a human follow-up, and misses stay silent
for the humans.

Matchmaking sweep: with `PAKKA_DISCORD_MATCHMAKING_CHANNEL` set, the bot
deletes posts older than `PAKKA_MATCHMAKING_MAX_AGE` (default 24h) every
hour, so the single LFG channel stays fresh. Pins are never touched.

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

Account gate: free-text answers on Discord need a linked, adult, notice-accepting
OpenPak account (`PAKKA_DISCORD_GATE_KEY` = the website internal key,
`PAKKA_DISCORD_GATE_URL` default `http://openpak-website:20010`); unset key
refuses everyone, children are never answered.

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
digest listing. Brand-new titles get the same treatment: one arrival keeps
its own `:new:` notice, several share a single listing. A drifted title
that still hasn't landed six hours after its release was announced gets
one `[Switch - eshop] … failed to update` staff alert instead of public
silence. Every `PAKKA_ESHOP_DIGEST_INTERVAL` (default 168h) Pakka posts
one staff digest of titles stuck behind upstream. The first poll only
baselines, so restarts never flood. After each sync, run-hb.sh posts the
broken-package set to `POST /v1/eshop-broken`; packages still broken after
`PAKKA_ESHOP_BROKEN_AFTER` (default 168h) get one staff alert, batched.
Broken first-seen persists in Postgres (`PAKKA_DATABASE_URL`; see State below), so redeploys keep the clock.

**One shop surface for every console.** The broken tracker is per console:
`POST /v1/shop/broken` takes `{"console": "wiiu", "broken": {...}}`, and
`POST /v1/eshop-broken` stays as an alias for `console=switch` (run-hb.sh
keeps working). `PAKKA_SHOP_CONSOLES` (default `switch`, e.g. `switch,wiiu,ps3`)
names the managed consoles; `PAKKA_SHOP_CORE_URL` and `PAKKA_SHOP_CORE_KEY`
point at shop-core, where the broken state is then kept durably (table
`shop_broken`; the Switch's old state doc is migrated there once). Alerts
read `[Switch - eshop]` for the Switch and `[<Console> - shop]` for the rest.
`!pakka shop [console]` shows every shop's status (`!pakka eshop` is
unchanged); staff also get `shop sync|pause|resume <console>`,
`shop retire|unretire <console> <title> [reason]` (hides from new buyers,
owners keep their download), `shop broken [console]` and `shop runs [console]`.

Game landings: with `PAKKA_NEWS_ADMIN_BASE` (default
`http://openpak-news-experiment:20043`), `PAKKA_NEWS_ADMIN_KEY` and both
announce channels set, `POST /v1/land` (`name`, optional 16-hex `title_id`,
optional one-paragraph `blurb`) lands a game end to end: a news channel for
the game, launch articles in OpenPak Games, OpenPak News and the new
channel, publish to consoles, a #announcements post, and a staff report.
Re-landing an existing channel answers 409; partial failures name the step
and what completed, and page staff. Run from the workstation with
`clients/tools/land-game.sh "Name" [title-id] [blurb...]`.

Deploy confirmations: `POST /v1/announce` (`channel`, `text`) relays one
operator post to Discord (localhost-only, redacted, 2000 chars max).
Verify `/healthz` first, then confirm from the workstation with
`clients/tools/pakka-announce.sh <channel-id> <text...>` — e.g.
`[Switch - nx-baas] deployed v1.2.3, healthy` to #alerts.

Feature announcements: `clients/tools/announce-feature.sh "<title>" "<text>" [image-url]`
posts one feature everywhere in one command: Discord #announcements (the
`/v1/announce` relay; the image URL goes on its own line so Discord embeds
it), an OpenVerse Announcements post and an OpenPak News article (both queued
as publisher drafts and approved as the operator, so the audit log records
them; News gets the image as its banner via `news.publish` `image_url`).
The script appends the alpha notice ("OpenPak is in alpha: everything here is
experimental and may change or break.") to every post automatically. The
image must be a JPEG on our own host, e.g. a file under the website's
`web/static/announce/` (served at `https://openpak.org/static/announce/`).
The OpenVerse Announcements community takes no images, so that post is text
only. Text must fit Discord's 2000 characters and News's 1200 bytes, in
points wording, with no claim that has not been tested. `ANNOUNCE_DRY_RUN=1`
prints the payloads and sends nothing.

Release watch: with `PAKKA_RELEASE_WATCH` (comma-separated openpak/*
repos) set, Pakka nudges staff once a repo's latest release ages past
`PAKKA_RELEASE_STALE_AFTER` (default 72h) without a newer one:
`[Releases - pakka] v0.8.0 released 4d ago — confirm it's deployed.` The
first sight of a tag baselines silently, so pre-existing old releases
never nag; state persists in Postgres (`PAKKA_DATABASE_URL`).

## Community

- [OpenPak](https://openpak.org)
- [Discord](https://discord.gg/U8S8eApwq)

## Docs

- [Protocol](docs/protocol.md): HTTP API, Discord commands, background jobs.
- [Deploy](docs/deploy.md): build, image, environment, multi-host.

## License

AGPL-3.0-only. See [LICENSE](LICENSE).

## State

All durable state (eShop broken first-seen, release watch, publisher queue, mirror and audit, chat
conversations and consents) lives in Postgres, so one database backup covers it. Set
`PAKKA_DATABASE_URL`; tables are created at start with a pool of at most 3 connections. Any feature
that keeps state refuses to start without it (an instance running none of them, like the
diagnostics-only status box, needs no database). On start, JSON files found in `PAKKA_STATE_DIR` are
copied in once (existing rows are never overwritten and each file is then renamed `<name>.imported-20261104` so an erased row cannot come back at the next start). For local
development only, `PAKKA_ALLOW_FILE_STORE=1` keeps state in JSON files under `PAKKA_STATE_DIR`.
Tests that need Postgres read `PAKKA_TEST_DATABASE_URL` and run with `-p 1`.
