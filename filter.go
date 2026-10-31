package main

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// State filters (lane list/tree/rows --state, project lanes --state and the
// lanes.state-filter setting) match a lane's own lifecycle state. In tree
// views they follow the same rule as ochazuke's prop filters on a tree: a
// matching lane keeps its ancestors visible as context, and a matching
// lane's children show only when they match too. Flat lists show matches
// only.

func stateParam(fallback string) ochazuke.Parameter {
	desc := "Only lanes in these lifecycle states (comma list, or a JSON array)"
	if fallback != "" {
		desc += "; " + fallback
	}
	// Free: a comma list is not one choice; checkStates validates each name.
	return ochazuke.Parameter{Type: "string", Description: desc, Source: "lane.states", Free: true}
}

// stateList reads a comma list, a JSON array from --json/MCP, or a string
// slice. Blank entries are dropped; order and duplicates do not matter.
func stateList(v any) []string {
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

// checkStates refuses names that are not configured lifecycle states, so a
// typo never reads as "nothing matched".
func (s *Store) checkStates(states []string) error {
	known := s.states()
	for _, name := range states {
		if _, ok := known[name]; !ok {
			names := []string{}
			for _, st := range s.stateRows() {
				names = append(names, st.Name)
			}
			return fmt.Errorf("unknown state %q; configured states: %s", name, strings.Join(names, ", "))
		}
	}
	return nil
}

// stateKept is the set of lanes a tree view shows under a state filter:
// matches plus their ancestors. nil means no filter (everything shows).
func stateKept(lanes []Lane, states []string) map[string]bool {
	if len(states) == 0 {
		return nil
	}
	parent := map[string]string{}
	for _, q := range lanes {
		parent[q.ID] = q.Parent
	}
	kept := map[string]bool{}
	for _, q := range lanes {
		if !slices.Contains(states, q.State) {
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
func laneFilterValues(data ochazuke.Thing, q Lane) {
	for _, name := range append([]string{"live_state", "started", "finished", "parent", "effective_project"}, editable...) {
		if name == "depends" {
			continue
		}
		if v, ok := data[name]; !ok || v == nil {
			data[name] = ""
		}
	}
	data["status"] = q.State
	data["pane_count"] = len(q.Panes)
}
