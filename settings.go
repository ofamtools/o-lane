package main

import (
	"fmt"
	"github.com/unixsuperhero/ochazuke"
)

const defaultStates = `[{"name":"planned","tone":"dim","open":true},{"name":"running","tone":"state.working","starts":true,"open":true},{"name":"parked","tone":"dim","open":true},{"name":"review","tone":"accent","open":true},{"name":"merged","tone":"state.done","ends":true},{"name":"done","tone":"state.done","ends":true},{"name":"cancelled","tone":"dim","ends":true}]`

const defaultStatePresets = `{"start":"running","park":"parked","review":"review","merge":"merged","finish":"done","cancel":"cancelled","reopen":"planned"}`

func appSettings() []ochazuke.Setting {
	rows := []ochazuke.Setting{
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
		}, Default: defaultStates, Description: "JSON lifecycle rows: name, tone, starts, ends, open"},
		{Name: "lanes.pane-roles", Type: ochazuke.SettingList, Default: []string{"implementer", "reviewer", "watcher", "coordinator"}, Description: "Pane roles"},
		{Name: "lanes.tag-states", Type: ochazuke.SettingString, Default: `{"LANE-DONE":"review"}`, Description: "JSON tag to lifecycle state map; unmapped tags append notes"},
		{Name: "lanes.plans-dir", Type: ochazuke.SettingString, Default: "", Description: "Optional plan directory; unset by default"},
		{Name: "lanes.export.path", Type: ochazuke.SettingString, Default: "", Description: "Optional export destination; unset disables export"},
		{Name: "lanes.export.columns", Type: ochazuke.SettingList, Default: []string{"wording", "estimate", "started", "finished", "result"}, Description: "Export columns"},
		{Name: "lanes.pane-open", Type: ochazuke.SettingList, Default: []string{"tuios", "focus-window", "-s", "{session}", "{window}"}, Description: "Pane focus argv template"},
		{Name: "lanes.live-states", Type: ochazuke.SettingString, Default: `{"working":"working","needs_input":"needs input","errored":"errored","done":"done","idle":"idle","closed":"closed"}`, Description: "JSON pane state to live label map"},
		{Name: "lanes.live-precedence", Type: ochazuke.SettingList, Default: []string{"needs input", "errored", "working", "done", "idle", "closed"}, Description: "Live state aggregation priority"},
		{Name: "lanes.live-roles", Type: ochazuke.SettingList, Default: []string{}, Description: "Included live roles; empty includes all"},
		{Name: "backup.daily", Type: ochazuke.SettingBoolean, Default: true, Description: "Private daily backup before first mutation"},
		{Name: "backup.keep", Type: ochazuke.SettingInteger, Default: 14, Description: "Daily archives to retain"},
	}
	for _, row := range []struct {
		name  string
		value bool
		desc  string
	}{
		{"lanes.durations", true, "Show derived durations"}, {"lanes.hide-closed-roots", false, "Hide closed roots"}, {"lanes.pane-column", true, "Show pane column"}, {"lanes.history-in-detail", true, "Show history"}, {"lanes.live", true, "Read live pane state"}, {"lanes.live-tones", true, "Colour live state"},
	} {
		rows = append(rows, ochazuke.Setting{Name: row.name, Type: ochazuke.SettingBoolean, Default: row.value, Description: row.desc})
	}
	return rows
}
