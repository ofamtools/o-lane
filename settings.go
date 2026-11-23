package main

const Version = "2.0.0-next"

const Namespace = "lane"

// Values of lanes.status-tone-scope.
const toneScopeRow = "row"

// toneScopes maps each accepted scope to whether it tones the whole row.
var toneScopes = map[string]bool{toneScopeRow: true, "cell": false}

const defaultStatuses = `[{"name":"planned","open":true},` +
	`{"name":"running","tone":"state.working","starts":true,"open":true,"description":"Someone is working on this now."},` +
	`{"name":"parked","tone":"lane.parked","open":true,"description":"Paused on purpose: nobody is working on it until someone picks it up again."},` +
	`{"name":"review","tone":"lane.review","open":true,"description":"The work is finished and waits for review before it merges."},` +
	`{"name":"merged","tone":"lane.merged","ends":true,"description":"The work is merged into main; it may still need a release or an install."},` +
	`{"name":"done","tone":"state.done","ends":true,"description":"Finished: shipped, delivered or answered."},` +
	`{"name":"cancelled","tone":"lane.cancelled","ends":true,"description":"Stopped without finishing; kept for the record."},` +
	`{"name":"archived","tone":"lane.archived","archive":true,"description":"Put away: kept with its history but hidden from the default views. Restore returns it to the status it had."}]`

const defaultLiveStatuses = `{"working":{"working":true,"name":"working","tone":"state.working","glyph":"●"},"needs_input":{"working":true,"name":"needs input","tone":"state.needs-input","glyph":"?"},"errored":{"name":"errored","tone":"state.errored","glyph":"✗"},"done":{"name":"done","tone":"state.done","glyph":"✓"},"idle":{"name":"idle","tone":"state.idle","glyph":"○"},"closed":{"name":"closed","tone":"state.exited","glyph":"×"},"missing":{"name":"missing","tone":"dim","glyph":"·"},"none":{"name":"shell"},"":{"name":"shell"}}`

const defaultStatusPresets = `{"start":"running","park":"parked","merge":"merged","finish":"done","cancel":"cancelled","reopen":"planned","archive":"archived"}`

// defaultSettings are the recognized settings.json keys and their defaults.
var defaultSettings = map[string]any{
	"lanes.statuses":         defaultStatuses,
	"lanes.status-presets":   defaultStatusPresets,
	"lanes.restore-presets":  []any{"reopen"},
	"lanes.import-columns":   defaultImportColumns,
	"lanes.plans-dir":        "",
	"lanes.hide-archived":    true,
	"lanes.rollup-counts":    true,
	"lanes.pane-one-lane":    true,
	"backup.daily":           true,
	"backup.keep":            14,
	"retention.backups.days": 14,
	"history.keep":           200,
	"matchMode":              "substring",
	"listRatio":              0.55,
	"stackWidth":             100,
}
