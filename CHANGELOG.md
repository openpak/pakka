# Changelog

## 0.11.0 - 2026-11-03

- All durable state moved to Postgres (`PAKKA_DATABASE_URL`, pool of 3): chat consents and conversations, eShop broken first-seen, release watch, publisher queue, mirror, done set and audit log. One database backup now covers Pakka.
- Existing JSON files under `PAKKA_STATE_DIR` are imported on start (copy, never overwrite an existing row, files left in place).
- Any enabled stateful feature (publisher, chat, eShop broken watch, release watch) now refuses to start without `PAKKA_DATABASE_URL`; `PAKKA_ALLOW_FILE_STORE=1` keeps the old JSON files for local development. Instances with no stateful feature (the status box) start without a database.
