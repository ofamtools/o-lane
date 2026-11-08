package main

import (
	"fmt"
	"time"

	"ofam.dev/ochazuke"
)

// Values of lanes.status-tone-scope.
const toneScopeRow = "row"

// toneScopes maps each accepted scope to whether it tones the whole row.
var toneScopes = map[string]bool{toneScopeRow: true, "cell": false}

// defaultStatuses give every status but planned its own tone (planned has
// none: the default colour). running and done use ochazuke's state tones; the
// others are the lane.* tones of statusTones. A user overrides any of it by
// editing lanes.statuses (another tone name) or the tone's row in themes.json.
const defaultStatuses = `[{"name":"planned","open":true},` +
	`{"name":"running","tone":"state.working","starts":true,"open":true,"description":"Someone is working on this now."},` +
	`{"name":"parked","tone":"lane.parked","open":true,"description":"Paused on purpose: nobody is working on it until someone picks it up again."},` +
	`{"name":"review","tone":"lane.review","open":true,"description":"The work is finished and waits for review before it merges."},` +
	`{"name":"merged","tone":"lane.merged","ends":true,"description":"The work is merged into main; it may still need a release or an install."},` +
	`{"name":"done","tone":"state.done","ends":true,"description":"Finished: shipped, delivered or answered."},` +
	`{"name":"cancelled","tone":"lane.cancelled","ends":true,"description":"Stopped without finishing; kept for the record."},` +
	`{"name":"archived","tone":"lane.archived","archive":true,"description":"Put away: kept with its history but hidden from the default views. Restore returns it to the status it had."}]`

// statusTones are the tone rows o-lane declares for the default statuses
// (Appearance.SetTone), laid under every themes.json entry so a theme still
// recolours them. Each is light then dark, chosen to read on both and to
// differ from the state tones (running blue, done green) and from each other.
var statusTones = map[string]ochazuke.ToneStyle{
	"lane.parked":    {Fg: ochazuke.SchemeColor{Light: "#C2570C", Dark: "#FF9E5E"}},
	"lane.review":    {Fg: ochazuke.SchemeColor{Light: "#6F42C1", Dark: "#B392F0"}},
	"lane.merged":    {Fg: ochazuke.SchemeColor{Light: "#00796B", Dark: "#4DD0C0"}, Bold: true},
	"lane.cancelled": {Fg: ochazuke.SchemeColor{Light: "#B0386A", Dark: "#F08DB4"}},
	"lane.archived":  {Fg: ochazuke.SchemeColor{Light: "muted", Dark: "muted"}, Dim: true},
}

const defaultLiveStatuses = `{"working":{"working":true,"name":"working","tone":"state.working","glyph":"●"},"needs_input":{"working":true,"name":"needs input","tone":"state.needs-input","glyph":"?"},"errored":{"name":"errored","tone":"state.errored","glyph":"✗"},"done":{"name":"done","tone":"state.done","glyph":"✓"},"idle":{"name":"idle","tone":"state.idle","glyph":"○"},"closed":{"name":"closed","tone":"state.exited","glyph":"×"},"missing":{"name":"missing","tone":"dim","glyph":"·"},"none":{"name":"shell"},"":{"name":"shell"}}`

const defaultStatusPresets = `{"start":"running","park":"parked","merge":"merged","finish":"done","cancel":"cancelled","reopen":"planned","archive":"archived"}`

