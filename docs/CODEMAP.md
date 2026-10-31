# Code map

- `main.go`: XDG data path, global `--data`, oden dispatch and ochazuke CLI mount.
- `app.go`: catalog verbs, explicit app nouns, shared relation dispatch, transport-bound actors, settings configuration and target reveal.
- `props.go`: framework standard typed `get`/`set` over the lane store.
- `settings.go`: public defaults; lifecycle rows, roles, links, watcher tags and later page/import/export declarations.
- `model.go`: specs, lanes, pane refs, links, actor-attributed history; derived lifecycle times, estimates and root rollup.
- `actor.go`: o-questions-style environment identity and store policy fallback; the app honours framework rights overrides.
- `changes.go`: atomic define/add, domain mutations, state presets, shared association handler, cycle refusal and undoable subtree delete.
- `store.go`: process lock, per-root documents, transaction recovery, snapshots and actor-safe undo.
- `atomic.go`: private synced temporary writes, rename and directory sync (adapted from o-questions).
- `backup.go`: daily private tar/gzip archives and retention (adapted from o-questions).
- `live.go`: one-shot tuios reads, aggregation and argv-based focus through an injectable runner. Subscriptions/page remain later work.
- `app_test.go`, `cli_test.go`: isolated schema, per-verb CLI, rights, actor and configurable-state checks.
- `store_test.go`: process concurrency, recovery, undo, cycle refusal, define atomicity and daily archives.
- `mcp_test.go`: generic framework stdio protocol, transport attribution, dry run, rights refusals and generated harness config.
- `Makefile`: bounded Go build/test and coordinator race gate.

The app owns domain state and persistence; released ochazuke owns the CLI, catalog, rights gate and future UI. No family-library modifications or local module replacements are required.

Compared with Vim: not applicable; lanes are domain records. Future tree motions use ochazuke's existing behaviour.
