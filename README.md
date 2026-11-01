<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="o-lane" src="assets/logo-light.svg" width="360">
  </picture>
  <br>
  <a href="https://github.com/ofamtools/o-lane/tags"><img alt="Latest tag" src="https://img.shields.io/github/v/tag/ofamtools/o-lane"></a>
  <a href="https://pkg.go.dev/ofam.dev/o-lane"><img alt="Go Reference" src="https://pkg.go.dev/badge/ofam.dev/o-lane.svg"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/ofamtools/o-lane"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/ofamtools/o-lane"></a>
</p>

<p align="center">Actor-attributed records for work lanes: nested lanes per project with lifecycle states, live pane and agent status, and undoable history, plus a Lanes page that mtm mounts.</p>

Private, actor-attributed records for work lanes. A root lane is a main task; lanes may nest to any depth. The CLI and catalog share ochazuke's verbs and agent-rights gate, with oden dispatch.

Data lives in `$XDG_DATA_HOME/o-lane` (default `~/.local/share/o-lane`); newly created data directories use mode 0700. Existing directory permissions are preserved, with a warning if group/other access is enabled. `--data DIR` overrides it in any argument position before `--`. No git repository or remote is created in the data directory. No plan directory or export path is assumed.

## Install

```sh
go install ofam.dev/o-lane@latest
mtm extension register lane      # mounts the Lanes page and the lane.* verbs in mtm
```

## Usage

```sh
o-lane lane define --project app --lanes '[{"name":"readme"},{"name":"logo","parent":"readme"}]'
o-lane lane start logo                     # planned -> running
o-lane lane associate logo --pane WINDOW_ID
o-lane lane note logo "logo C picked"
o-lane lane state logo review
o-lane lane finish logo
o-lane lane rows --format table            # the page's columns
o-lane lane tree --state running,review    # JSON; matches keep their ancestors
o-lane                                     # the Lanes page
o-lane lane --help                         # every verb and its parameters
```

```text
$ o-lane lane rows --format table          # a review lane with no pane associated
Name    State                  Project  Repo  Estimate  Elapsed / duration  Panes         Agents
readme  review · unassociated  app                                          unassociated
logo    review · unassociated  app                      <1m                 unassociated
```

Every answer is JSON unless `--format` asks for a table. Add `--data DIR` to try it against a scratch store.

## Lanes and projects

The `lane` noun supports add, define, list, tree, show, history, set, get, rename, reword, move, copy, start, state, park, review, merge, finish, cancel, reopen, note, link, unlink, associate, unassociate, live, focus-pane, yank-id, yank-path, delete and undo. Discover parameters in the schema-1 catalog or noun help. `set` and `get` use the framework's standard typed-property grammar, including multi-property writes. `pane.associate`, `pane.unassociate` and `pane.lanes` expose the reverse relation using the same association handler; `agent.associate`, `agent.unassociate`, `agent.lanes`, `agent.live` and `agent.list` do the same for harness sub-agents (see Agents).

Every lane belongs to a project. A project is a reference only, an id or name with an optional host kind (`mtm:<id>`, `o-lane`); o-lane keeps no project store and does not need mtm. A lane's own `project` wins; otherwise it inherits the nearest ancestor's, so a root's project covers its subtree. Answers include `effective_project`, `project_from` and `project_association` (associated, inherited or unassociated). A missing project records the lane as unassociated and is never refused, but agents should always set one: `o-lane lane define --project PROJECT --lanes '[...]'` (the batch's top lanes take it), `lane add --project`, `plan define PATH --project` (the root takes it), or `set PROJECT` later. `lane associate --project P` and `lane unassociate --project P` share the relation handler with `project associate P LANE` / `project unassociate P LANE`; associating a root sets the project for its subtree, which is also how existing records migrate. A pane and project in one associate call share one undo. `lane list --project P`, `lane tree --project P`, `lane rows --project P` and `project lanes P` filter by effective project; a reference without a kind matches the same id or name under any kind. `project list` lists recorded references and, when `lanes.project-source` is `mtm` (the default when `mtm` is on PATH, else `none`), the names `mtm project list` returns through the injectable runner. That source is used only for completion (`project.choices` backs every project form field, free text allowed) and listing; it never validates writes.

