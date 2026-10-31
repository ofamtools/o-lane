# o-lane

Private, actor-attributed records for work lanes. A root lane is a main task; lanes may nest to any depth. The CLI and catalog share ochazuke's verbs and agent-rights gate, with oden dispatch.

Data lives in `$XDG_DATA_HOME/o-lane` (default `~/.local/share/o-lane`), mode 0700. `--data DIR` overrides it in any argument position before `--`. No git repository or remote is created in the data directory. No plan directory or export path is assumed.

The `lane` noun supports add, define, list, tree, show, history, set, get, rename, reword, move, copy, start, state, park, review, merge, finish, cancel, reopen, note, link, unlink, associate, unassociate, live, focus-pane, yank-id, yank-path, delete and undo. Discover parameters in the schema-1 catalog or noun help. `set` and `get` use the framework's standard typed-property grammar, including multi-property writes. `pane.associate`, `pane.unassociate` and `pane.lanes` expose the reverse relation using the same association handler.

`define --json '{"lanes":[{"name":"task"},{"name":"implementation","parent":"task"}]}'` accepts an atomic JSON batch. Parents may appear before or after children. Definitions match parent/name, update supplied spec fields, preserve existing owner wording and never delete lanes. Repeating an unchanged batch writes nothing. Markdown plan parsing belongs to the next lane.

Lifecycle states are JSON rows in `lanes.states`, with `name`, `tone`, `starts`, `ends`, `open`. The default names are planned, running, parked, review, merged, done and cancelled; there is no transition table. Presets name default states; a custom state table must retain those names to use those presets. Started and finished are derived from history, including after undo. Root views expose both the recorded state and `rolled_state`: mixed descendants use the first configured open state present, otherwise the first configured closed state present.

Pane refs include host, session, window, role and optional harness/model/agent. Unknown window IDs are accepted. Missing windows are unassociated; pane-less subagents retain their participation metadata. The CLI `live` verb reads snapshots using an explicit recorded session and an injectable command runner, without a subscription. It never writes live changes to history. `lanes.live=false` disables reads. Focus uses `lanes.pane-open` as an argv template, without shell evaluation. Headless agents cannot focus panes; a later UI host supplies live interaction. Yank verbs write the framework register and return the text.

Every mutation is process-locked, writes through a recoverable transaction and retains an undo snapshot. Root files contain the root and its descendants. Old root documents become empty after moves/undo rather than being unlinked. Delete hides the entire subtree and is undoable. `lane.undo` restores the latest outstanding snapshot; an agent may undo only its own latest change, preventing a snapshot from overwriting another actor's later work. Daily private archives retain 14 days by default (`backup.daily`, `backup.keep`). There is no restore/import/export command yet.

Owner is the default actor. `OCHAZUKE_ACTOR` (owner, agent or JSON) explicitly binds identity; an agent's missing harness/model/pane/session fields use the framework environment. In its absence, o-questions-style harness detection also recognizes `TUIOS_HARNESS`, `AGENT_HARNESS`, Codex and Claude Code. A pane alone does not imply an agent. `O_LANE_ACTOR` is an app identity override when no framework identity was supplied. History records the request-bound actor, including MCP actors.

Agent defaults allow ordinary lane work and reverse relations. Delete and reword are never allowed by default. Owner rules in `agents.rights` may override declarations. Future import is never and export confirm; those verbs are intentionally absent until implemented. Optional export settings are declarations only, with an empty path default. UI settings are also declared for the later page; this version does not open a TUI.

Dependencies are released tags: ochazuke v0.72.1, oden v0.1.1 and monogatari v0.3.0 (reserved for page stories). The Makefile bounds Go parallelism and uses `GOWORK=off`, trimpath and nice. `gate` includes formatting, vet and race tests for the coordinator.

Compared with Vim: lanes are domain records, so no Vim editing behaviour is introduced. Future tree motions use ochazuke's existing behaviour.

The released generic MCP server is available as `o-lane mcp serve` (the fully qualified spelling is `o-lane ochazuke mcp serve`). It provides catalog, describe, run and state over newline-delimited JSON-RPC. `o-lane mcp config` prints the harness entry, with the qualified spelling to avoid future app-name collisions. MCP always binds an agent identity, even if the launching shell identifies as owner; tool arguments cannot supply an actor. The stdio smoke test covers define/start/state/associate, dry-run, immutable request identity and declaration-named refusals for delete/reword. Import remains absent until L6, where it must declare never; export must declare confirm.

A harness config entry has the following shape; use the generated `mcp config` output for the installed executable path. It uses normal XDG storage unless an explicit `--data` override is added to the arguments.

```json
{"mcpServers":{"o-lane":{"command":"o-lane","args":["ochazuke","mcp","serve"]}}}
```

MCP's framework history describes requests in the current server process. Durable domain undo is `lane.undo`; custom domain verbs do not publish a framework `history.undo` inverse. No harness or owner config is edited by the application.
