# mtm pane sample output

Captured 2026-11-08 from a throwaway `mtm --server` daemon in a temp HOME and
XDG_RUNTIME_DIR (branch `mtmd-integrate`): `mtm session list --json`,
`mtm session list-panes --json --session mtm-only`, and
`mtm session events --wait_ms N --types '["agent-state"]'` with and without a
cursor. Window ids and paths are from the throwaway daemon. `job #N ... done`
lines go to stderr and are not part of the samples.
