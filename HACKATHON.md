# Hackathon contribution record

## Existing foundation

OpenPak's accounts, social network, services, game servers, clients and emulator work predate Pakka's hackathon implementation. They are context and dependencies, not new hackathon contributions.

## Repository preparation

The initial repository commit contains planning, licensing and contributor documentation only. It does not implement an assistant. Preparation SHA: `441603f23f79f60b99ee01733934480b231dd8c1`, tagged `preparation-baseline`.

## Event boundary

- Event start: 2026-10-26 16:00 UTC (owner-confirmed).
- Event end: not yet recorded.
- Implementation-start baseline: `441603f` — HEAD at the official start. It coincides with the `preparation-baseline` tag, so no intervening work falls between preparation and event start.

Commits `237617b`–`e0924da` (2026-10-26 16:59–18:06 UTC) are event-time planning docs, not pre-event work. Do not backdate commits or describe pre-event work as event-built.

## Event-built work

- 2026-10-26: initial Go harness — HTTP service (`/healthz`, `/readyz`,
  `POST /v1/runs`), bounded read-only checks (DNS/TCP/HTTP), fixture runner
  with embedded synthetic scenarios, env-only config, CI. Commit
  `f30ff3833eb14d913ef23ea1af65963e4e460350`. Validation: `gofmt`, `go vet`,
  `go test ./...` green; live smoke test of `/healthz` and a fixture
  `/v1/runs` call returned schema-valid evidence. Fixtures are synthetic;
  no model integration, repair flow, or deployment yet.
- 2026-10-26: live-mode validation — two `/v1/runs` (mode live) calls against
  DNS `openpak.org` (pass, 4 addrs), TCP `127.0.0.1:1` (fail, refused), unset
  health URL (skipped); normalized outcomes byte-identical across runs.
  No code change.
- 2026-10-26: M2 Meta investigation — `api.meta.ai` chat-completions client
  (default model `muse-spark-1.3-contributor`), allowlisted tool registry
  (dns/tcp/http, no model-supplied targets), bounded loop with validated
  evidence-backed findings and insufficient-evidence downgrade, fixture
  scripts, `POST /v1/investigations`, `.env` support. Commit
  `26102cde8f622ba7602f7632ae783d3739437ae8`. Validation: gofmt/vet/test
  green; live smoke of fixture investigation and no-key 503. Live model
  call not yet tested (awaiting API key).
- 2026-10-26: container packaging — fixed Dockerfile for the stdlib-only
  build, added .dockerignore (keeps .env and .git out of the image), mirrored
  the tag-release workflow (multi-arch ghcr publish). Built and pushed
  `ghcr.io/openpak/pakka:bbda721` and `:latest` (amd64, local podman build).
  Validation: container smoke test (/healthz ok, fixture investigation ok),
  runs as uid 65532, /app holds only the binary. Commit
  `bbda721c88793e4c9fe4d4a432dab51feee35513`. Release workflow validated
  with a dry-run dispatch (amd64+arm64 builds success, publish skipped).
- 2026-10-26: release v0.1.0 and first deploy — tag `v0.1.0`, release workflow
  published multi-arch `ghcr.io/openpak/pakka:0.1.0`, `:0.1`, `:latest`.
  Deployed on draughts-server as quadlet `openpak-pakka` (localhost-only
  24800, auto-update on :latest). Validation: service active, /healthz ok,
  fixture investigation ok. Live model calls still await the Meta API key.
- 2026-10-26: first live model investigation — restarted with the Meta API
  key; `POST /v1/investigations` (mode live) returned a validated finding
  (insufficient-evidence, correctly, with TCP/health targets unconfigured;
  evidence cites only the executed dns.resolve check). Proves the client,
  loop, and validation against real api.meta.ai. Full local-vs-outage
  distinction still needs real targets configured.
- 2026-10-26: Discord adapter — bot serves `!pakka run` and
  `!pakka investigate` from the shared in-process core (discordgo,
  guild allowlist, 2000-char replies, Message Content intent).
  Validation: unit tests (fake sender, fixture core) green with vet/gofmt;
  live gateway check pending deploy plus bot token.
