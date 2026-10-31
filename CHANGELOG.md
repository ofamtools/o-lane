# Changelog

## Unreleased

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
