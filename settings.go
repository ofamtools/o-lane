package main

import (
	"fmt"
	"time"

	"ofam.dev/ochazuke"
)

const defaultStates = `[{"name":"planned","tone":"dim","open":true},` +
	`{"name":"running","tone":"state.working","starts":true,"open":true,"description":"Someone is working on this now."},` +
	`{"name":"parked","tone":"dim","open":true,"description":"Paused on purpose: nobody is working on it until someone picks it up again."},` +
	`{"name":"review","tone":"accent","open":true,"description":"The work is finished and waits for review before it merges."},` +
	`{"name":"merged","tone":"state.done","ends":true,"description":"The work is merged into main; it may still need a release or an install."},` +
	`{"name":"done","tone":"state.done","ends":true,"description":"Finished: shipped, delivered or answered."},` +
	`{"name":"cancelled","tone":"dim","ends":true,"description":"Stopped without finishing; kept for the record."}]`

const defaultLiveStates = `{"working":{"name":"working","tone":"state.working","glyph":"●"},"needs_input":{"name":"needs input","tone":"state.needs-input","glyph":"?"},"errored":{"name":"errored","tone":"state.errored","glyph":"✗"},"done":{"name":"done","tone":"state.done","glyph":"✓"},"idle":{"name":"idle","tone":"state.idle","glyph":"○"},"closed":{"name":"closed","tone":"state.exited","glyph":"×"},"missing":{"name":"missing","tone":"dim","glyph":"·"},"none":{"name":"shell"},"":{"name":"shell"}}`

const defaultStatePresets = `{"start":"running","park":"parked","review":"review","merge":"merged","finish":"done","cancel":"cancelled","reopen":"planned"}`

func appSettings() []ochazuke.Setting {
	rows := []ochazuke.Setting{
		{Name: "lanes.refresh", Type: ochazuke.SettingDuration, Default: 5 * time.Second, Description: "Standalone page refresh interval; zero disables polling"},
		{Name: "lanes.state-presets", Type: ochazuke.SettingString, Default: defaultStatePresets, Description: "JSON preset verb to lifecycle state map; targets must exist in lanes.states"},
		{Name: "lanes.link-kinds", Type: ochazuke.SettingList, Default: []string{"commit", "pr", "report", "question", "plan", "task", "url", "retry-of"}, Description: "Link kinds accepted by link/unlink"},
		{Name: "lanes.import-columns", Type: ochazuke.SettingString, Default: defaultImportColumns, Validate: func(v any) error { _, e := parseImportColumns(fmt.Sprint(v)); return e }, Description: "JSON markdown header to spec field map; case-insensitive, longest matching header prefix"},
		{Name: "lanes.import-results", Type: ochazuke.SettingString, Default: `{"merged":"merged","shipped":"merged","running":"running","parked":"parked","stopped":"cancelled","on hold":"cancelled","report only":"done"}`, Description: "Reserved for L6: imported result to state mapping"},

		{Name: "lanes.states", Type: ochazuke.SettingString, Validate: func(v any) error {
			text, ok := v.(string)
			if !ok {
				return fmt.Errorf("expected JSON lifecycle rows")
			}
			_, e := parseStates(text)
			return e
		}, Default: defaultStates, Description: "JSON lifecycle rows: name, tone, starts, ends, open, description (shown under the focused lane; empty shows nothing)"},
		{Name: "lanes.pane-roles", Type: ochazuke.SettingList, Default: []string{"implementer", "reviewer", "watcher", "coordinator"}, Description: "Pane roles"},
		{Name: "lanes.tag-states", Type: ochazuke.SettingString, Default: `{"LANE-DONE":"review"}`, Description: "JSON tag to lifecycle state map; unmapped tags append notes"},
		{Name: "lanes.plans-dir", Type: ochazuke.SettingString, Default: "", Description: "Optional plan directory; unset by default"},
		{Name: "lanes.export.path", Type: ochazuke.SettingString, Default: "", Description: "Optional export destination; unset disables export"},
		{Name: "lanes.export.columns", Type: ochazuke.SettingList, Default: []string{"wording", "estimate", "started", "finished", "result"}, Description: "Export columns"},
		{Name: "lanes.pane-open", Type: ochazuke.SettingList, Default: []string{"tuios", "focus-window", "-s", "{session}", "{window}"}, Description: "Pane focus argv template"},
		{Name: "lanes.live-states", Type: ochazuke.SettingString, Default: defaultLiveStates, Validate: func(v any) error { _, e := parseLiveStates(fmt.Sprint(v)); return e }, Description: "JSON pane state to {name, tone, glyph} map; legacy string labels accepted"},
		{Name: "lanes.live-precedence", Type: ochazuke.SettingList, Default: []string{"needs input", "errored", "working", "done", "idle", "closed", "missing"}, Description: "Live state aggregation priority"},
		{Name: "lanes.live-roles", Type: ochazuke.SettingList, Default: []string{}, Description: "Roles counted in the aggregate; empty includes all; pane details always include every role"},
		{Name: "lanes.project-source", Type: ochazuke.SettingString, Default: projectSourceDefault(), Validate: func(v any) error {
			if x := fmt.Sprint(v); x != "mtm" && x != "none" && x != "" {
				return fmt.Errorf("lanes.project-source: use mtm or none")
			}
			return nil
		}, Description: "Where project completion looks: mtm (default when mtm is on PATH) or none; never required"},
		{Name: "lanes.project-filter", Type: ochazuke.SettingString, Default: "", Description: "Page shows only subtrees containing this project; empty shows all"},
		{Name: "lanes.state-filter", Type: ochazuke.SettingList, Default: []string{}, Description: "Page shows only lanes in these lifecycle states, with their ancestors as context; empty shows all"},
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
		{"lanes.tree", true, "Show lane nesting; changes apply on the next page mount"}, {"lanes.state-tones", true, "Colour lifecycle states"}, {"lanes.detail", true, "Show lane detail sections; changes apply on the next page mount"},
		{"lanes.durations", true, "Show derived durations"}, {"lanes.hide-closed-roots", false, "Hide closed roots"}, {"lanes.pane-column", true, "Show pane column"}, {"lanes.history-in-detail", true, "Show history"}, {"lanes.live", true, "Read live pane state"}, {"lanes.agent-live", true, "Read live state of associated agents from lanes.agent-harnesses data"}, {"lanes.live-tones", true, "Colour live state"},
		{"lanes.group-by-project", true, "Group page roots by project, unassociated last"}, {"lanes.project-column", true, "Show the project column"}, {"lanes.name-tone", true, "Draw the lane name in the same tone as its state cell"},
		{"lanes.pane-one-lane", true, "A pane belongs to one lane: associating it with a lane removes it from the others, and only its latest lane shows its live status; false lets lanes share panes"},
		{"lanes.state-descriptions", true, "Show the focused lane's state description and reason in the detail; changes apply on the next page mount"},
	} {
		rows = append(rows, ochazuke.Setting{Name: row.name, Type: ochazuke.SettingBoolean, Default: row.value, Description: row.desc})
	}
	return rows
}
