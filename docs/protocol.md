# Protocol: pakka

Pakka is a Go harness exposing a small HTTP API and a Discord bot over the same
in-process checks and investigations. Design background: [architecture](architecture.md).

## HTTP API

Listens on `PAKKA_ADDR` (default `127.0.0.1:24800`).

- `GET /healthz`, `GET /readyz` - liveness and readiness.
- `POST /v1/runs` - run diagnostic checks, e.g. `{"mode":"fixture","scenario":"local-misconfig"}`.
  `mode` is `fixture` (synthetic, no key needed) or `live`.
- `POST /v1/investigations` - model-driven diagnosis, e.g.
  `{"mode":"fixture","scenario":"service-outage","symptoms":"cannot connect"}`.
  Live mode needs a Meta API key; steps are bounded by `PAKKA_INVESTIGATE_STEPS`.
- `POST /v1/log-events` - journal shipper ingest (`journalctl -o json` lines).
  Reachable only over localhost and the private ZeroTier network; never expose it publicly.

Live-check targets come only from the environment, never from requests. Fixture
scenarios: `all-pass`, `local-misconfig`, `service-outage`.

## Discord

Prefix `!pakka` (`PAKKA_DISCORD_PREFIX`):

- `help` - usage
- `run [scenario] [fixture|live]` - run checks
- `investigate [fixture [scenario]|live] <symptoms>` - diagnose a problem
- `eshop` - eShop update watch status

`@pakka <question>` talks to the moderator agent (live diagnostics, service
status, recent log alerts). All chat-bound output is redacted (IPs, tokens,
secrets, paths). Discord identity is never treated as an OpenPak account.

Gateway intents required: Message Content, Server Members (moderator roles,
welcomer). Without Message Content the gateway closes with 4014.

## Background jobs

- Status monitor: polls `PAKKA_STATUS_TARGETS` (`Area/service=https://...` or
  `=tcp://host:port`) every `PAKKA_STATUS_INTERVAL` (60s); posts up/down
  transitions only. The first poll baselines, so restarts never flood.
- Welcomer: tags new members every `PAKKA_DISCORD_WELCOME_INTERVAL` (15m).
- Support routing: model classifies messages in the help/bug (optional
  connection) channels and redirects once per user per hour; unsure stays silent.
- Log watching: filters shipped units by `PAKKA_LOG_WATCH_UNITS` (glob list),
  warning+ to the ops alerts channel, moderation signals to the modlog channel,
  30-minute cooldown per distinct message.
- eShop watch: polls the store catalog (`PAKKA_ESHOP_API_BASE`) and upstream
  GitHub releases every `PAKKA_ESHOP_INTERVAL` (1h); alerts as
  `[Switch - eshop] ...`; announcements to the announce channel (several
  updates in one poll share a digest); a title still not landed six hours after
  release gets one staff "failed to update" alert. `PAKKA_ESHOP_SYNC_COMMAND`
  triggers the build/publish pipeline, single-flight with
  `PAKKA_ESHOP_SYNC_TIMEOUT` (2h) and `PAKKA_ESHOP_SYNC_COOLDOWN` (6h).
