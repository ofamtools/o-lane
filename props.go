package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// Reuse the framework's standard typed set/get verbs, including multi-property
// writes and property shorthand. Domain-only operations keep explicit verbs.
// archiveProp is the lane prop with the archive role; its value is whether
// the lane's status row carries archive:true.
const archiveProp = "archived"

// statusProp is the lane prop that holds the lifecycle status.
const statusProp = "status"

// reasonProp is the lane prop that holds why the lane is in its status: the
// note of the latest status change. Written in the same set as a status (or
// archived) change it is that change's note; written alone it appends a note.
const reasonProp = "reason"

func (a *App) declareProps() {
	props := []ochazuke.PropSpec{{Name: "id", Parameter: ochazuke.Parameter{Type: "string"}, Key: true}}
	for _, name := range editable {
		param := ochazuke.Parameter{Type: "string"}
		if name == "depends" {
			param.Type = ochazuke.SettingList
		}
		props = append(props, ochazuke.PropSpec{Name: name, Parameter: withRef(name, param)})
	}
	props = append(props, filterProps()...)
	// Writable archive-role prop: ochazuke derives archive/restore, the
	// archived saved filter and collection.hide-archived from it; a write moves the
	// lane to or from the lanes.statuses row with archive:true (setArchived).
	props = append(props, ochazuke.PropSpec{Name: archiveProp, Role: ochazuke.RoleArchive, Parameter: ochazuke.Parameter{Type: ochazuke.SettingBoolean, Description: "In the archive status (the lanes.statuses row with archive:true); writing it archives or restores the lane"}})
	// The lifecycle status: writing it records a status change (lane status
	// done, lane set status=done); its Enum is lanes.statuses, kept in step
	// by refreshStatusEnum.
	props = append(props, ochazuke.PropSpec{Name: statusProp, Parameter: ochazuke.Parameter{Type: ochazuke.SettingString, Enum: statusNames(a.store.statusRows()), Description: "Lifecycle status (a lanes.statuses row); writing it records a status change in the lane's history"}})
	props = append(props, ochazuke.PropSpec{Name: reasonProp, Parameter: ochazuke.Parameter{Type: ochazuke.SettingString, Description: "Why the lane is in its status (the latest status change's note); set it with the status in one write: lane set ID status=review reason=\"branch X\""}})
	a.r.Define("lane").Props(props...).Filters(openFilter(a.store.statusRows()), ochazuke.SavedFilter{Name: "stale", Noun: "lane", Conditions: []ochazuke.PropFilter{{Prop: "stale", Value: true}}})
	a.r.Define("lane").Store(ochazuke.Store{Kind: ochazuke.KindCollection,
		Get: func(c ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			if e := a.configure(); e != nil {
				return nil, e
			}
			q, e := a.store.Resolve(id)
			if e != nil {
				return nil, e
			}
			t, e := ochazuke.ThingOf(q)
			if e == nil {
				t[archiveProp] = a.store.statuses()[q.Status].Archive
				t[reasonProp] = statusReason(q.History)
			}
			return t, e
		},
		Patch: func(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
			if e := a.configure(); e != nil {
				return nil, e
			}
			a.bindActor(c)
			q, e := a.store.mutate("set", id, func(q *Lane, _ *[]Lane) error { return a.patchLane(q, changes) })
			if e != nil {
				return nil, e
			}
			if a.changed != nil {
				a.changed()
			}
			t, e := ochazuke.ThingOf(q)
			if e == nil {
				t[archiveProp] = a.store.statuses()[q.Status].Archive
				t[reasonProp] = statusReason(q.History)
			}
			return t, e
		},
	})
	for _, key := range []string{"lane.get", "lane.set"} {
		if op, ok := a.r.Operations.Find(ochazuke.Key(key)); ok {
			op.Agent = ochazuke.AgentAllow
			a.r.Operations.Set(op)
		}
	}
}

// refProps are the lane props whose stored value is the key of a thing of a
// noun, so the relation graph can follow them: one declaration shape (Noun,
// plus HostNoun for a noun of the program that mounts the catalog), one table,
// no per-prop branch. A prop is listed only when its stored value is the
// target noun's key: parent holds a lane id; task holds an mtm task id.
// Not listed, with why: project (stored as a name, an id
// or kind:id; mtm keys by id), repo, branch and worktree (names and paths; the
// git nouns key by "repo/PATH"), and the pane association (a list of
// objects, not a prop of one key).
var refProps = map[string]ochazuke.Parameter{
	"parent": {Noun: "lane"},
	"task":   {Noun: "task", HostNoun: "task"},
	// project holds the id of the host's project once resolved (migrated by
	// project resolve; written as an id from then on), so the host's project
	// is the target; a name that matches no project stays as typed.
	"project": {Noun: "project", HostNoun: "project"},
}

// withRef adds a prop's reference declaration (refProps) to its parameter. A
// reference is Free: a key that names nothing (a dead task, a deleted
// parent) is kept as written, never refused.
func withRef(name string, p ochazuke.Parameter) ochazuke.Parameter {
	if ref, ok := refProps[name]; ok {
		p.Noun, p.HostNoun, p.Free = ref.Noun, ref.HostNoun, true
	}
	return p
}

