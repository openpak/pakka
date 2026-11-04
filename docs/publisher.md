# Publisher

Pakka is OpenPak's publisher and the official OpenVerse account. The owner decided this on 2026-11-01. It is implemented as explicit, scoped, audited permissions, not a blanket bypass. Code: `internal/publisher`.

## What publishes without a person

Only templated, factual posts, and only to OpenVerse. The text comes from a fixed template and checked facts (no links, no mentions, no control characters, 80 characters per fact). No model output and no free text can take this path. The rule is in code (`autoEligible`); configuration can switch a category on or off but cannot change its mode.

| Category | Source | Template |
| --- | --- | --- |
| `news_mirror` | every published News article | the article's title and text, plus a Discuss link |
| `splatfest` | `POST /v1/publisher/events` | "{winner} won the {event} Splatfest!" |
| `tournament` | `POST /v1/publisher/events` | "{winner} won {tournament}!" |
| `course` | `POST /v1/publisher/events` | "Featured course: {course} by {creator}." |

The News mirror baselines silently on first start (the archive is not flooded into OpenVerse), then posts each new article once into the matching game community, or a general room (Announcements, Switch, Help) when the game has no community. A post that Pakka itself sent as News is never mirrored back. Badge-earned activity is not Pakka's job; that is the OpenVerse activity system.

The event endpoint is ready; the game servers do not call it yet.

## What waits for a staff member

Anything with free text, and anything that is not a post: `highlights` (the weekly "Best of OpenVerse", written by the model from the week's most liked posts, links and mentions stripped), `announcement`, `community` (new communities), `news` (News articles), `boss`. A "send as News" flag on any post also makes it a draft.

Staff approve with `!pakka publish list|show|approve|reject <id>` in Discord (moderator role or guild owner; with no moderator role set only the owner passes) or through the admin API below. Approval needs the `queue:approve` scope plus the tool's own scope, refuses while the kill switch is on or the category is off, and cannot publish twice. A failed approval stays in the queue and can be retried; every post carries an event id, so a retry never duplicates. New drafts are announced in the staff alerts channel. The website `/admin` has the queue page (System Settings, Pakka posts): approve, edit then approve, or reject with a reason, with each draft's audit history.

## Scopes

| Actor | Scopes |
| --- | --- |
| `system:*` (Pakka's own jobs) | `openverse:post`, `queue:draft` |
| `model` (anything the language model asked for) | `queue:draft` only |
| `staff:*` (Discord staff, admin key) | all of them, including `queue:approve` |

Tool scopes: `openverse:post`, `openverse:community`, `news:publish`, `boss:queue`. The model's tools (`propose_post`, `propose_community`, `propose_news`) only create drafts.

## Tools

- `openverse.post`: post as Pakka in a community, optional image from our own hosts (`PAKKA_PUBLISH_IMAGE_HOSTS`, https, JPEG, 4 MiB, no off-list redirects), optional Discuss link, optional send-as-News.
- `openverse.community`: create a community (artwork is generated; found again by name or title id, so it is repeatable).
- `news.publish`: one article per fitting channel (the game's channel by title id, plus `openpak_news`) and one publish. The article text is one paragraph. Wii U and 3DS News are not involved.
- `boss.notify`: always answers `blocked: no BOSS keys`; the draft stays failed and retryable.
- The Xbox 360 feed and OpenVerse Lite read from OpenVerse, so posts reach them without anything extra.

## Controls

- `PAKKA_PUBLISH_KILL=1` stops every publish and approval. `POST /v1/publisher/kill` engages it at runtime; only an environment change and restart releases it.
- `PAKKA_PUBLISH_CATEGORIES` lists the categories that are on (comma list). Unlisted means refused, for auto posts and drafts alike. Empty means nothing publishes.
- Every attempt, denial, queueing, approval, rejection and publish is appended to the `pakka_publisher_audit` table in Postgres before it happens. If the log cannot be written, Pakka refuses to publish.

## Configuration

`PAKKA_OPENVERSE_URL`, `PAKKA_OPENVERSE_KEY` (the OpenVerse `PN_MIIVERSE_API_OFFICIAL_KEY`), `PAKKA_NEWS_ADMIN_BASE`, `PAKKA_NEWS_ADMIN_KEY`, `PAKKA_PUBLISH_ADMIN_KEY`, `PAKKA_PUBLISH_CATEGORIES`, `PAKKA_PUBLISH_KILL`, `PAKKA_PUBLISH_MIRROR_INTERVAL` (default 10m, 0 = off), `PAKKA_PUBLISH_HIGHLIGHTS_EVERY` (default 0 = manual only), `PAKKA_VERSE_URL`, `PAKKA_DATABASE_URL`, `PAKKA_PUBLISH_META_API_KEY` / `PAKKA_PUBLISH_META_MODEL` (the highlights writer's own model credentials; without any model the weekly draft is a plain count with community names). Without the OpenVerse URL and key the publisher is off. Run it on one host only.

## Admin API

All need `X-Admin-Key`; with no admin key set they answer 404. `X-Approver` names the person for the audit log. Localhost and the private network only.

`GET /v1/publisher/status`, `GET /v1/publisher/drafts?status=pending|all`, `GET /v1/publisher/drafts/{id}` (the draft with its audit history), `POST /v1/publisher/drafts` (queue an action), `POST /v1/publisher/drafts/{id}/approve` (optional JSON `{body,title,description}` edits the free text first; the proposal is kept on the draft as `original`), `.../reject` (optional `{reason}`), `POST /v1/publisher/run` (staff publish now), `POST /v1/publisher/events`, `POST /v1/publisher/mirror` (`{topic, news_id}`), `POST /v1/publisher/mirror/run`, `POST /v1/publisher/highlights/run`, `POST /v1/publisher/kill`, `GET /v1/publisher/audit?n=`, `DELETE /v1/publisher/posts/{id}` (remove one of Pakka's own OpenVerse posts; rollback and test cleanup, audited).

## Known limits

- `news.publish` goes through the News admin API (create article, publish). Consoles read News from nx-bcat; while nx-bcat serves its static tree (`NX_BCAT_SYNC_URL` unset) an article published this way does not reach consoles. The News to OpenVerse mirror does not depend on that.
- The Discord approval commands need the process that holds the queue to have the Discord gateway. In prod the publisher runs on the REST-only instance, so approvals go through the admin API there.
- Splatfest, tournament and course posts wait for those game servers to call `POST /v1/publisher/events`.
