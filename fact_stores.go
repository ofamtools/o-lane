package main

import (
	"context"
	"fmt"
	"strings"

	"ofam.dev/ochazuke"
)

// The project and agent nouns are thing nouns (DESIGN-001) with read-only
// (facts) stores: a project is a reference recorded on lanes (or listed by
// lanes.project-source), an agent is a participant the agent hook
// recorded, with its derived live state. Nothing here is stored by o-lane
// as a record of its own; project.associate and agent.associate relate
// them to lanes, and list and get are the standard verbs, over the same
// rows project list and agent list answer.

func factProp(name, kind, desc string) ochazuke.PropSpec {
	return ochazuke.PropSpec{Name: name, ReadOnly: true, Parameter: ochazuke.Parameter{Type: kind, Description: desc}}
}

func projectProps() []ochazuke.PropSpec {
	return []ochazuke.PropSpec{
		{Name: "ref", Key: true, Parameter: ochazuke.Parameter{Type: "string", Description: "The project reference lanes carry"}},
		{Name: "name", Role: ochazuke.RoleLabel, ReadOnly: true, Fallback: "ref", Parameter: ochazuke.Parameter{Type: "string", Description: "The project's name, when its source gives one (else the reference)"}},
		factProp("source", "string", "Where the reference comes from: lanes, or the lanes.project-source"),
		factProp("lane_count", ochazuke.SettingInteger, "Lanes whose effective project it is"),
	}
}

func agentProps() []ochazuke.PropSpec {
	return []ochazuke.PropSpec{
		{Name: "agent_id", Key: true, Parameter: ochazuke.Parameter{Type: "string", Description: "The harness sub-agent id"}},
		factProp("harness", "string", "The harness"),
		factProp("session", "string", "The harness session the agent runs in"),
		factProp("agent_type", "string", "The agent kind"),
		factProp("state", "string", "working, idle, done or missing, from lanes.agent-harnesses data"),
		factProp("activity", "string", "When the agent last did anything"),
		factProp("transcript", "string", "The agent's transcript file"),
		factProp("error", "string", "Why the state could not be read"),
	}
}

func (a *App) projectStore() ochazuke.Store {
	things := func() ([]ochazuke.Thing, error) {
		if e := a.configure(); e != nil {
			return nil, e
		}
		rows, e := a.projectRows(context.Background())
		if e != nil {
			return nil, e
		}
		out := make([]ochazuke.Thing, 0, len(rows))
		for _, r := range rows {
			out = append(out, ochazuke.Thing{"ref": r.Ref, "name": firstOf(r.Name, r.Ref), "source": r.Source, "lane_count": r.Lanes})
		}
		return out, nil
	}
	return ochazuke.Store{Kind: ochazuke.Collection, Facts: true,
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) { return things() },
		Get: func(_ ochazuke.Ctx, ref string) (ochazuke.Thing, error) {
			all, e := things()
			if e != nil {
				return nil, e
			}
			for _, t := range all {
				if t["ref"] == ref {
					return t, nil
				}
			}
			return nil, fmt.Errorf("no project %q (project list prints them)", ref)
		},
	}
}

func (a *App) agentStore() ochazuke.Store {
	things := func() ([]ochazuke.Thing, error) {
		if e := a.configure(); e != nil {
			return nil, e
		}
		rows, e := a.agentRows()
		if e != nil {
			return nil, e
		}
		out := make([]ochazuke.Thing, 0, len(rows))
		for _, r := range rows {
			t, e := ochazuke.ThingOf(r)
			if e != nil {
				return nil, e
			}
			if r.Activity != nil {
				t["activity"] = r.Activity.Format("2006-01-02T15:04:05Z07:00")
			}
			out = append(out, t)
		}
		return out, nil
	}
	return ochazuke.Store{Kind: ochazuke.Collection, Facts: true,
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) { return things() },
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			all, e := things()
			if e != nil {
				return nil, e
			}
			for _, t := range all {
				if t["agent_id"] == strings.TrimSpace(id) {
					return t, nil
				}
			}
			return nil, fmt.Errorf("no agent %q (agent list prints them)", id)
		},
	}
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
