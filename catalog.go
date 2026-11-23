package main

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

// The catalog protocol (matomeru's ext package): a host runs
//
//	o-lane catalog          a Manifest: lane.lane & lane.plan schemas and commands
//	o-lane query [TEXT]     {"items": [record...], "total": n}, read-only
//
// and never writes to o-lane; what the owner changes on a record in the host
// is kept there as a shadow record.

const (
	laneKind = "lane.lane"
	planKind = "lane.plan"
)

// manifestDoc is `o-lane catalog`: the wire shape of ext.Manifest.
type manifestDoc struct {
	Name     string             `json:"name"`
	Version  string             `json:"version,omitempty"`
	Schemas  []schemaDoc        `json:"schemas,omitempty"`
	Commands []core.CommandSpec `json:"commands,omitempty"`
	Bindings []core.Binding     `json:"bindings,omitempty"`
}

// schemaDoc is the wire shape of data.Schema.
type schemaDoc struct {
	Kind           string           `json:"kind"`
	Title          string           `json:"title"`
	Plural         string           `json:"plural"`
	Fields         []core.ParamSpec `json:"fields"`
	DefaultColumns []string         `json:"defaultColumns,omitempty"`
	SummaryField   string           `json:"summaryField,omitempty"`
	UniqueFields   []string         `json:"uniqueFields,omitempty"`
}

