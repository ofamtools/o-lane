# Changelog

## Unreleased

- Lanes page: the detail opens with the focused lane's state, a plain description of what it means and the reason (the state change's note, else the last note before it). Descriptions are an optional `description` field on `lanes.states` rows; defaults describe every state except planned. `lane show`/`lane rows` add `state_description` and `state_reason`. Pane detail labels live status `agent <state>` and the `live state` field reads `agent status`. `lanes.state-descriptions` (default true) turns the section off.
- Accept tuios session/window lists as either JSON arrays or named objects for pane choices and live snapshots; preserve explicit session/window targets when focusing a lane's pane with `g p`.

- Associate lanes with harness sub-agents: `lane associate/unassociate --agent-id ID [--harness H] [--agent-session S]` and the agent-side `agent associate/unassociate/lanes` share the association handler; `agent live` and `agent list` read agent state. Agent participants are keyed by harness and id.
- Agent live state (working, idle, done, missing) derived from `lanes.agent-harnesses` data rows (transcript path templates, time field, idle override, hook event map, payload fields; default row for Claude Code subagents) and `lanes.agent-idle` (default 3m). It joins the lane's live aggregate; the Agents column shows each agent's state and last-activity age. `lanes.agent-live` (default true) turns it off. Adds a `missing` row to the default `lanes.live-states` and `lanes.live-precedence`.
- `o-lane agent-hook HARNESS [EVENT]` appends a hook payload's start/stop to `agent-events.jsonl` in the data directory; bounded, lock-free and always exits 0.
- `o-lane setup` (ochazuke's shared setup verbs) declares `claude-subagent-start` and `claude-subagent-stop`, which add `o-lane agent-hook claude-code` to Claude Code's `hooks.SubagentStart`/`SubagentStop` beside existing entries; idempotent, backed up, with plan and uninstall.

- Filter lanes by status: lane rows declare read-only filter props (`status`, `rolled_state`, `live_state`, `effective_project`, `created`/`updated`/`started`/`finished`, `pane_count`, plus `name` and `parent`) so ochazuke's `z /` prop filters and saved filters work on the Lanes page; a default `open` saved filter follows `lanes.states`. `--state` (comma list) on `lane list`, `lane tree`, `lane rows` and `project lanes`, and a `lanes.state-filter` page setting (default empty, all states). Tree views keep a match's ancestors as context and show a matching lane's children only when they match.
- Lanes page: the lane name takes the same tone as its state cell (lifecycle tone, or the live tone when live state is shown), from the existing tone data; `lanes.name-tone` (default true) turns it off.
- Scope lanes to projects: a `project` reference on lanes (inherited from the nearest ancestor), `--project` on lane define/add and plan define, `lane associate/unassociate --project` sharing one handler with `project associate/unassociate`, `project lanes`/`project list`, and `--project` filters on lane list/tree/rows. Records without a project load as unassociated.
- Lanes page: project column, grouping of roots by project and a project filter (`lanes.project-column`, `lanes.group-by-project`, `lanes.project-filter`). Optional `lanes.project-source` (mtm when installed, else none) completes project names through the injectable runner; it is never required.

- Derive live lane/subtree state from explicit tuios window snapshots and a cancellable standalone subscription, with seq/boot resume, gap recovery, disconnect handling and retained closed-pane references.
- Add configurable live names, tones and glyphs, precedence and aggregate roles, combined lifecycle/live cells, per-pane live detail and live/colour opt-outs. Preserve existing string-only state mappings; live reads never write lane history.
- Include live projections in CLI/MCP read answers and portable page rows. Mounted extensions use fresh snapshots on row fetch; persistent hosted subscriptions await a general catalog transport hook.

- Define lanes from Markdown plan tables, with configurable column mappings, portable plan lookup, plan.define/plan.lanes and read-only directory parsing reports.
- Preserve explicit empty JSON/table updates and reject duplicate definitions while keeping exact-name re-definition idempotent, actor-attributed and undoable.
- Add the standalone and portable Lanes tree page with configurable lifecycle tones, derived durations, pane-role/agent columns, Markdown/fields/history detail sections and a read-only record viewer.
- Add catalog binding rows and framework state/pane chooser entry points, using explicit tuios sessions and an injectable runner; existing CLI state/association semantics remain available.
- Add page opt-outs and optional asynchronous refresh; focus panes off the UI loop and retain recorded-pane focus when live reads are disabled. Add four deterministic monogatari page stories and temporary-data mtm registration verification.

- Scaffold o-lane with an oden/ochazuke catalog shell, XDG data paths and public settings.
- Add nested lane records, actor-attributed history, configurable states, typed properties, idempotent JSON batches, pane relations, state presets, links, retry copies and CLI snapshot/focus/yank verbs.
- Add private per-root storage with process locking, atomic recovery, daily backups and undo; agent rights follow the catalog declarations and owner overrides.
- Expose the released generic catalog MCP shell; verify bound agent identity, dry runs and delete/reword refusal over stdio.
- Let pane removal resolve a window ID without repeating its recorded session or host.
- Resolve define/add parents exactly, preferring batch names, and preserve idempotence when a prefix matches an unrelated lane.
- Distinguish agent runs for undo, remove the app-specific actor override, and prevent detected harnesses from becoming owners through identity overrides.
- Preserve original history during undo, append attributed receipts, and expose deleted audit records with `lane list --deleted`.
- Make lifecycle presets configurable through `lanes.state-presets`; clear finished times after reopening.
- Preserve existing data-directory permissions with warnings; isolate tests with temporary directories and cleared identity variables.
- Share pane query matching and bound subprocess waits when descendants retain output pipes.
