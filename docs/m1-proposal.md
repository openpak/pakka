# M1 proposal: evidence (agreed 2026-10-26)

Planning document for Milestone 1 of [Implementation plan](plan.md).
No implementation exists yet; implementation starts at the recorded event
boundary (see [HACKATHON.md](../HACKATHON.md)).

## Decisions

- Platform: Nintendo Switch, developed against Harbor with validation on real
  Switch hardware.
- Scenarios: (A) wrong-environment local misconfiguration, (B) staged service
  outage with correct local config.
- First player interface (M3 direction): website / mobile app rather than a
  console app, to unblock testing sooner.

## Scenario A: local configuration failure

Console/emulator pointed at the wrong environment — e.g. stock Nintendo hosts
instead of OpenPak (missed or reverted Switch Setup patch), or stale endpoint
configuration. Deterministic, common, reversible.

Harness checks (all read-only, allowlisted): DNS resolution of expected
OpenPak hosts, TCP connectivity, TLS handshake/certificate validation, HTTP
service health endpoints.

## Scenario B: service outage with correct local config

Local checks pass, but a core service (e.g. nx-baas health) fails — reproduced
against a stopped staging service or a simulated 5xx. Pakka must report the
outage and propose no local changes. This exercises the
no-unnecessary-changes requirement that Milestone 2 builds on.

## Diagnostic surfaces

Harness-side only for M1: DNS, TCP, TLS/certificates, HTTP health endpoints.
Console-exposed capabilities are still unknown; local checks arrive via the
player agent / console client in M3–M4. Never assume every console exposes the
same capabilities.

## Evidence schema v0

One result per check, plus a run envelope:

```json
{"schema": "pakka.evidence/v0", "run_id": "…", "scenario": "local-misconfig",
 "results": [{"check_id": "dns.resolve", "ts": "…", "source": "harness",
 "outcome": "pass", "summary": "…", "evidence": {}, "duration_ms": 12}]}
```

Outcomes: `pass | fail | error | skipped`. Evidence is redacted at collection,
before any external model or chat request (see [Architecture](architecture.md)).

## Fixtures

- Synthetic JSON fixtures checked into the repo: one per scenario plus an
  all-pass control.
- The runner supports fixture mode (deterministic, no network) and live mode.
- Never commit credentials, player logs, account data, or private research.

## Done when

Both scenarios produce deterministic structured results: same input, same
outcome set, in fixture mode and at least once live. (Source: plan.md.)

## Out of scope for M1

Model integration (M2), repair flows (M3), agent collaboration (M4),
maintainer reports (M5), and any player-facing interface beyond a
fixture/CLI runner.
