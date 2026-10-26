# Architecture proposal

This is a design, not a description of deployed functionality.

## Components

1. Entry adapters: console interfaces and, later, community integrations start a support session.
2. Session service: tracks player consent, participating agents, evidence and investigation state.
3. Meta model adapter: powers tool selection, diagnosis and repair proposals through Meta Model API. Confirm current API contracts and select a model before implementation. Provider credentials stay on the backend.
4. Diagnostic tools: execute bounded, typed checks against approved OpenPak endpoints. Return structured evidence rather than asking the model to infer success from prose.
5. Local adapters: the player's agent or console client performs supported local checks and explicitly authorized repairs.
6. Verification: repeat relevant checks and report recovered, unresolved or insufficient evidence.
7. Maintainer workflow: aggregate consented, redacted incidents and prepare issues or patch proposals for review.

## Investigation lifecycle

Requested -> consent established -> collecting evidence -> diagnosis -> proposed repair -> authorized execution -> verification -> resolved or escalated.

Each diagnostic result needs a check identifier, timestamp, source, outcome and redacted evidence. Repair proposals need the affected setting, expected effect, permission requirement, rollback plan and verification criteria. These are conceptual fields; no wire protocol is established yet.

## Agent collaboration

Start with two agents: Pakka and the player's agent. Exchange capabilities, diagnostic requests, results, repair proposals and verification results. Authenticate sessions and bind permissions to the relevant participant; do not trust a display name as identity.

Nexus Chat is a candidate meeting place. Validate programmatic creation and participation, visibility, participant control, retention and terms before adoption. Unlisted rooms must not be described as private. Do not claim standards-based A2A interoperability until the relevant protocol is implemented and tested.

## Boundaries

- Treat player messages, logs and peer-agent content as untrusted data.
- Only registered tools may run; never execute arbitrary model-generated shell commands.
- Limit steps, time, output size and model cost per session.
- Redact credentials and personal data before external model or chat requests.
- Obtain consent before collecting diagnostics and explicit scoped authorization for mutations.
- Keep local secrets local. Restrict remote checks to approved destinations.
- Back up supported configuration changes and provide rollback.
- Maintainers review code proposals and production changes. No autonomous deployment in the MVP.
- Define retention and deletion before storing real player incidents.