## Defining lanes from JSON and plans

`define --json '{"lanes":[{"name":"task"},{"name":"implementation","parent":"task"}]}'` accepts an atomic JSON batch. Parents may appear before or after children. Parents resolve by exact ID or name; names declared in the same batch take priority and prefixes are never accepted as parents. Definitions match parent/name, update supplied spec fields, preserve existing owner wording and never delete lanes. Repeating an unchanged batch writes nothing. Explicit empty fields clear spec values; omitted fields remain unchanged. Duplicate parent/name rows are refused atomically.

`lane define --plan PATH` creates a root from the first Markdown H1 (or the filename without its extension) and children from the plan's Lanes table. `--name` overrides the root name, `--wording` supplies verbatim root wording on creation, and `--task` adds an optional root task link. `plan define PATH` shares this handler. `plan lanes PATH` lists recorded roots and children linked to that exact absolute plan path. Plan files are read only. Existing lifecycle state, history, panes and owner wording survive re-definition; removed table rows never delete records.

`lanes.import-columns` is a JSON header-to-spec-field map. Headers match case-insensitively after whitespace normalization; the longest whole-word prefix wins, so extended “Tests and stories (keys…)” headers work. Defaults include `Lane`, `Scope`/`Work`/`What`, `Repo`, `Depends on`, `Estimate`/`Est.`/`Lane time`/`Agent time`, `Done when`, and `Tests and stories`. Unknown columns are ignored; repeated mapped fields and duplicate or empty lane names are refused. Lane names use Markdown text without emphasis/code delimiters; other cells preserve source Markdown and estimate text. Each dependency cell is retained as one free-text entry, including external events. Explicit Lanes sections take precedence over earlier comparison tables; without one, a table whose first column maps to `name` is accepted. Fenced examples are ignored.

`--dry-run` on define parses and returns the proposed specs without accessing the store. `lane define --plans-dir DIR --dry-run` recursively checks Markdown files and returns sorted per-file lane counts, skipped files without lane tables, errors and a total `errors` count. Directory mode is read only and requires `--dry-run`; inspect `errors` in its JSON result. This checks parsing, names and column mappings; stored parent resolution and write policy are checked when definitions execute. MCP's outer `run.dry_run` performs the framework's request/policy preview without invoking the parser; use the define argument `dry-run` to preview parsed specs through MCP.

`lanes.plans-dir` is empty by default. When set, exact bare filenames (with an optional `.md` suffix) resolve there; explicit paths work independently. Neither plan discovery nor any runtime default assumes a particular workspace. Both define verbs are allowed for agents, subject to `agents.rights`; they retain the store's atomic write, actor and undo behaviour.

## States and live panes

Lifecycle states are JSON rows in `lanes.states`, with `name`, `tone`, `starts`, `ends`, `open`. The default names are planned, running, parked, review, merged, done and cancelled; there is no transition table. `lanes.state-presets` maps start/park/review/merge/finish/cancel/reopen to state names; configure it alongside a custom state table. A missing preset or a target absent from `lanes.states` refuses with a reason naming `lanes.state-presets`. Started and finished are derived from effective history, including after undo. A move to any non-ending state clears finished. Root views expose both the recorded state and `rolled_state`: mixed descendants use the first configured open state present, otherwise the first configured closed state present.

Pane refs include host, session, window, role and optional harness/model/agent. Unknown window IDs are accepted. Empty window refs are unassociated; pane-less subagents retain their participation metadata. A recorded window absent from a successful snapshot reads closed. The CLI `live` verb returns every associated pane and the aggregate over the lane’s whole subtree. CLI `show`, `list`, `tree` and portable `rows` answers also include derived live state, sharing one snapshot per explicit host/session per call through the injectable runner, without a subscription. Live reads never write records or history. `lanes.live=false` disables reads. Focus uses `lanes.pane-open` as an argv template, without shell evaluation. Headless agents cannot focus panes; the standalone page supplies live interaction. Focus runs through the injectable runner off the UI loop and still works when live reads are disabled. Yank verbs write the framework register and return the text.