func appSettings() []ochazuke.Setting {
	rows := []ochazuke.Setting{
		{Name: "lanes.refresh", Type: ochazuke.SettingDuration, Default: 5 * time.Second, Description: "Standalone page refresh interval; zero disables polling"},
		{Name: "lanes.status-presets", Type: ochazuke.SettingString, Default: defaultStatusPresets, Description: "JSON preset verb to lifecycle status map; targets must exist in lanes.statuses"},
		{Name: "lanes.link-kinds", Type: ochazuke.SettingList, Default: []string{"commit", "pr", "doc", "report", "question", "plan", "task", "url", "retry-of"}, Description: "Link kinds accepted by link/unlink"},
		{Name: "lanes.doc-link-kinds", Type: ochazuke.SettingList, Default: []string{"doc", "report"}, Description: "Link kinds used for the lane row's doc; newest matching link wins; empty disables doc selection"},
		{Name: "lanes.import-columns", Type: ochazuke.SettingString, Default: defaultImportColumns, Validate: func(v any) error { _, e := parseImportColumns(fmt.Sprint(v)); return e }, Description: "JSON markdown header to spec field map; case-insensitive, longest matching header prefix"},
		{Name: "lanes.import-results", Type: ochazuke.SettingString, Default: `{"merged":"merged","shipped":"merged","running":"running","parked":"parked","stopped":"cancelled","on hold":"cancelled","report only":"done"}`, Description: "Reserved for L6: imported result to status mapping"},

		{Name: "lanes.statuses", Type: ochazuke.SettingString, Validate: func(v any) error {
			text, ok := v.(string)
			if !ok {
				return fmt.Errorf("expected JSON lifecycle rows")
			}
			_, e := parseStatuses(text)
			return e
		}, Default: defaultStatuses, Description: "JSON lifecycle rows: name, tone, starts, ends, open, archive (the status that carries the archive role), description (shown under the focused lane; empty shows nothing)"},
		{Name: "lanes.status-tone-scope", Type: ochazuke.SettingString, Default: toneScopeRow, Validate: func(v any) error {
			if _, ok := toneScopes[fmt.Sprint(v)]; !ok {
				return fmt.Errorf("lanes.status-tone-scope: use row or cell")
			}
			return nil
		}, Description: "Where a lifecycle status's tone draws: row (every cell of the lane's row) or cell (the status cell only); lanes.status-tones false turns colour off"},
		{Name: "lanes.pane-roles", Type: ochazuke.SettingList, Default: []string{"implementer", "reviewer", "watcher", "coordinator"}, Description: "Pane roles"},
		{Name: "lanes.tag-statuses", Type: ochazuke.SettingString, Default: `{"LANE-DONE":"review"}`, Description: "JSON tag to lifecycle status map; unmapped tags append notes"},
		{Name: "lanes.plans-dir", Type: ochazuke.SettingString, Default: "", Description: "Optional plan directory; unset by default"},
		{Name: "lanes.export.path", Type: ochazuke.SettingString, Default: "", Description: "Optional export destination; unset disables export"},
		{Name: "lanes.export.columns", Type: ochazuke.SettingList, Default: []string{"wording", "estimate", "started", "finished", "result"}, Description: "Export columns"},
		{Name: "lanes.pane-open", Type: ochazuke.SettingList, Default: []string{"mtm", "pane", "switch", "--session", "{session}", "--window", "{window}"}, Description: "Pane focus argv template ({session}, {window}, {host}); the default is mtm's pane.switch"},
		{Name: "lanes.live-statuses", Type: ochazuke.SettingString, Default: defaultLiveStatuses, Validate: func(v any) error { _, e := parseLiveStatuses(fmt.Sprint(v)); return e }, Description: "JSON pane status to {name, tone, glyph} map, with working:true for active statuses; legacy string labels accepted"},
		{Name: "lanes.live-precedence", Type: ochazuke.SettingList, Default: []string{"needs input", "errored", "working", "done", "idle", "closed", "missing"}, Description: "Live status aggregation priority"},
		{Name: "lanes.live-roles", Type: ochazuke.SettingList, Default: []string{}, Description: "Roles counted in the aggregate; empty includes all; pane details always include every role"},
		{Name: "lanes.project-source", Type: ochazuke.SettingString, Default: projectSourceDefault(), Validate: func(v any) error {
			if x := fmt.Sprint(v); x != "mtm" && x != "none" && x != "" {
				return fmt.Errorf("lanes.project-source: use mtm or none")
			}
			return nil
		}, Description: "Where project completion looks: mtm (default when mtm is on PATH) or none; never required"},
		{Name: "lanes.project-filter", Type: ochazuke.SettingString, Default: "", Description: "Page shows only subtrees containing this project; empty shows all"},
		{Name: "lanes.status-filter", Type: ochazuke.SettingList, Default: []string{}, Description: "Page shows only lanes in these lifecycle statuses, with their ancestors as context; empty shows all"},
		{Name: "lanes.agent-harnesses", Type: ochazuke.SettingString, Default: defaultAgentHarnesses, Validate: func(v any) error { _, e := parseAgentHarnesses(fmt.Sprint(v)); return e }, Description: "JSON harness name to agent data row: transcripts (path templates with {agent}/{session}), time_field, idle, events (hook event to start/stop) and fields (hook payload field names)"},
		{Name: "lanes.agent-idle", Type: ochazuke.SettingDuration, Default: 3 * time.Minute, Description: "An associated agent with no transcript activity for this long is idle; zero never idles"},
		{Name: "backup.daily", Type: ochazuke.SettingBoolean, Default: true, Description: "Private daily backup before first mutation"},
		{Name: "backup.keep", Type: ochazuke.SettingInteger, Default: 14, Description: "Daily archives to retain"},
	}
	for _, row := range []struct {
		name  string
		value bool
		desc  string
	}{
		{"lanes.tree", true, "Show lane nesting; changes apply on the next page mount"}, {"lanes.status-tones", true, "Colour lifecycle statuses"}, {"lanes.detail", true, "Show lane detail sections; changes apply on the next page mount"},
		{"lanes.durations", true, "Show derived durations"}, {"lanes.hide-closed-roots", false, "Hide closed roots"}, {"lanes.pane-column", true, "Show pane column"}, {"lanes.history-in-detail", true, "Show history"}, {"lanes.live", true, "Read live pane status"}, {"lanes.agent-live", true, "Read live status of associated agents from lanes.agent-harnesses data"}, {"lanes.live-tones", true, "Colour live status"},
		{"lanes.group-by-project", true, "Group page roots by project, unassociated last"}, {"lanes.project-column", true, "Show the project column"}, {"lanes.name-tone", true, "Draw the lane name in the same tone as its status cell"},
		{"lanes.pane-one-lane", true, "A pane belongs to one lane: associating it with a lane removes it from the others, and only its latest lane shows its live status; false lets lanes share panes"},
		{"lanes.rollup-counts", true, "Show done/total beside a lane whose subtree is not all complete (complete: a status with ends in lanes.statuses)"},
		{"lanes.stale-flag", true, "Flag working lifecycle statuses with no working participant; requires live reads"},
		{"lanes.status-descriptions", true, "Show the focused lane's status description and reason in the detail; changes apply on the next page mount"},
	} {
		rows = append(rows, ochazuke.Setting{Name: row.name, Type: ochazuke.SettingBoolean, Default: row.value, Description: row.desc})
	}
	return append(rows, paneCommandSettings()...)
}
