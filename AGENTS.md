# Pakka agent instructions

- This is an independent repository, not a monorepo component.
- Read README.md, HACKATHON.md and docs/plan.md before implementation.
- Distinguish plans, preparation and event-built working features accurately.
- Use English and AGPL-3.0-only for new code unless explicitly directed otherwise.
- Keep changes focused. Verify meaningful behavior when runtime code is added.
- Never publish credentials, raw player diagnostics, personal data or private research.
- Do not give the model arbitrary command execution or deploy its proposals autonomously.
- Do not send messages to players or publish issues/patches without authorization. The one standing authorization: on 2026-11-01 the owner made Pakka the publisher and the official OpenVerse account. That is limited to the scoped, audited permissions in docs/publisher.md (templated posts auto-publish; free text waits for a staff member; kill switch and per-category toggles). Do not widen it.
- Use supported OpenPak emulator forks; Harbor is the current Switch target.
- Do not write to /tmp. In the OpenPak workspace, use workspace scratch/tmp for throwaway work; outside it, use a local ignored scratch directory.
- Runtime: Go harness (`main.go`, `internal/`). Build and test with `GOWORK=off` (the repo builds standalone; workspace `go.work` is ignored). See README. No deployment is implemented yet.
