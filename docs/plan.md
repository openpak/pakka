# Implementation plan

## Preparation baseline

Create the public project, document scope and record existing OpenPak dependencies. Do not present this preparation as event-built functionality. Confirm event dates and eligibility boundaries before implementation.

## Foundation: Go harness and server deployment

Build the shared Go runtime first: Meta adapter, bounded agent loop, session handling, authorization and registered service tools. Expose an authenticated conversation API shared by the website and app, and add a Discord adapter using the same runtime. Choose the first entry point for an end-to-end read-only service check before expanding surfaces.

Prepare container packaging, CI, health/readiness checks, graceful shutdown, redacted logs and configuration documentation. Validate locally before a separately authorized server deployment. Confirm account linking and per-tool permissions for every entry point.

Console clients and peer-agent sessions depend on this foundation. They are subsequent integrations, not prerequisites for the first running harness.

## Milestone 1: evidence

Choose a platform based on available hardware and toolchains. Reproduce one local configuration failure and one service outage. Identify existing diagnostic surfaces without assuming every console exposes the same capabilities. Specify a minimal evidence schema and synthetic fixtures.

Done when both cases produce deterministic structured results.

## Milestone 2: Meta investigation

Implement a backend, a current Meta API integration and a small allowlisted tool registry. Add bounded investigation, evidence-backed findings and an insufficient-evidence outcome.

Done when the model chooses useful checks and distinguishes local failure from outage on the fixtures and a real integration.

## Milestone 3: recovery

Build one player interface and a reversible repair flow. Require scoped permission, preserve prior configuration and retest.

Done when a real failure becomes a verified connection and rollback works.

## Milestone 4: bring your agent

Validate Nexus integration. Implement session invitation and capability exchange with a second agent. Keep sensitive evidence outside a public or merely unlisted chat.

Done when the player's agent can join, perform an approved local check, report evidence and participate in verification.

## Milestone 5: maintainer feedback

Group related redacted incidents and produce a maintainer report with evidence and a proposed next action. Publishing issues or patches requires the relevant authorization.

## Evaluation and demo

Measure correct diagnosis, evidence support, unnecessary changes, recovery verification, latency and cost. Include an unknown failure that should remain unresolved. Report sample sizes and observed results.

Show existing OpenPak briefly as context, then demonstrate only event-built functionality. Preserve time for rehearsals, documentation and a clear contribution record. Reduce platform count before sacrificing a reliable end-to-end recovery.

## Later

Additional console clients, dashboard integrations, community entry points and narrowly scoped remediation. No platform is supported until its client and diagnostic flow are tested.