// filterProps are the read-only props a lane row carries for prop filters
// (z / on the page, saved filters in collection.filters). Domain verbs own their
// writes (status, rename, move, associate), so set never writes these.
// Values come from the page row's data (laneFilterValues).
func filterProps() []ochazuke.PropSpec {
	out := []ochazuke.PropSpec{}
	for _, row := range []struct{ name, kind, desc string }{
		{"name", "string", "Lane name"},
		{"parent", "string", "Parent lane ID; empty for a root"},
		{"rolled_status", "string", "Subtree rollup of lifecycle statuses"},
		{"live_status", "string", "Aggregate live pane status; empty when live reading is off"},
		{"effective_project", "string", "The lane's project or the nearest ancestor's"},
		{"created", "string", "Creation time (RFC 3339)"},
		{"updated", "string", "Last change (RFC 3339)"},
		{"started", "string", "First start (RFC 3339); empty when never started"},
		{"finished", "string", "Finish time (RFC 3339); empty while open"},
		{"pane_count", ochazuke.SettingInteger, "Associated panes"},
		{"stale", ochazuke.SettingBoolean, "Working lifecycle status with no working participant; false when live reads or stale flags are off"},
	} {
		out = append(out, ochazuke.PropSpec{Name: row.name, ReadOnly: true, Parameter: withRef(row.name, ochazuke.Parameter{Type: row.kind, Description: row.desc})})
	}
	return out
}

// openConditions is the default "open" saved filter: status differs from
// every configured closed status (statuses without open), ANDed. The rows are
// the lanes.statuses in force when the noun is declared; a user collection.filters
// row named "open" replaces it.
func openFilter(statuses []Status) ochazuke.SavedFilter {
	return ochazuke.SavedFilter{Name: "open", Noun: "lane", Conditions: openConditions(statuses)}
}

// statusNames are the names of the configured status rows.
func statusNames(rows []Status) []string {
	out := make([]string, 0, len(rows))
	for _, st := range rows {
		out = append(out, st.Name)
	}
	return out
}

// statusFilterOps are the operations whose status parameter filters by
// lifecycle status; refreshStatusEnum offers them the configured names.
var statusFilterOps = []ochazuke.Key{"lane.choose-status", "lane.list", "lane.tree", "lane.rows", "lane." + pageRowsVerb, "project.lanes"}

// refreshStatusEnum keeps the status prop's Enum and the status filter
// parameters' choices in step with the configured lanes.statuses (configure
// runs before every request).
func (a *App) refreshStatusEnum() {
	names := statusNames(a.store.statusRows())
	if n := a.r.Noun("lane"); n != nil {
		for i := range n.Props {
			if n.Props[i].Name == statusProp {
				n.Props[i].Enum = slices.Clone(names)
			}
		}
	}
	for _, key := range statusFilterOps {
		op, ok := a.r.Operations.Find(key)
		if !ok {
			continue
		}
		if p, ok := op.Parameters["status"]; ok {
			p.Enum = slices.Clone(names)
			op.Parameters = maps.Clone(op.Parameters)
			op.Parameters["status"] = p
			a.r.Operations.Set(op)
		}
	}
}

// refreshOpenFilter keeps the declared "open" filter in step with the
// configured lanes.statuses (configure runs before every request).
func (a *App) refreshOpenFilter() {
	n := a.r.Noun("lane")
	if n == nil {
		return
	}
	for i, f := range n.Filters {
		if f.Name == "open" {
			n.Filters[i] = openFilter(a.store.statusRows())
		}
	}
}

func openConditions(statuses []Status) []ochazuke.PropFilter {
	out := []ochazuke.PropFilter{}
	for _, st := range statuses {
		if !st.Open {
			out = append(out, ochazuke.PropFilter{Prop: "status", Op: "!=", Value: st.Name})
		}
	}
	return out
}

// archiveFlag reads the archive prop's value from a typed or text write.
func archiveFlag(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "yes", "1", "on":
			return true, nil
		case "false", "no", "0", "off", "":
			return false, nil
		}
	}
	return false, fmt.Errorf("%s: expected true or false", archiveProp)
}

// patchLane applies one set to a lane (Store.patch).
func (a *App) patchLane(q *Lane, changes ochazuke.Thing) error { return a.store.patch(q, changes) }

// lifecycleProps are the lane props a write records as a lifecycle change
// (status, archived) or as that change's note (reason). lane set and lane
// define write them through the same patch.
var lifecycleProps = []string{statusProp, archiveProp, reasonProp}

// patch applies one set to a lane: the props in key order, with the
// reason as the note of the status (or archived) change in the same write.
func (s *Store) patch(q *Lane, changes map[string]any) error {
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	edited := false
	reason, hasReason := "", false
	if v, ok := changes[reasonProp]; ok && v != nil {
		reason, hasReason = fmt.Sprint(v), true
	}
	for _, key := range keys {
		if key == reasonProp {
			continue
		}
		if key == statusProp {
			if e := s.status(q, fmt.Sprint(changes[key]), reason); e != nil {
				return e
			}
			hasReason = false
			continue
		}
		if key == archiveProp {
			on, e := archiveFlag(changes[key])
			if e != nil {
				return e
			}
			if e = s.setArchived(q, on, reason); e != nil {
				return e
			}
			hasReason = false
			continue
		}
		if e := setSpec(&q.Spec, key, changes[key]); e != nil {
			return e
		}
		edited = true
	}
	if hasReason && strings.TrimSpace(reason) != "" {
		// A reason written alone is the current status's reason (statusReason).
		s.entry(q, reasonProp, "", "", reason, nil, nil)
	}
	if edited {
		s.entry(q, "edit", "", "", "set", nil, nil)
	}
	return nil
}
