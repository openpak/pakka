# Player chat API

Enabled by `PAKKA_CHAT_KEY` (shared secret with the OpenPak website). Only the
website calls these routes, over the private network; browsers and the app reach
them through the website's `/pakka` and `/api/v1/pakka` routes, which authenticate
the player first. Every request carries:

- `Authorization: Bearer <PAKKA_CHAT_KEY>`
- `X-Account-Id: <account id the website resolved>`
- `X-Pakka-Grant: <opaque, short-lived grant the website minted for this player>`

The grant is how Pakka asks the website for things about this player: tools call
`GET <website>/internal/pakka/tool/{status|consoles|friends|rewards|eligibility}`
with the grant, and the website resolves it to the account. No tool takes an
account argument, and the model never sees a grant.

| Route | Purpose |
| --- | --- |
| `GET /v1/chat/state` | availability, eligibility, consent, limits |
| `POST /v1/chat/consent`, `DELETE` | accept or withdraw the data-use notice |
| `GET/POST /v1/chat/conversations` | list mine, start one |
| `GET/DELETE /v1/chat/conversations/{id}` | read, delete |
| `DELETE /v1/chat/account` | account erasure: every conversation, the consent record and the rate state of the account (idempotent; the website's erasure step) |
| `POST /v1/chat/conversations/{id}/messages` | `{"text"}`; answers `text/event-stream`: `start`, `tool`, `delta`, `done` or `error` |

Gates, in order: service key, eligibility (adult, not supervised; fail closed),
consent, message size, per-conversation single flight, burst (`PAKKA_CHAT_BURST`,
6/min), daily cap (`PAKKA_CHAT_DAILY_CAP`, 100), concurrency. Diagnoses are limited
to `PAKKA_CHAT_DIAGNOSES_PER_HOUR` (3). Conversations live under
Postgres (`pakka_chat_conversations`) for `PAKKA_CHAT_RETENTION` (30 days); at most
`PAKKA_CHAT_MAX_CONVERSATIONS` (30) per account. Counters are in memory.

Model: `PAKKA_CHAT_META_API_KEY` (optional) gives chat its own Meta credentials, and `PAKKA_CHAT_META_MODEL` its own model id; otherwise chat uses the process model.

Tools: `docs_search` (docs.openpak.org search index), `service_status`,
`my_consoles`, `my_friends`, `my_rewards`, `diagnose_connection` (existing
investigator, read-only). There are no write tools. See `data-use.md` for what is
sent to the model.
