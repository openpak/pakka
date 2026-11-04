# Changelog

## 0.14.0 - 2026-11-04

- One console-neutral shop surface (openpak/prds#124, phase P2). The broken-package tracker has a console field: `POST /v1/shop/broken` `{console, broken}`; `POST /v1/eshop-broken` stays as an alias for `console=switch`. Alerts keep the `[Switch - eshop]` wording for the Switch and read `[<Console> - shop]` for others.
- Durable in shop-core: with `PAKKA_SHOP_CORE_URL`/`PAKKA_SHOP_CORE_KEY` the tracker keeps its state in shop-core's `shop_broken` table; the Switch's old `eshop-broken` state doc is moved there once and emptied. Without them nothing changes. A shop-core that is down at start is retried on every report and never overwritten by a partial view.
- `!pakka shop [console]` (status of every managed shop; `!pakka eshop` is unchanged) and staff commands `shop sync|pause|resume <console>`, `shop retire|unretire <console> <title> [reason]`, `shop broken [console]`, `shop runs [console]`. A paused shop starts no sync run. `PAKKA_SHOP_CONSOLES` names the managed consoles (default `switch`).

## 0.13.1 - 2026-11-04

- The one-shot import of the legacy state directory renames each imported file to `<name>.imported-20261104` (never deletes it). Before, the files stayed and every start re-imported them, so a conversation erased from Postgres (account erasure, user delete) could come back. Test: import, erase, re-import. (openpak/prds#85)

## 0.13.0 - 2026-11-04

- Discord account gate (`internal/discordgate`): before the model answers a free-text mention, the Discord user must be linked to an OpenPak account (website `GET /internal/discord/{id}`), be an adult with no supervision, and have accepted the Meta data-use notice. Child accounts are never answered, and anything that cannot be verified is refused (fails closed). Commands are unchanged.
- Env: `PAKKA_DISCORD_GATE_KEY` (the website internal key; without it every free-text answer is refused), `PAKKA_DISCORD_GATE_URL`, `PAKKA_DISCORD_LINK_URL`, `PAKKA_DISCORD_NOTICE_URL`.

## 0.12.0 - 2026-11-04

- Publisher review for the website admin queue: `POST /v1/publisher/drafts/{id}/approve` takes an optional edit of the free text (the model's proposal is kept on the draft as `original`, with `edited_by`), `.../reject` takes a reason, and `GET /v1/publisher/drafts/{id}` returns the draft with its audit history from Postgres. Edits are validated and audited (`edited`) before anything publishes; templated posts cannot be edited.
- `DELETE /v1/chat/account`: erases every chat conversation, the consent record and the rate-limit state of one account, for the website's account-erasure finaliser.

## 0.11.0 - 2026-11-03

- All durable state moved to Postgres (`PAKKA_DATABASE_URL`, pool of 3): chat consents and conversations, eShop broken first-seen, release watch, publisher queue, mirror, done set and audit log. One database backup now covers Pakka.
- Existing JSON files under `PAKKA_STATE_DIR` are imported on start (copy, never overwrite an existing row, files left in place).
- Any enabled stateful feature (publisher, chat, eShop broken watch, release watch) now refuses to start without `PAKKA_DATABASE_URL`; `PAKKA_ALLOW_FILE_STORE=1` keeps the old JSON files for local development. Instances with no stateful feature (the status box) start without a database.