## Agents (subagents inside one pane)

Lanes are often run by harness sub-agents, several inside one pane (Claude Code subagents today; Codex or omp sub-agents are new data rows). Associate a lane with the agent, not only its pane: `o-lane lane associate LANE --agent-id ID --harness claude-code [--agent-session S] [--agent ofam-lane] [--role R]`. `agent associate ID LANE`, `agent unassociate ID LANE` and `agent lanes ID` are the agent-side view of the same relation and share its handler; `lane unassociate LANE --agent-id ID` removes it. A participant that names an agent is keyed by harness and agent id (re-associating updates it), so an agent and the pane it runs in can both be recorded. The Claude Code agent id is the `agentId` the Agent tool reports (also the `agent-<id>.jsonl` transcript name). Agent ids must be simple (letters, digits, `_ . -`). Undo restores agent relations like any other change.

Agent live state is derived from data, never stored: `lanes.agent-harnesses` maps a harness name to a row with `transcripts` (path templates; `{agent}` and `{session}` are substituted, `{session}` is `*` when unknown, then `~`, `$VAR` and `${VAR:-default}` expand and the result is globbed), `time_field` (the RFC 3339 field of a transcript line; empty uses the file's modification time), optional `idle`, `events` (hook event name to `start`/`stop`) and `fields` (hook payload field names for event, agent, session, type, transcript). The default row reads Claude Code's `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/*/<session>/subagents/agent-<id>.jsonl`. States are:

- `done`: the latest recorded hook event for the agent is a stop (an agent-team teammate that wakes again records a start and reads working again);
- `working`: the last transcript line (or a start event) is newer than `lanes.agent-idle` (default 3m; 0 never idles; a row's `idle` overrides it);
- `idle`: older than that;
- `missing`: no transcript and no recorded events.

Reads stat and tail (64 KiB) each transcript once per refresh, off the UI loop; the hook log is read incrementally. Agent states use `lanes.live-states` words, tones and glyphs (a `missing` row is added) and join the lane's live aggregate, so an open lane shows `running · working`. The Agents column shows each agent with its state and last-activity age (`claude-code ofam-lane a3f09c2e · working 2m`); pane detail adds the full agent id, agent session and activity time. `agent live ID` reads one agent, `agent list` lists every agent the hook recorded, newest first. `lanes.agent-live=false` turns agent reads off.

`o-lane agent-hook HARNESS [EVENT]` records a hook payload from stdin as one appended line in `agent-events.jsonl` in the data directory. It never takes the store lock, reads at most 1 MiB for at most 2s, ignores events the row does not map, refuses unsafe ids, prints nothing on stdout and always exits 0 (errors go to stderr), so it cannot break the harness. The event argument overrides the payload's event field.

Install the hook with ochazuke's shared setup verbs. o-lane declares two setup items (group `claude-code`), `claude-subagent-start` and `claude-subagent-stop`, which add `{"hooks":[{"type":"command","command":"o-lane agent-hook claude-code","timeout":5}]}` to `hooks.SubagentStart` and `hooks.SubagentStop` in `${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json`, beside existing entries (such as tuios's). They own only entries whose first command contains `o-lane agent-hook`.

```sh
o-lane setup                                  # status: installed / missing / n/a
o-lane setup plan                             # dry run: the whole resulting file, nothing written
o-lane setup install --yes                    # or: o-lane setup install --group claude-code --yes
o-lane setup uninstall --yes                  # removes only o-lane's entries
o-lane setup status --home /tmp/fixture-home  # any command against another home (tests)
```

Install is idempotent, writes atomically, keeps the first original as `settings.json.o-lane.bak` (never overwritten) and needs owner confirmation (`--yes`); agents cannot confirm. `CLAUDE_CONFIG_DIR` picks another Claude Code directory, as it does for Claude Code; `--home` isolates HOME/XDG. Without a Claude Code directory the items read `n/a`. `setup.skip` lists item ids to skip.

## Store, history and identity

Every mutation is process-locked, writes through a recoverable transaction and retains an undo snapshot. Root files contain the root and its descendants. Old root documents become empty after moves/undo rather than being unlinked. Delete hides the entire subtree and is undoable. `lane.undo` restores the latest outstanding snapshot and appends an undo receipt to each affected lane. Original entries remain visible, with the undone entry IDs, original actor and undoing actor recorded; derived values exclude undone entries. Creation undo retains the record as a deleted audit tombstone. `lane list --deleted` includes deleted records and their complete histories; an agent may undo only its own latest change, preventing a snapshot from overwriting another actor's later work. Daily private archives retain 14 days by default (`backup.daily`, `backup.keep`). There is no restore/import/export command yet.

Owner is the default actor. `OCHAZUKE_ACTOR` (owner, agent or JSON) explicitly binds identity; an agent's missing harness/model/pane/session fields use the framework environment. Harness detection recognizes `TUIOS_HARNESS`, `AGENT_HARNESS`, Codex and Claude Code. A pane alone does not imply an agent. `OCHAZUKE_ACTOR` is the single explicit override; detected harnesses retain agent status even if it requests owner. `O_LANE_ACTOR` is ignored. Codex thread IDs and Claude session IDs (`CLAUDE_SESSION_ID` or `CLAUDE_CODE_SESSION_ID`) distinguish runs in the actor name; a harness alone supplies no identifying name. Anonymous agents cannot claim undo ownership. CLI identity is advisory, not authentication. History records the request-bound actor, including MCP actors.

Agent defaults allow ordinary lane work and reverse relations. Delete and reword are never allowed by default. Owner rules in `agents.rights` may override declarations. Future import is never and export confirm; those verbs are intentionally absent until implemented. Optional export settings are declarations only, with an empty path default. The standalone TUI and portable catalog page share the same lane data.

Dependencies are released tags of ochazuke, oden and monogatari (page stories); `go.mod` has the versions. The Makefile bounds Go parallelism and uses `GOWORK=off`, trimpath and nice. `gate` includes formatting, vet and race tests for the coordinator.

## The Lanes page

Opening o-lane without command words, or using `view`, opens the Lanes page. It publishes the same portable page in the catalog for extension hosts, including mtm. The tree preserves arbitrary nesting, lists parents before children and orders sibling records newest first. Columns show name, rolled lifecycle state, repo, estimate, elapsed time (or completed duration), associated pane counts by role, and harness/model/agent metadata. Lifecycle tones come from `lanes.states`; recorded state and rolled state remain separate detail fields.

The detail uses ochazuke's fields, Markdown and thread sections: owner wording, scope, done-when, pane references with role/harness/session/since, complete spec metadata and actor-attributed history. Enter opens a full read-only record; Escape closes it. The catalog declares six binding rows: Enter show, `s` choose state, `g p` focus pane, `a` associate pane, `n` note and `y` yank ID. Hints come from those rows. State and pane pickers use `lane.choose-state` / `lane.choose-pane`, thin entry points to the existing handlers. The CLI's `state --from-tag` and missing-pane recording stay available. In the pane form, `ctrl+space` opens tuios window suggestions; a picked reference includes its explicit session, and the role defaults to implementer. An unavailable chooser reports an error without changing the lane. Direct `associate` still records unknown or unassociated panes.

Page settings and defaults:

| Setting | Default | Effect when disabled |
|---|---|---|
| `lanes.tree` | true | Flat parent-first rows; next page mount |
| `lanes.state-tones` | true | Plain lifecycle cells |
| `lanes.live` | true | Lifecycle-only cells; no snapshots or subscription |
| `lanes.live-tones` | true | Keep live words and glyphs without their colour |
| `lanes.agent-live` | true | Agent participants read no transcripts or hook log; their lanes show no agent state |
| `lanes.agent-idle` | 3m | Set to 0 so an agent never reads idle |
| `lanes.name-tone` | true | Plain lane names; otherwise the name uses the tone that draws its state cell (live tone when shown, else lifecycle tone). Selection and focus styles still win |
| `lanes.durations` | true | Hide the elapsed/duration column |
| `lanes.pane-column` | true | Hide the pane-role column |
| `lanes.history-in-detail` | true | Hide the history section and history projection |
| `lanes.detail` | true | Hide detail and Enter viewer; next page mount |
| `lanes.hide-closed-roots` | false | Show closed roots and their children |
| `lanes.refresh` | 5s | Set to 0 to disable standalone polling |
| `lanes.group-by-project` | true | Roots keep newest-first order instead of grouping by project (named projects alphabetically, unassociated last) |
| `lanes.project-column` | true | Hide the project column |
| `lanes.project-filter` | "" | Empty shows every project; a reference shows only subtrees containing lanes of that project |
| `lanes.state-filter` | [] (all) | Empty shows every state; a list of lifecycle states shows matching lanes with their ancestors as context |

### Filtering

Filtering by status. Lane rows carry read-only props for ochazuke's prop filters: `status` (the recorded lifecycle state; the prop cannot be called `state` because `lane state` is a verb, and a word after a noun is a verb or a prop, never both), `rolled_state`, `live_state` (empty when live reads are off), `name`, `parent`, `project`, `effective_project`, `repo`, `branch`, `estimate`, `created`, `updated`, `started`, `finished` (RFC 3339 text; empty when unset) and `pane_count` (integer), alongside the editable spec props. Every prop is present on every row, so `!=` compares against an empty value. On the page, `z /` picks a prop then one of the values on screen (`z /` → status → running); repeat it to AND more conditions, `z !` drops one, `z C` clears them, `list.filter-save` saves the current conditions to `list.filters` and `z ]`/`z [` cycle saved filters. o-lane declares one saved filter, `open`: `status != X` for every state without `open` in `lanes.states` (by default merged, done, cancelled), kept in step with the configured states; a user `list.filters` row named `open` replaces it. Saved rows can also use the `in` operator, for example `{"name":"active","noun":"lane","conditions":[{"prop":"status","op":"in","value":["running","review"]}]}`. The `/` text filter matches the name and every visible cell (state, project, repo, ...), not hidden data; matching hidden text needs ochazuke's unreleased `ListItem.Search` (a skipped test, `TestRowSearchText`, marks the follow-up).

`--state` takes a comma list of lifecycle states on `lane list`, `lane tree`, `lane rows` and `project lanes` (`--state running,review`; repeating the flag keeps only the last value, as ochazuke's CLI does for every string flag). Unknown names are refused with the configured list. `lanes.state-filter` applies the same filter to the page, like `lanes.project-filter`; `lane rows --state` overrides it for one fetch. It composes with the project filter (both must hold) and with `lanes.hide-closed-roots`, which still hides a closed root even when it is context.

Tree rule (the same for `z /` prop filters, `--state` on `lane tree`/`lane rows` and `lanes.state-filter`): a lane that matches keeps its ancestors visible as context, and a matching lane's children show only when they match too. Flat answers (`lane list`, `project lanes`) list matches only. Context rows are not dimmed: ochazuke's tree has no context style yet, so a context row looks like any other row; its status cell shows why it is there.

### Live state on the page

The general `tree.guides` and `detail.sections` settings also apply. Durations, columns, state tones, history and closed-root filtering refresh in the standalone page after a setting changes; tree shape and detail visibility take effect on the next mount. A host reads settings-dependent columns and sections from the catalog, so refresh its extension catalog after changing those settings. Unregistering the extension is mtm's opt-out. No owner config is edited by registration examples or tests. Rendering uses cached rows. An interactive standalone page owns one cancellable `tuios subscribe --types agent-state,window-closed --hosts` reader. It resumes with `--after-seq` and `--boot-id`, refreshes explicit-session window snapshots at subscription start and after a gap, and reconnects with bounded backoff. A filtered stream's sequence jumps are normal; only gap markers trigger resync. Disconnects read unknown until a snapshot succeeds. A remote pane uses tuios's `host:session` selection; an unreachable host reads unknown. Closing the page or disabling live reads cancels its reader.

Mounted catalog hosts receive the same live cells, glyphs and detail data through `lane.rows` snapshots on each fetch. The released v0.72.1 catalog has no page-lifetime subscription hook for external processes, so mounted hosts refresh live state when they fetch rows; the standalone reader is not started by a CLI/catalog request. A persistent subscription in a mounted host requires a general framework transport change.

`lanes.live-states` maps raw tuios states to JSON `{name, tone, glyph}` objects. Default words/tones/glyphs are working/state.working/●, needs input/state.needs-input/?, errored/state.errored/✗, done/state.done/✓, idle/state.idle/○ and closed/state.exited/×. Empty or `none` agent state reads shell with no tone or glyph. Existing string-only map values remain accepted and supply plain labels. Unknown raw states retain their words. `lanes.live-precedence` defaults to needs input, errored, working, done, idle, closed; the first present label wins. Custom labels absent from precedence use alphabetical fallback. `lanes.live-roles` defaults to all roles (empty list), including watcher; narrowing it affects the aggregate, while pane detail still lists every role.

Open lanes show a combined cell such as `running · needs input`: each word uses its own configured tone, and a leading name-cell glyph uses the live tone. Closed lifecycle records show the lifecycle word alone. Live words and structured per-pane name/tone/glyph/error data are derived; snapshots, subscriptions and gaps never change records, history or lifecycle state. The portable pane Markdown detail shows live words; its renderer has no per-run tone metadata.

### Compared with Vim

Compared with Vim: lane states, pane association and elapsed time are domain operations (no Vim counterpart). Tree navigation, folding, search, yanks and scroll windows use ochazuke's existing bindings: `j`/`k` correspond to Vim `j`/`k` (`:help j`, `:help k`); the read-only viewer's `gg`/`G` and `ctrl+d`/`ctrl+u` are similar to `:help gg`, `:help G`, `:help CTRL-D` and `:help CTRL-U` (viewer page-down/up moves eight lines). No application key switch or function-key binding is introduced.

| Vim form | Help tag | o-lane comparison |
|---|---|---|
| `j` / `k` | `j`, `k` | Similar: visual list rows or viewer lines; Vim moves buffer lines |
| `gg` / `G` | `gg`, `G` | Similar: first/last viewer window position; no text cursor or line argument |
| `ctrl+d` / `ctrl+u` | `CTRL-D`, `CTRL-U` | Similar: viewer scrolls eight lines; Vim defaults to half a screen |
| Lane states, panes, durations | n/a | Domain records have no Vim counterpart |

## MCP

In the family, agents reach o-lane through mtm's single MCP gateway (`mtm mcp serve`, which runs `lane.*` once the extension is registered). The released generic MCP server is also available as `o-lane mcp serve` (the fully qualified spelling is `o-lane ochazuke mcp serve`). It provides catalog, describe, run and state over newline-delimited JSON-RPC. `o-lane mcp config` prints the harness entry, with the qualified spelling to avoid future app-name collisions. MCP always binds an agent identity, even if the launching shell identifies as owner; tool arguments cannot supply an actor. The stdio smoke test covers define/start/state/associate, dry-run, immutable request identity and declaration-named refusals for delete/reword. Import remains absent until L6, where it must declare never; export must declare confirm.

A harness config entry has the following shape; use the generated `mcp config` output for the installed executable path. It uses normal XDG storage unless an explicit `--data` override is added to the arguments.

```json
{"mcpServers":{"o-lane":{"command":"o-lane","args":["ochazuke","mcp","serve"]}}}
```

MCP's framework history describes requests in the current server process. Durable domain undo is `lane.undo`; custom domain verbs do not publish a framework `history.undo` inverse. No harness or owner config is edited by the application.
