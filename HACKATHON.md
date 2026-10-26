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

## Publication

Keep the repository public. Only publish code, assets and data we have rights to distribute. Use synthetic or explicitly approved redacted fixtures; never publish private research or credentials.
