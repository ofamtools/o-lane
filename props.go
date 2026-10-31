package main

import (
	"github.com/unixsuperhero/ochazuke"
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
	a.r.Define("lane").Props(props...).Store(ochazuke.Store{Kind: ochazuke.Collection,
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
			return ochazuke.ThingOf(q)
		},
	})
	for _, key := range []string{"lane.get", "lane.set"} {
		op := a.r.Operations[key]
		op.Agent = ochazuke.AgentAllow
		a.r.Operations[key] = op
	}
}
