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

## Community

- [OpenPak](https://openpak.org)
- [Discord](https://discord.gg/U8S8eApwq)

## License

AGPL-3.0-only. See [LICENSE](LICENSE).
