package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	table "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const defaultImportColumns = `{"lane":"name","scope":"scope","work":"scope","what":"scope","repo":"repo","depends on":"depends","estimate":"estimate","est.":"estimate","lane time":"estimate","agent time":"estimate","done when":"done_when","tests and stories":"tests"}`

var errNoLanes = errors.New("no Lanes table found")
var lanesHeading = regexp.MustCompile(`(?i)^(?:[0-9]+[.)]?\s+)?lanes\b`)

type planDefinition struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Lanes  []Spec `json:"lanes"`
	fields []map[string]any
}

// PlanSummary summarizes a plan referenced by one or more lanes in the store.
type PlanSummary struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	LaneCount int      `json:"lane_count"`
	DoneCount int      `json:"done_count"`
	OpenCount int      `json:"open_count"`
	RootIDs   []string `json:"root_ids,omitempty"`
	Projects  []string `json:"projects,omitempty"`
	Updated   string   `json:"updated,omitempty"`
}

func decode(v any, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func jsonFields(v any) []string {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func unknownKeys(row map[string]any, known []string) []string {
	var out []string
	for k := range row {
		if !slices.Contains(known, k) {
			out = append(out, fmt.Sprintf("unknown field %q (ignored)", k))
		}
	}
	sort.Strings(out)
	return out
}

func parseImportColumns(raw string) (map[string]string, error) {
	var input map[string]string
	if e := json.Unmarshal([]byte(raw), &input); e != nil {
		return nil, fmt.Errorf("lanes.import-columns: %w", e)
	}
	columns := map[string]string{}
	for header, field := range input {
		header = strings.ToLower(strings.Join(strings.Fields(header), " "))
		if header == "" || (field != "name" && field != "wording" && !slices.Contains(editable, field)) {
			return nil, fmt.Errorf("lanes.import-columns: invalid mapping %q to %q", header, field)
		}
		if _, exists := columns[header]; exists {
			return nil, fmt.Errorf("lanes.import-columns: duplicate header %q", header)
		}
		columns[header] = field
	}
	hasName := false
	for _, field := range columns {
		if field == "name" {
			hasName = true
		}
	}
	if !hasName {
		return nil, errors.New("lanes.import-columns: a name column is required")
	}
	return columns, nil
}

// Prefixes match whole header words, so "Tests and stories (keys...)" uses
// the same data row as "Tests and stories". More specific mappings win.
func columnField(header string, columns map[string]string) string {
	header = strings.ToLower(strings.Join(strings.Fields(header), " "))
	best, result := "", ""
	for name, field := range columns {
		if header == name || strings.HasPrefix(header, name+" ") {
			if len(name) > len(best) {
				best, result = name, field
			}
		}
	}
	return result
}

func plainMarkdown(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n := n.(type) {
			case *ast.Text:
				value := n.Segment.Value(src)
				if !n.IsRaw() {
					value = util.UnescapePunctuations(value)
					value = []byte(html.UnescapeString(string(value)))
				}
				b.Write(value)
			case *ast.String:
				b.Write(n.Value)
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

func rawCell(n ast.Node, src []byte) string {
	var b strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		segment := n.Lines().At(i)
		b.Write(segment.Value(src))
	}
	return strings.TrimSpace(b.String())
}

func parsePlan(path string, src []byte, columns map[string]string) (planDefinition, error) {
	p := planDefinition{Path: path, Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Lanes: []Spec{}}
	doc := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(src))
	type candidate struct {
		node    *table.Table
		inLanes bool
	}
	var tables []candidate
	section := 0
	titled := false
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			label := plainMarkdown(h, src)
			if h.Level == 1 && !titled {
				p.Name, titled = label, true
			}
			if section > 0 && h.Level <= section {
				section = 0
			}
			if lanesHeading.MatchString(label) {
				section = h.Level
			}
		}
		if t, ok := n.(*table.Table); ok {
			tables = append(tables, candidate{t, section > 0})
		}
		return ast.WalkContinue, nil
	})
	hasName := func(t *table.Table) bool {
		for cell := t.FirstChild().FirstChild(); cell != nil; cell = cell.NextSibling() {
			if columnField(plainMarkdown(cell, src), columns) == "name" {
				return true
			}
		}
		return false
	}
	// Prefer explicitly labelled sections to earlier design/comparison tables.
	explicit := false
	for _, c := range tables {
		if c.inLanes && hasName(c.node) {
			explicit = true
		}
	}
	seen := map[string]bool{}
	for _, c := range tables {
		if explicit && !c.inLanes {
			continue
		}
		header := c.node.FirstChild()
		if !hasName(c.node) || (!explicit && columnField(plainMarkdown(header.FirstChild(), src), columns) != "name") {
			continue
		}
		var fields []string
		used := map[string]bool{}
		for cell := header.FirstChild(); cell != nil; cell = cell.NextSibling() {
			field := columnField(plainMarkdown(cell, src), columns)
			if field != "" && used[field] {
				return p, fmt.Errorf("duplicate mapped column %q", field)
			}
			used[field] = true
			fields = append(fields, field)
		}
		for row := header.NextSibling(); row != nil; row = row.NextSibling() {
			obj := map[string]any{"plan": path}
			i := 0
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				field := fields[i]
				i++
				if field == "" {
					continue
				}
				value := rawCell(cell, src)
				if field == "name" {
					value = plainMarkdown(cell, src)
				}
				if field == "depends" {
					deps := []string{}
					if value != "" {
						deps = append(deps, value)
					}
					obj[field] = deps
				} else {
					obj[field] = value
				}
			}
			var spec Spec
			if e := decode(obj, &spec); e != nil {
				return p, e
			}
			if strings.TrimSpace(spec.Name) == "" {
				return p, errors.New("empty lane name")
			}
			if seen[spec.Name] {
				return p, fmt.Errorf("duplicate lane name %q", spec.Name)
			}
			seen[spec.Name] = true
			p.Lanes = append(p.Lanes, spec)
			p.fields = append(p.fields, obj)
		}
	}
	if len(p.Lanes) == 0 {
		return p, errNoLanes
	}
	return p, nil
}