// recordDoc is the wire shape of data.Record.
type recordDoc struct {
	Kind            string          `json:"kind"`
	ID              string          `json:"id"`
	Fields          map[string]any  `json:"fields"`
	ReadOnly        map[string]bool `json:"readOnly,omitempty"`
	ArchiveStatus   string          `json:"archiveStatus"`
	StarStatus      string          `json:"starStatus"`
	ChangeSetNumber int64           `json:"changeSetNumber"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

// queryResult is `o-lane query`.
type queryResult struct {
	Items []recordDoc `json:"items"`
	Total int         `json:"total"`
}

func laneFields() []core.ParamSpec {
	f := func(name string, t core.ParamType, desc string) core.ParamSpec {
		return core.ParamSpec{Name: name, Type: t, Description: desc}
	}
	return []core.ParamSpec{
		f("name", core.ParamString, "Lane name"),
		f("status", core.ParamString, "Lifecycle status"),
		f("rolled_status", core.ParamString, "Subtree rolled-up status"),
		f("parent", core.ParamString, "Parent lane ID (empty for a root)"),
		f("project", core.ParamString, "Effective project display name"),
		f("project_id", core.ParamString, "Effective project reference ID"),
		f("plan", core.ParamPath, "Source Markdown plan path"),
		f("repo", core.ParamString, "Repository name or path"),
		f("branch", core.ParamString, "Git branch"),
		f("estimate", core.ParamString, "Time estimate text"),
		f("wording", core.ParamString, "Verbatim owner wording"),
		f("scope", core.ParamString, "Lane scope"),
		f("done_when", core.ParamString, "Done criteria"),
		f("archived", core.ParamBool, "Whether the lane is in an archive status"),
		f("pane_count", core.ParamInt, "Associated pane/agent count"),
		f("children_done", core.ParamInt, "Complete descendant lanes"),
		f("children_total", core.ParamInt, "Total non-archived descendant lanes"),
		f("updated", core.ParamString, "Last update timestamp (RFC 3339)"),
	}
}

func planFields() []core.ParamSpec {
	f := func(name string, t core.ParamType, desc string) core.ParamSpec {
		return core.ParamSpec{Name: name, Type: t, Description: desc}
	}
	return []core.ParamSpec{
		f("name", core.ParamString, "Plan title"),
		f("path", core.ParamPath, "Plan Markdown file path"),
		f("lane_count", core.ParamInt, "Total lanes in this plan"),
		f("done_count", core.ParamInt, "Completed lanes in this plan"),
		f("open_count", core.ParamInt, "Open lanes in this plan"),
		f("projects", core.ParamString, "Associated projects"),
		f("updated", core.ParamString, "Most recent lane update (RFC 3339)"),
	}
}

func catalogCommands() []core.CommandSpec {
	laneRef := core.ParamSpec{Name: "id", Type: core.ParamRef, Required: true, RefKinds: []string{laneKind}, Description: "Lane ID, unique prefix, or name"}
	planRef := core.ParamSpec{Name: "plan", Type: core.ParamPath, Required: true, Description: "Plan file path or ID"}
	str := func(name string, req bool, desc string) core.ParamSpec {
		return core.ParamSpec{Name: name, Type: core.ParamString, Required: req, Description: desc}
	}
	cmd := func(id, cli, desc string, params ...core.ParamSpec) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc, Params: params, Tags: []string{Namespace}, Meta: map[string]any{"cli": "o-lane " + cli}}
	}
	return []core.CommandSpec{
		cmd(laneKind+".define", "lane define", "Define one or more lanes (from flags, JSON, or a Markdown plan)",
			str("name", false, "Lane name"),
			str("parent", false, "Parent lane ID or name"),
			str("project", false, "Project reference"),
			core.ParamSpec{Name: "plan", Type: core.ParamPath, Description: "Markdown plan file"}),
		cmd(laneKind+".list", "lane list --format json", "List lanes with optional status/project filters",
			str("status", false, "Filter by lifecycle status"),
			str("project", false, "Filter by effective project"),
			core.ParamSpec{Name: "archived", Type: core.ParamBool, Description: "Include archived lanes"}),
		cmd(laneKind+".show", "lane show --json", "Show full details, rollup, and history for a lane", laneRef),
		cmd(laneKind+".state", "lane state", "Set a lane's lifecycle status (any non-empty status accepted)",
			laneRef, str("status", true, "Target status"), str("note", false, "Optional reason/note")),
		cmd(laneKind+".note", "lane note", "Append a note to a lane's history",
			laneRef, str("text", true, "Note text")),
		cmd(laneKind+".associate", "lane associate", "Associate a project, pane, or agent with a lane",
			laneRef, str("project", false, "Project reference"), str("pane", false, "Pane window ID"), str("session", false, "Pane session")),
		cmd(laneKind+".archive", "lane archive", "Archive a lane (or all finished trees with --finished-trees)",
			core.ParamSpec{Name: "id", Type: core.ParamRef, RefKinds: []string{laneKind}, Description: "Lane ID"},
			core.ParamSpec{Name: "finished-trees", Type: core.ParamBool, Description: "Archive all complete root trees"}),
		cmd(laneKind+".undo", "lane undo", "Undo the most recent store mutation"),
		cmd(planKind+".define", "plan define", "Import a Markdown plan's Lanes table into the store",
			planRef, str("project", false, "Project reference"), str("name", false, "Override root lane name")),
		cmd(planKind+".list", "plan list --format json", "List all plans referenced in the lane store"),
		cmd(planKind+".show", "plan show --json", "Show a plan's summary and constituent lanes", planRef),
		cmd("lane.rollup", "rollup --format json", "Show subtree rollup counts and rolled statuses"),
	}
}

// manifest is the catalog `o-lane catalog` prints.
func manifest() manifestDoc {
	return manifestDoc{
		Name:    "o-lane",
		Version: Version,
		Schemas: []schemaDoc{
			{
				Kind: laneKind, Title: "Lane", Plural: "Lanes",
				Fields:         laneFields(),
				DefaultColumns: []string{"name", "status", "project", "plan", "updated"},
				SummaryField:   "name",
			},
			{
				Kind: planKind, Title: "Plan", Plural: "Plans",
				Fields:         planFields(),
				DefaultColumns: []string{"name", "lane_count", "done_count", "open_count", "updated"},
				SummaryField:   "name",
				UniqueFields:   []string{"path"},
			},
		},
		Commands: catalogCommands(),
	}
}

func laneRecord(st *Store, q Lane, rows []Lane, roll map[string][2]int) recordDoc {
	v := st.view(q, rows)
	r := roll[q.ID]
	archived := st.statuses()[q.Status].Archive
	fields := map[string]any{
		"name":           q.Name,
		"status":         q.Status,
		"rolled_status":  v.RolledStatus,
		"parent":         q.Parent,
		"project":        v.EffectiveProject,
		"project_id":     v.EffectiveProjectID,
		"plan":           q.Plan,
		"repo":           q.Repo,
		"branch":         q.Branch,
		"estimate":       q.Estimate,
		"wording":        q.Wording,
		"scope":          q.Scope,
		"done_when":      q.DoneWhen,
		"archived":       archived,
		"pane_count":     len(q.Panes),
		"children_done":  r[0],
		"children_total": r[1],
		"updated":        q.Updated.UTC().Format(time.RFC3339),
	}
	ro := make(map[string]bool, len(fields))
	for k := range fields {
		ro[k] = true
	}
	archStatus := "active"
	if archived {
		archStatus = "archived"
	}
	return recordDoc{
		Kind:          laneKind,
		ID:            q.ID,
		Fields:        fields,
		ReadOnly:      ro,
		ArchiveStatus: archStatus,
		StarStatus:    "unstarred",
		CreatedAt:     q.Created.UTC(),
		UpdatedAt:     q.Updated.UTC(),
	}
}

func planRecord(ps PlanSummary) recordDoc {
	updated, _ := time.Parse(time.RFC3339, ps.Updated)
	fields := map[string]any{
		"name":       ps.Name,
		"path":       ps.Path,
		"lane_count": ps.LaneCount,
		"done_count": ps.DoneCount,
		"open_count": ps.OpenCount,
		"projects":   strings.Join(ps.Projects, ", "),
		"updated":    ps.Updated,
	}
	ro := make(map[string]bool, len(fields))
	for k := range fields {
		ro[k] = true
	}
	return recordDoc{
		Kind:          planKind,
		ID:            ps.ID,
		Fields:        fields,
		ReadOnly:      ro,
		ArchiveStatus: "active",
		StarStatus:    "unstarred",
		CreatedAt:     updated,
		UpdatedAt:     updated,
	}
}

// queryStore returns matching lane.lane and lane.plan records (read-only).
func queryStore(st *Store, text, kind string, includeArchived bool) (queryResult, error) {
	rows, err := st.List()
	if err != nil {
		return queryResult{}, err
	}
	hidden := st.archiveHidden(rows, nil, !includeArchived)
	roll := st.rollups(rows)
	q := strings.ToLower(strings.TrimSpace(text))
	res := queryResult{Items: []recordDoc{}}

	if kind == "" || kind == laneKind {
		for _, lane := range rows {
			if hidden[lane.ID] {
				continue
			}
			rec := laneRecord(st, lane, rows, roll)
			if q != "" {
				hay := strings.ToLower(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
					lane.ID, lane.Name, lane.Status, rec.Fields["project"], lane.Plan, lane.Repo, lane.Scope))
				if !strings.Contains(hay, q) {
					continue
				}
			}
			res.Items = append(res.Items, rec)
		}
	}
	if kind == "" || kind == planKind {
		for _, ps := range listPlans(st, rows) {
			if q != "" {
				hay := strings.ToLower(ps.ID + "\x00" + ps.Name + "\x00" + ps.Path + "\x00" + strings.Join(ps.Projects, " "))
				if !strings.Contains(hay, q) {
					continue
				}
			}
			res.Items = append(res.Items, planRecord(ps))
		}
	}
	res.Total = len(res.Items)
	return res, nil
}

func extManifest() *oden.ExtManifest {
	m := manifest()
	schemas := make([]oden.ExtSchema, 0, len(m.Schemas))
	for _, s := range m.Schemas {
		fields := make([]oden.ExtField, 0, len(s.Fields))
		for _, f := range s.Fields {
			fields = append(fields, oden.ExtField{
				Name:        f.Name,
				Type:        string(f.Type),
				Required:    f.Required,
				Description: f.Description,
				Enum:        f.Enum,
				Default:     f.Default,
				RefKinds:    f.RefKinds,
			})
		}
		schemas = append(schemas, oden.ExtSchema{
			Kind:           s.Kind,
			Title:          s.Title,
			Plural:         s.Plural,
			Fields:         fields,
			DefaultColumns: s.DefaultColumns,
			SummaryField:   s.SummaryField,
			UniqueFields:   s.UniqueFields,
		})
	}
	cmds := make([]oden.ExtCommand, 0, len(m.Commands))
	for _, c := range m.Commands {
		params := make([]oden.ExtField, 0, len(c.Params))
		for _, p := range c.Params {
			params = append(params, oden.ExtField{
				Name:        p.Name,
				Type:        string(p.Type),
				Required:    p.Required,
				Description: p.Description,
				Enum:        p.Enum,
				Default:     p.Default,
				RefKinds:    p.RefKinds,
			})
		}
		cmds = append(cmds, oden.ExtCommand{
			ID:          c.ID,
			Description: c.Description,
			Params:      params,
			Tags:        c.Tags,
			Meta:        c.Meta,
		})
	}
	return &oden.ExtManifest{
		Name:     m.Name,
		Version:  m.Version,
		Schemas:  schemas,
		Commands: cmds,
	}
}

func extQueryStore(st *Store, kind string, args []string) (oden.ExtQueryResult, error) {
	a, err := parseArgs(args, "archived")
	if err != nil {
		return oden.ExtQueryResult{}, err
	}
	if kind == "" && a.flags["kind"] != "" {
		kind = a.flags["kind"]
	}
	qr, err := queryStore(st, strings.Join(a.pos, " "), kind, a.has("archived"))
	if err != nil {
		return oden.ExtQueryResult{}, err
	}
	items := make([]oden.ExtRecord, 0, len(qr.Items))
	for _, it := range qr.Items {
		items = append(items, oden.ExtRecord{
			Kind:            it.Kind,
			ID:              it.ID,
			Fields:          it.Fields,
			ReadOnly:        it.ReadOnly,
			ArchiveStatus:   it.ArchiveStatus,
			StarStatus:      it.StarStatus,
			ChangeSetNumber: it.ChangeSetNumber,
			CreatedAt:       it.CreatedAt,
			UpdatedAt:       it.UpdatedAt,
		})
	}
	return oden.ExtQueryResult{Items: items, Total: qr.Total}, nil
}
