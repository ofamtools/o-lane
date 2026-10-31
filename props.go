package main

import (
	"ofam.dev/ochazuke"
	"slices"
)

// Reuse the framework's standard typed set/get verbs, including multi-property
// writes and property shorthand. Domain-only operations keep explicit verbs.
func (a *App) declareProps() {
	props := []ochazuke.PropSpec{{Name: "id", Parameter: ochazuke.Parameter{Type: "string"}, Key: true}}
	for _, name := range editable {
		kind := "string"
		if name == "depends" {
			kind = ochazuke.SettingList
		}
		props = append(props, ochazuke.PropSpec{Name: name, Parameter: ochazuke.Parameter{Type: kind}})
	}
	props = append(props, filterProps()...)
	a.r.Define("lane").Props(props...).Filters(openFilter(a.store.stateRows()))
	a.r.Define("lane").Store(ochazuke.Store{Kind: ochazuke.Collection,
		Get: func(c ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			q, e := a.store.Resolve(id)
			if e != nil {
				return nil, e
			}
			return ochazuke.ThingOf(q)
		},
		Patch: func(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
			if e := a.configure(); e != nil {
				return nil, e
			}
			a.bindActor(c)
			q, e := a.store.mutate("set", id, func(q *Lane, _ *[]Lane) error {
				keys := make([]string, 0, len(changes))
				for key := range changes {
					keys = append(keys, key)
				}
				slices.Sort(keys)
				for _, key := range keys {
					if e := setSpec(&q.Spec, key, changes[key]); e != nil {
						return e
					}
				}
				a.store.entry(q, "edit", "", "", "set", nil, nil)
				return nil
			})
			if e != nil {
				return nil, e
			}
			if a.changed != nil {
				a.changed()
			}
			return ochazuke.ThingOf(q)
		},
	})
	for _, key := range []string{"lane.get", "lane.set"} {
		op := a.r.Operations[key]
		op.Agent = ochazuke.AgentAllow
		a.r.Operations[key] = op
	}
}

// filterProps are the read-only props a lane row carries for prop filters
// (z / on the page, saved filters in list.filters). Domain verbs own their
// writes (state, rename, move, associate), so set never writes these.
// Values come from the page row's data (laneFilterValues).
func filterProps() []ochazuke.PropSpec {
	out := []ochazuke.PropSpec{}
	for _, row := range []struct{ name, kind, desc string }{
		{"name", "string", "Lane name"},
		{"parent", "string", "Parent lane ID; empty for a root"},
		// "state" is the lane.state verb, and a word after a noun is a verb
		// or a prop, never both; status carries the lifecycle state.
		{"status", "string", "Lifecycle state (the state field; lane.state records it)"},
		{"rolled_state", "string", "Subtree rollup of lifecycle states"},
		{"live_state", "string", "Aggregate live pane state; empty when live reading is off"},
		{"effective_project", "string", "The lane's project or the nearest ancestor's"},
		{"created", "string", "Creation time (RFC 3339)"},
		{"updated", "string", "Last change (RFC 3339)"},
		{"started", "string", "First start (RFC 3339); empty when never started"},
		{"finished", "string", "Finish time (RFC 3339); empty while open"},
		{"pane_count", ochazuke.SettingInteger, "Associated panes"},
	} {
		out = append(out, ochazuke.PropSpec{Name: row.name, ReadOnly: true, Parameter: ochazuke.Parameter{Type: row.kind, Description: row.desc}})
	}
	return out
}

// openConditions is the default "open" saved filter: status differs from
// every configured closed state (states without open), ANDed. The rows are
// the lanes.states in force when the noun is declared; a user list.filters
// row named "open" replaces it.
func openFilter(states []State) ochazuke.SavedFilter {
	return ochazuke.SavedFilter{Name: "open", Noun: "lane", Conditions: openConditions(states)}
}

// refreshOpenFilter keeps the declared "open" filter in step with the
// configured lanes.states (configure runs before every request).
func (a *App) refreshOpenFilter() {
	n := a.r.Noun("lane")
	if n == nil {
		return
	}
	for i, f := range n.Filters {
		if f.Name == "open" {
			n.Filters[i] = openFilter(a.store.stateRows())
		}
	}
}

func openConditions(states []State) []ochazuke.PropFilter {
	out := []ochazuke.PropFilter{}
	for _, st := range states {
		if !st.Open {
			out = append(out, ochazuke.PropFilter{Prop: "status", Op: "!=", Value: st.Name})
		}
	}
	return out
}
