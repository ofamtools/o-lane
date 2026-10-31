package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	table "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"ofam.dev/ochazuke"
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

func defineParams() map[string]ochazuke.Parameter {
	return map[string]ochazuke.Parameter{
		"lanes":     {Type: "array", Description: "JSON lane specs; exclusive with plan/plans-dir"},
		"plan":      param("Markdown plan path or exact filename in lanes.plans-dir"),
		"plans-dir": param("Recursively check Markdown plans; requires dry-run"),
		"dry-run":   {Type: "boolean", Description: "Parse and list definitions without accessing the lane store"},
		"name":      param("Root name; defaults to the plan title or filename"),
		"wording":   param("Verbatim owner wording on root creation only"),
		"task":      param("Optional root task link"),
		"project":   projectParam("Project for the batch's top lanes (plan: the root); children inherit. " + projectHelp),
	}
}

func planDefineParams() map[string]ochazuke.Parameter {
	p := defineParams()
	delete(p, "lanes")
	delete(p, "plans-dir")
	path := p["plan"]
	path.Required = true
	p["plan"] = path
	return p
}

func (a *App) planPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("plan path is required")
	}
	if !filepath.IsAbs(path) && filepath.Dir(path) == "." && a.r.Settings().String("lanes.plans-dir") != "" {
		path = filepath.Join(a.r.Settings().String("lanes.plans-dir"), path)
		if filepath.Ext(path) == "" {
			path += ".md"
		}
	}
	return filepath.Abs(path)
}

func (a *App) readPlan(path string, columns map[string]string) (planDefinition, error) {
	path, e := a.planPath(path)
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

func (a *App) checkPlans(dir string, columns map[string]string) (planChecks, error) {
	out := planChecks{Plans: []planCheck{}}
	e := filepath.WalkDir(dir, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		p, e := a.readPlan(path, columns)
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

func (a *App) define(args ochazuke.Arguments) ochazuke.Result {
	str := func(k string) string { return ochazuke.StringArg(args, k, "") }
	dry, _ := args["dry-run"].(bool)
	_, hasJSON := args["lanes"]
	sources := 0
	for _, present := range []bool{hasJSON, str("plan") != "", str("plans-dir") != ""} {
		if present {
			sources++
		}
	}
	if sources != 1 {
		return done(nil, errors.New("define requires exactly one of lanes, plan or plans-dir"))
	}
	if str("plans-dir") != "" {
		if !dry {
			return done(nil, errors.New("plans-dir requires dry-run"))
		}
		columns, e := parseImportColumns(a.r.Settings().String("lanes.import-columns"))
		if e != nil {
			return done(nil, e)
		}
		result, e := a.checkPlans(str("plans-dir"), columns)
		if e != nil {
			return done(nil, e)
		}
		// Error counts and per-file reasons remain machine-readable, including failures.
		return done(result, nil)
	}
	var specs []Spec
	var fields []map[string]any
	if hasJSON {
		if str("name") != "" || str("wording") != "" || str("task") != "" {
			return done(nil, errors.New("name, wording and task require a plan"))
		}
		if e := decode(args["lanes"], &specs); e != nil {
			return done(nil, e)
		}
		if e := decode(args["lanes"], &fields); e != nil {
			return done(nil, e)
		}
	} else {
		columns, e := parseImportColumns(a.r.Settings().String("lanes.import-columns"))
		if e != nil {
			return done(nil, e)
		}
		p, e := a.readPlan(str("plan"), columns)
		if e != nil {
			return done(nil, e)
		}
		if str("name") != "" {
			p.Name = str("name")
		}
		root := Spec{Name: p.Name, Wording: str("wording"), Task: str("task"), Plan: p.Path, Project: str("project")}
		specs = append(specs, root)
		var rootFields map[string]any
		_ = decode(root, &rootFields)
		fields = append(fields, rootFields)
		for i, spec := range p.Lanes {
			if spec.Name == root.Name {
				return done(nil, fmt.Errorf("plan root and child share name %q; choose a root name", root.Name))
			}
			spec.Parent = root.Name
			specs = append(specs, spec)
			fields = append(fields, p.fields[i])
		}
	}
	if project := str("project"); project != "" && hasJSON {
		// The batch's top lanes (parent outside the batch) take the project
		// unless they name their own; batch children inherit from them.
		names := map[string]bool{}
		for _, spec := range specs {
			names[spec.Name] = true
		}
		for i := range specs {
			if specs[i].Project == "" && !names[specs[i].Parent] {
				specs[i].Project = project
				if fields[i] == nil {
					fields[i] = map[string]any{}
				}
				fields[i]["project"] = project
			}
		}
	}
	if e := validateDefinitions(specs); e != nil {
		return done(nil, e)
	}
	if dry {
		return done(specs, nil)
	}
	rows, e := a.store.defineFields(specs, fields)
	return done(rows, e)
}

func (a *App) planLanes(path string) ochazuke.Result {
	path, e := a.planPath(path)
	if e != nil {
		return done(nil, e)
	}
	rows, e := a.store.List()
	if e != nil {
		return done(nil, e)
	}
	out := []View{}
	for _, q := range rows {
		if q.Plan == path {
			out = append(out, a.store.view(q, rows))
		}
	}
	return done(out, nil)
}
