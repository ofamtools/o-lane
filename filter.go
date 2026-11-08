package main

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// Status filters (lane list/tree/rows --status, project lanes --status and the
// lanes.status-filter setting) match a lane's own lifecycle status. In tree
// views they follow the same rule as ochazuke's prop filters on a tree: a
// matching lane keeps its ancestors visible as context, and a matching
// lane's children show only when they match too. Flat lists show matches
// only.

func statusParam(fallback string) ochazuke.Parameter {
	desc := "Only lanes in these lifecycle statuses (comma list, or a JSON array)"
	if fallback != "" {
		desc += "; " + fallback
	}
	// Free: a comma list is not one choice; checkStatuses validates each name.
	return ochazuke.Parameter{Type: "string", Description: desc, Free: true}
}

// statusList reads a comma list, a JSON array from --json/MCP, or a string
// slice. Blank entries are dropped; order and duplicates do not matter.
func statusList(v any) []string {
	var raw []string
	switch x := v.(type) {
	case string:
		raw = strings.Split(x, ",")
	case []string:
		raw = x
	case []any:
		for _, item := range x {
			raw = append(raw, strings.Split(fmt.Sprint(item), ",")...)
		}
	}
	out := []string{}
	for _, word := range raw {
		if word = strings.TrimSpace(word); word != "" && !slices.Contains(out, word) {
			out = append(out, word)
		}
	}
	return out
}

// checkStatuses refuses names that are not configured lifecycle statuses, so a
// typo never reads as "nothing matched".
func (s *Store) checkStatuses(statuses []string) error {
	known := s.statuses()
	for _, name := range statuses {
		if _, ok := known[name]; !ok {
			names := []string{}
			for _, st := range s.statusRows() {
				names = append(names, st.Name)
			}
			return fmt.Errorf("unknown status %q; configured statuses: %s", name, strings.Join(names, ", "))
		}
	}
	return nil
}

// statusKept is the set of lanes a tree view shows under a status filter:
// matches plus their ancestors. nil means no filter (everything shows).
func statusKept(lanes []Lane, statuses []string) map[string]bool {
	if len(statuses) == 0 {
		return nil
	}
	parent := map[string]string{}
	for _, q := range lanes {
		parent[q.ID] = q.Parent
	}
	kept := map[string]bool{}
	for _, q := range lanes {
		if !slices.Contains(statuses, q.Status) {
			continue
		}
		for id := q.ID; id != "" && !kept[id]; id = parent[id] {
			kept[id] = true
		}
	}
	return kept
}

// laneFilterValues makes every filter prop present in a row's data, so
// = and != conditions compare an empty value rather than failing on a
// missing key (Spec fields omit empty values in JSON).
func laneFilterValues(data ochazuke.Thing, q Lane, archived bool) {
	for _, name := range append([]string{"live_status", "started", "finished", "parent", "effective_project"}, editable...) {
		if name == "depends" {
			continue
		}
		if v, ok := data[name]; !ok || v == nil {
			data[name] = ""
		}
	}
	data["status"] = q.Status
	data[archiveProp] = archived
	data["pane_count"] = len(q.Panes)
}
