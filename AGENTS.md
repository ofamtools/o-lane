# o-lane

Created by o-proj.

## Plans and todos

Family plans, todos and the roadmap live in `~/hub/o-plans/` (`README.md` is the index). Read it before planning work; add or update items there. Do not add a TODO.md or docs/plans/ to this repo.

## Implementation

- o-lane owns lane records, tree relations, persistence and policy; ochazuke owns the CLI shell, catalog, agent-rights gate and UI. Use released module tags and `GOWORK=off`.
- Lifecycle states are `lanes.states` data. No transition rules. Owner wording changes only through `reword`. Pane relations share handlers on both nouns. Missing pane context records an unassociated lane.
- Defaults must work for any user: XDG data, no machine paths, no required external daemon. Export is optional and off by default.
- Store writes are locked, atomic and recoverable. Every mutation records its actor and supports undo. Live state is derived and never persisted.
- Tests and manual app runs use explicit `--data` and HOME/XDG fixtures under a task scratch directory. Never read or change the owner's database/config. Go checks run under nice with `GOFLAGS='-trimpath -p=2' GOMAXPROCS=3`.
- Keep docs/CODEMAP.md and the hand-written Unreleased changelog current. Plans stay in the family plan repository.
