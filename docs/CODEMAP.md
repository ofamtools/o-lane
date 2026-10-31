# Code map

- `main.go`: XDG data path, global `--data`, oden dispatch and ochazuke CLI mount.
- `app.go`: catalog verbs, explicit app nouns, shared relation dispatch, transport-bound actors, settings configuration and target reveal.
- `props.go`: framework standard typed `get`/`set` over the lane store.
- `settings.go`: public defaults; lifecycle rows and configurable state presets, roles, links, watcher tags and later page/import/export declarations.
- `model.go`: specs, lanes, pane refs, links, actor-attributed history; derived lifecycle times, estimates and root rollup.
- `actor.go`: harness detection, per-run identity, the single framework override and store policy fallback; the app honours framework rights overrides.
- `plan.go`: Goldmark table parsing, validated configurable column aliases, shared JSON/plan definition handler, exact plan relation queries and read-only directory checks.
- `plan_test.go`, `testdata/plans/`: three copied table variants and golden specs; CLI/MCP rights, idempotency, source preservation and dry-run isolation regressions.
- `changes.go`: atomic define/add, domain mutations, state presets, shared association handler, cycle refusal and undoable subtree delete.
- `store.go`: process lock, per-root documents, transaction recovery, snapshots and actor-safe undo with append-only audit receipts.
- `atomic.go`: private synced temporary writes, rename and directory sync (adapted from o-questions).
- `backup.go`: daily private tar/gzip archives and retention (adapted from o-questions).
- `live.go`: validated data-driven live labels/tones/glyphs (including legacy string maps), explicit host/session snapshot batching, all-level subtree aggregation, CLI read enrichment and cancellable argv focus. Role filters narrow aggregates while retaining pane details; all live state is derived.
- `live_reader.go`: incremental stdout through the existing injectable runner, bounded scanner/event/notification queues, mutex-protected snapshot cache, seq/boot reconnect, gap resync, replay ceiling, bounded backoff and cancellation. Settings and framework objects stay on the UI loop.
- `live_test.go`, `testdata/live/`: aggregate/settings/snapshot/persistence tables, recorded gap/replay/close streams, boot-change and page-reader lifecycle regressions. A stub tuios executable on temporary PATH exercises the production runner without a daemon.
- `app_test.go`, `cli_test.go`: isolated schema, per-verb CLI, rights, actor and configurable-state checks.
- `store_test.go`: process concurrency, recovery, undo, cycle refusal, define atomicity and daily archives.
- `review_test.go`: V1–V7 regressions plus shared pane matching, effective finish times, subprocess pipe bounds, deleted history and legacy/crash-safe audit undo.
- `mcp_test.go`: generic framework stdio protocol, transport attribution, dry run, rights refusals and generated harness config.
- `page.go`: portable tree-page declaration, lifecycle/live word spans and live glyph tones, derived duration/pane-role cells, detail sections, binding rows and chooser sources; state and pane chooser entry points share existing domain handlers. The pane chooser discovers sessions/windows through the injectable runner and carries explicit session/window references.
- `tui.go`: standalone mount, asynchronous replacement row loads and optional refresh timer, reader lifetime/generation/cancellation and live settings projection, plus a cached read-only detail viewer. Rendering reads only cached rows; tree/field/detail layout and keys belong to ochazuke. CLI/smoke/catalog requests start no subscription.
- `page_test.go`, `cmd/o-story/stories/lanes/*.json`: monogatari fixtures and the four L4 plus two L8 stories, plus tree/catalog mounting, opt-outs, fixed-clock durations, ordering, pane chooser failures, focus and yank checks. Data and identity are isolated; every external command is fake.
- `Makefile`: bounded Go build/test and coordinator race gate.

The app owns domain state and persistence; released ochazuke owns the CLI, catalog, rights gate and UI. No family-library modifications or local module replacements are required.

Compared with Vim: domain operations have no Vim counterpart. Navigation/folding/search reuse ochazuke; the read-only viewer uses its scroll window with j/k, gg/G and ctrl+d/ctrl+u binding rows (eight-line half-page movement). See README for help tags and differences.

Mounted extension rows include live snapshots. Released ochazuke v0.72.1 has no persistent external page subscription hook; hosted reader lifetimes and per-run tones in Markdown detail require general framework capabilities. No app-specific daemon or host workaround is introduced.
