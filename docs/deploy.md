# Deploy: pakka

## Build and test

Go 1.26+, standard library only. Builds standalone; the workspace `go.work` is ignored.

```sh
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
gofmt -l .
```

## Image

`Dockerfile`: static build, alpine runtime, runs as uid 65532 with `/app/data`
writable. Every listener is >= 20000, so no capability is needed (rootless
podman). Release images are built by CI on `v*.*.*` tags.

## Configuration

Environment only; `.env` (git-ignored) is read if present and plain environment
wins. See `.env.example` for the full list. Core variables:

| Variable | Purpose |
|---|---|
| `PAKKA_ADDR` | listen address, default `127.0.0.1:24800` |
| `PAKKA_DNS_HOST`, `PAKKA_TCP_ADDR`, `PAKKA_HEALTH_URL` | live-check targets |
| `PAKKA_CHECK_TIMEOUT`, `PAKKA_MAX_RUNS` | check bounds |
| `PAKKA_META_API_KEY` (or `META_API_KEY`), `PAKKA_META_API_BASE`, `PAKKA_META_MODEL` | Meta Model API (default model `muse-spark-1.3-contributor`) |
| `PAKKA_INVESTIGATE_STEPS` | investigation step limit |
| `PAKKA_DISCORD_TOKEN` | enables the bot; channel/guild/role IDs per `.env.example` |

Never commit credentials, player logs or account data.

## Multi-host

Extra hosts run the same image with `PAKKA_DISCORD_GATEWAY=false` (REST only, no
second gateway session) and a local journal shipper posting to their own
localhost `/v1/log-events`. Each instance announces with its own hostname. Run
the status monitor, welcomer and eShop watcher on one host only to avoid
duplicates. The eShop sync command must run on a host with the hbstore-tools
checkout, toolchain and keys.