- 2026-10-26: Discord automation batch — per-command channel gating
  (investigate ops-only), model-based support routing across setup-help /
  connection-help / bug-reports (model first, keywords on API failure,
  hourly per-user cooldown), outage monitor with status-channel alerts,
  15-minute newcomer welcomer. Commit
  `627a135e174273b6144e56cdbb03d7b5a80da71f`. Validation: unit tests
  green with vet/gofmt. Live check pending channel IDs + deploy.
- 2026-10-26: release v0.3.0, Discord automation live — tag `v0.3.0`,
  multi-arch publish, server auto-updated. Verified: service active,
  /healthz ok, discord connected, status monitor + welcomer started
  (status + welcome channels wired). Support routing and ops gating await
  their channel IDs.
- 2026-10-26: journal log watching — host shippers post journal JSON to
  `POST /v1/log-events`; Pakka filters by unit glob and announces
  warning+ to the ops-alerts channel and moderation signals to the
  moderation-log channel (30-minute cooldown per message, host-tagged).
  Commit `36838350377dc56eb1ff45953769ad8c6c2a40d0`. Validation: unit tests
  green with vet/gofmt. Deploy + shippers pending release.
- 2026-10-26: release v0.4.0, log watching live on draughts — tag `v0.4.0`,
  multi-arch publish, server auto-updated with a ZeroTier-bound ingest port.
  Verified: service active, /healthz ok, log watcher started, synthetic ops
  + moderation test events accepted (202). Journal shipper installed on
  draughts; Raspi excluded (not infra) — quilted-waffle is the second
  monitored server.
- 2026-10-26: REST-only alerts mode for second-site instances — quilted-waffle
  is the main host; its ZeroTier path to draughts flapped earlier so second
  hosts run the same image with `PAKKA_DISCORD_GATEWAY=false`, posting alerts
  via Discord REST with no cross-host dependency. Commit
  `5ab8775f6977581760f0373cccd5628460ab87eb`. Validation: unit tests green
  with vet/gofmt. Quilted-waffle deploy pending release.
- 2026-10-26: release v0.5.0, quilted-waffle live — ZeroTier draughts↔quilted
  verified up both ways (both nodes online, TCP OK), but the REST-only design
  stands (no cross-host dependency). Tag `v0.5.0`, multi-arch publish;
  quilted-waffle runs `openpak-pakka` (localhost-only 24800, gateway off,
  monitor/welcomer disabled) plus a journal shipper. Fixed the ops-alerts
  channel ID (`1564357306504974336`; the `...349...` variant 404s) on both
  hosts after a live 404 proved the old value wrong. Verified end to end via
  the Discord API: quilted synthetic events landed in #ops-alerts and
  #moderation-log.
- 2026-10-26: #hackathon dev channel + chat privacy (v0.5.1) — commands and
  investigate gated to #hackathon (`1564373127599759400`); command gating
  covers prefixed commands only so support routing keeps working in its own
  channels; `@pakka` mentions answer (commands run as-is, free text becomes
  a live investigation). All Discord-bound output is redacted: IPs, tokens,
  secrets, local paths, and configured internal endpoints never reach chat
  on any channel; service/host names stay as operational context. Verified:
  unit tests green, live redaction check scrubbed IP/token/path in #ops-alerts.
- 2026-10-26: moderator-only mentions (v0.5.2) —
  `PAKKA_DISCORD_MOD_ROLES` gates `@pakka` to members holding a listed role
  (Moderator `1563497150220865556`); prefixed commands unaffected.
- 2026-10-26: mention + routing fixes (v0.5.3) — discordgo never sets
  State.User, so self is now resolved over REST at startup (mentions were
  silently dead); guild owner bypasses the mod gate (owners often hold no
  roles; observed live); unknown `!pakka <text>` in support channels routes
  like plain chat instead of going silent (observed live in #setup-help).

## Publication

Keep the repository public. Only publish code, assets and data we have rights to distribute. Use synthetic or explicitly approved redacted fixtures; never publish private research or credentials.