func resolvePlanPath(path, plansDir string) (string, error) {
	if path == "" {
		return "", errors.New("plan path is required")
	}
	if !filepath.IsAbs(path) && filepath.Dir(path) == "." && plansDir != "" {
		path = filepath.Join(plansDir, path)
		if filepath.Ext(path) == "" {
			path += ".md"
		}
	}
	return filepath.Abs(path)
}

func readPlanFile(path, plansDir string, columns map[string]string) (planDefinition, error) {
	path, e := resolvePlanPath(path, plansDir)
	if e != nil {
		return planDefinition{}, e
	}
	src, e := os.ReadFile(path)
	if e != nil {
		return planDefinition{}, e
	}
	p, e := parsePlan(path, src, columns)
	if e != nil {
		return p, fmt.Errorf("%s: %w", path, e)
	}
	return p, nil
}

type planCheck struct {
	Path    string `json:"path"`
	Lanes   int    `json:"lanes"`
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}
type planChecks struct {
	Plans  []planCheck `json:"plans"`
	Errors int         `json:"errors"`
}

func checkPlansDir(dir, plansDir string, columns map[string]string) (planChecks, error) {
	out := planChecks{Plans: []planCheck{}}
	e := filepath.WalkDir(dir, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		p, e := readPlanFile(path, plansDir, columns)
		row := planCheck{Path: path, Lanes: len(p.Lanes)}
		if errors.Is(e, errNoLanes) {
			row.Skipped = true
		} else if e != nil {
			row.Error = e.Error()
			out.Errors++
		}
		out.Plans = append(out.Plans, row)
		return nil
	})
	return out, e
}

// defineKeyNotices names each key of a JSON definition that is no spec
// field and no prop lane set writes: never dropped in silence, never refused.
func defineKeyNotices(specs []Spec, fields []map[string]any, warn func(string)) {
	if warn == nil {
		return
	}
	known := append(jsonFields(Spec{}), lifecycleProps...)
	for i, row := range fields {
		for _, text := range unknownKeys(row, known) {
			warn(fmt.Sprintf("lane define: lane %d (%q): %s", i+1, specs[i].Name, text))
		}
	}
}

// defineFromPlan parses a Markdown plan file and defines its root + child lanes.
func defineFromPlan(s *Store, planPath, plansDir, rootName, wording, task, project string, dry bool, columns map[string]string) ([]Lane, []Spec, error) {
	p, e := readPlanFile(planPath, plansDir, columns)
	if e != nil {
		return nil, nil, e
	}
	if rootName != "" {
		p.Name = rootName
	}
	root := Spec{Name: p.Name, Wording: wording, Task: task, Plan: p.Path, Project: project}
	specs := []Spec{root}
	var rootFields map[string]any
	_ = decode(root, &rootFields)
	fields := []map[string]any{rootFields}
	for i, spec := range p.Lanes {
		if spec.Name == root.Name {
			return nil, nil, fmt.Errorf("plan root and child share name %q; choose a root name", root.Name)
		}
		spec.Parent = root.Name
		specs = append(specs, spec)
		fields = append(fields, p.fields[i])
	}
	s.fillUntitled(specs)
	if e := validateDefinitions(specs); e != nil {
		return nil, nil, e
	}
	if dry {
		return nil, specs, nil
	}
	rows, e := s.defineFields(specs, fields)
	return rows, specs, e
}

// listPlans aggregates all distinct plans referenced by visible lanes in the store.
func listPlans(s *Store, rows []Lane) []PlanSummary {
	byPlan := map[string]*PlanSummary{}
	statuses := s.statuses()
	for _, q := range rows {
		if q.Plan == "" {
			continue
		}
		ps, ok := byPlan[q.Plan]
		if !ok {
			name := strings.TrimSuffix(filepath.Base(q.Plan), filepath.Ext(q.Plan))
			if q.Parent == "" && q.Name != "" {
				name = q.Name
			}
			ps = &PlanSummary{ID: slug(q.Plan), Path: q.Plan, Name: name}
			byPlan[q.Plan] = ps
		}
		if q.Parent == "" {
			if q.Name != "" {
				ps.Name = q.Name
			}
			ps.RootIDs = append(ps.RootIDs, q.ID)
		}
		ps.LaneCount++
		if s.complete(q.Status) {
			ps.DoneCount++
		} else if !statuses[q.Status].Archive {
			ps.OpenCount++
		}
		if proj, from := effectiveProject(rows, q); proj != "" {
			disp := s.projectDisplay(proj, labelOf(rows, from))
			if !slices.Contains(ps.Projects, disp) {
				ps.Projects = append(ps.Projects, disp)
			}
		}
		ts := q.Updated.UTC().Format("2006-01-02T15:04:05Z")
		if ts > ps.Updated {
			ps.Updated = ts
		}
	}
	out := make([]PlanSummary, 0, len(byPlan))
	for _, ps := range byPlan {
		sort.Strings(ps.Projects)
		out = append(out, *ps)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Updated != out[j].Updated {
			return out[i].Updated > out[j].Updated
		}
		return out[i].Name < out[j].Name
	})
	return out
}
