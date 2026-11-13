package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// A lane's project is stored as the id of the host's project (mtm's key), so
// the relation graph can follow it; the name people type and read is the
// host's name for that id, kept in project_label as the fallback for a host
// that is absent or a project that is gone. A reference typed as a name, an
// id or kind:id (mtm:<id>) is resolved to the id when it matches exactly one
// project; one that matches nothing, or several, is stored as typed.

// Resolution outcomes.
const (
	projectEmpty     = "empty"
	projectID        = "id"        // already the id of a project
	projectKindID    = "kind-id"   // mtm:<id> of a project
	projectName      = "name"      // the name of exactly one project
	projectAmbiguous = "ambiguous" // the name of several projects
	projectUnmatched = "unmatched"
)

// projectMatch is how a reference resolved: Status, and for a match the
// project's id and name; for an ambiguous name, every id.
type projectMatch struct {
	Status string
	ID     string
	Name   string
	IDs    []string
	// Resolved is set when the reference names exactly one project.
	Resolved bool
	// Rewrite is set when resolving changes the stored text (not already an id).
	Rewrite bool
}

// Matched reports whether the reference names exactly one project.
func (m projectMatch) Matched() bool { return m.Resolved }

// resolveProjectRef resolves ref against the host's projects: an exact id
// first (with or without the mtm: kind), then a name, case-insensitively,
// as mtm resolves names.
func resolveProjectRef(projects []hostProject, ref string) projectMatch {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return projectMatch{Status: projectEmpty}
	}
	kind := false
	id := ref
	if rest, ok := strings.CutPrefix(ref, "mtm:"); ok && rest != "" {
		id, kind = rest, true
	}
	for _, p := range projects {
		if p.ID == id {
			status := projectID
			if kind {
				status = projectKindID
			}
			return projectMatch{Status: status, ID: p.ID, Name: p.Name, Resolved: true, Rewrite: kind}
		}
	}
	var found []hostProject
	for _, p := range projects {
		if strings.EqualFold(p.Name, ref) {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return projectMatch{Status: projectUnmatched}
	case 1:
		return projectMatch{Status: projectName, ID: found[0].ID, Name: found[0].Name, Resolved: true, Rewrite: true}
	}
	m := projectMatch{Status: projectAmbiguous}
	for _, p := range found {
		m.IDs = append(m.IDs, p.ID)
	}
	return m
}

// How long a host project list is reused, and how long after a failed read
// the host is left alone: a failed read is remembered so a page of hundreds
// of rows asks once, not once per row.
const (
	projectsTTL     = 3 * time.Second
	projectsFailTTL = 30 * time.Second
)

func (a *App) projectsFreshLocked() bool {
	if !a.projectsAt.IsZero() && time.Since(a.projectsAt) <= projectsTTL {
		return true
	}
	return !a.projectsFailAt.IsZero() && time.Since(a.projectsFailAt) <= projectsFailTTL
}

// fetchProjects reads the host once and records the answer or the failure.
// It is bounded by the call that started it: a served process's next call
// brings its own environment, so the read never outlives its call.
func (a *App) fetchProjects(ctx context.Context, done chan struct{}) {
	list, e := a.hostProjects(ctx)
	a.pmu.Lock()
	if e != nil {
		a.projectsFailAt = time.Now() // keep the last good list, if any
	} else {
		a.projects, a.projectsAt, a.projectsFailAt = list, time.Now(), time.Time{}
	}
	a.pflight = nil
	a.pmu.Unlock()
	close(done)
}

// refreshProjects starts a read of the host's projects unless the cache is
// fresh or one is under way; the returned channel closes when it ends (nil
// when nothing was started and nothing is pending).
func (a *App) refreshProjects() chan struct{} {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	if a.pflight != nil {
		return a.pflight
	}
	if a.projectsFreshLocked() {
		return nil
	}
	a.pflight, a.pstarted = make(chan struct{}), time.Now()
	ctx := a.callCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go a.fetchProjects(ctx, a.pflight)
	return a.pflight
}

// projectCached is the last list the host gave, however old; it never waits.
func (a *App) projectCached() []hostProject {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	return a.projects
}

// projectIndexWithin is the host's projects, waiting at most wait for a read
// that is due; on a slow or failed read it is the last good list (or none),
// so names stay as stored.
func (a *App) projectIndexWithin(wait time.Duration) []hostProject {
	if done := a.refreshProjects(); done != nil {
		// A read already under way has used up part of the wait: callers
		// after the first do not wait again for a host that is slow.
		a.pmu.Lock()
		wait -= time.Since(a.pstarted)
		a.pmu.Unlock()
		select {
		case <-done:
		case <-time.After(max(wait, 0)):
		}
	}
	return a.projectCached()
}

// projectIndex is for a write, which needs the host to resolve a typed name:
// it waits for the read (bounded by hostProjects's own timeout).
func (a *App) projectIndex() []hostProject {
	return a.projectIndexWithin(2 * projectsFailTTL)
}

// linkProjects gives the store its views of the host's projects.
func (a *App) linkProjects() {
	a.store.ProjectRef = func(ref string) projectMatch {
		if strings.TrimSpace(ref) == "" {
			return projectMatch{Status: projectEmpty}
		}
		return resolveProjectRef(a.projectIndex(), ref)
	}
}

// projectDisplay is the name a stored project reference is shown as: the
// name kept on the lane with it, else the stored value. A read never asks the
// host, so a renamed project keeps its old name until a write touches the row.
func (s *Store) projectDisplay(stored, label string) string {
	if stored == "" {
		return ""
	}
	if label != "" {
		return label
	}
	return stored
}

// canonProject is the id a reference resolves to, or the reference itself.
func (s *Store) canonProject(ref string) string { return canonVia(s.ProjectRef, ref) }

func canonVia(resolve func(string) projectMatch, ref string) string {
	if resolve != nil {
		if m := resolve(ref); m.Matched() {
			return m.ID
		}
	}
	return ref
}

// projectMatcher reports whether a lane's stored project (and the name kept
// with it) is the queried reference: by id, by name or by kind:id. It matches
// from what is stored. The host is asked only when the query names nothing
// stored: at once when rows are given and none of them matches, else (a write
// with no rows) on the first stored value that does not match directly. A
// slow or absent host never hides a lane.
func (s *Store) projectMatcher(query string, rows []Lane) func(stored, label string) bool {
	query = strings.TrimSpace(query)
	direct := func(stored, label string) bool {
		return stored != "" && (projectMatches(stored, query) || label != "" && strings.EqualFold(label, query))
	}
	asked, id := false, ""
	ask := func() {
		if !asked {
			asked, id = true, s.canonProject(query)
		}
	}
	if query != "" && rows != nil && !slices.ContainsFunc(rows, func(q Lane) bool { return direct(q.Project, q.ProjectLabel) }) {
		ask()
	}
	return func(stored, label string) bool {
		if stored == "" {
			return false
		}
		if direct(stored, label) {
			return true
		}
		if rows == nil && query != "" {
			ask()
		}
		return asked && (projectMatches(stored, id) || projectMatches(s.canonProject(stored), id))
	}
}

// normalizeProjects resolves the project of every lane a write added or
// changed. A reference that resolves is stored as the id with the project's
// name beside it; one that does not stays as typed (and loses a name kept
// for the old project); an ambiguous one is stored as typed and reported.
func (s *Store) normalizeProjects(before, after []Lane) {
	if s.ProjectRef == nil {
		return
	}
	old := map[string]string{}
	for _, q := range before {
		old[q.ID] = q.Project
	}
	for i := range after {
		q := &after[i]
		if prev, ok := old[q.ID]; ok && prev == q.Project || q.Project == "" {
			continue
		}
		m := s.ProjectRef(q.Project)
		switch {
		case m.Matched():
			q.Project, q.ProjectLabel = m.ID, m.Name
		case m.Status == projectAmbiguous:
			q.ProjectLabel = ""
			if s.Warn != nil {
				s.Warn(fmt.Sprintf("project %q matches %d projects; lane %s keeps it as typed", q.Project, len(m.IDs), q.ID))
			}
		default:
			q.ProjectLabel = ""
		}
	}
}

// ProjectReport is what the migration found and did.
type ProjectReport struct {
	DryRun    bool          `json:"dry_run"`
	Rows      int           `json:"rows"`
	Empty     int           `json:"empty"`
	AlreadyID int           `json:"already_id"`
	KindToID  int           `json:"kind_id_to_id"`
	NameToID  int           `json:"name_to_id"`
	Rewritten int           `json:"rewritten"`
	Unmatched []ProjectKept `json:"unmatched"`
	Ambiguous []ProjectKept `json:"ambiguous"`
	Backup    string        `json:"backup,omitempty"`
}

// ProjectKept is a reference left as stored, with its row count.
type ProjectKept struct {
	Name string   `json:"name"`
	Rows int      `json:"rows"`
	IDs  []string `json:"ids,omitempty"`
}

type splice struct {
	from, to int
	text     string
}

// projectProp is the lane key the migration reads: only a lane's own, never
// a history entry's.
const projectProp = "project"

// rowFrame is one JSON object or array being walked.
type rowFrame struct {
	obj   bool
	key   bool // the next string is a key
	cur   string
	keyAt int
}

// projectScan walks one root file (a JSON array of lanes) and finds each
// lane's own project string and the edit that rewrites it, without touching
// a byte elsewhere.
type projectScan struct {
	b        []byte
	projects []hostProject
	rep      *ProjectReport
	kept     map[string]*ProjectKept
	amb      map[string]*ProjectKept
	stack    []rowFrame
	out      []splice
	hasProj  bool
	key      string // the lane key whose string is the project
}

// stringStart is where the string token ending at end begins.
func (p *projectScan) stringStart(end int) int {
	for i := end - 2; i >= 0; i-- {
		if p.b[i] != '"' {
			continue
		}
		n := 0
		for j := i - 1; j >= 0 && p.b[j] == '\\'; j-- {
			n++
		}
		if n%2 == 0 {
			return i
		}
	}
	return 0
}

func (p *projectScan) valueDone() {
	if n := len(p.stack); n > 0 && p.stack[n-1].obj {
		p.stack[n-1].key = true
	}
}

// inLane reports whether the walk is at a key of a lane (an object directly
// inside the root array) named projectProp.
func (p *projectScan) atProject() bool {
	return len(p.stack) == 2 && p.stack[1].obj && p.stack[1].cur == p.key
}

func (p *projectScan) delim(t json.Delim) {
	if t == '{' || t == '[' {
		p.stack = append(p.stack, rowFrame{obj: t == '{', key: t == '{'})
		return
	}
	if len(p.stack) == 2 && p.stack[1].obj && !p.hasProj {
		p.rep.Rows++
		p.rep.Empty++
	}
	if len(p.stack) == 2 {
		p.hasProj = false
	}
	p.stack = p.stack[:len(p.stack)-1]
	p.valueDone()
}

func (p *projectScan) value(t string, end int) {
	p.rep.Rows++
	p.hasProj = true
	m := resolveProjectRef(p.projects, t)
	switch {
	case m.Status == projectEmpty:
		p.rep.Empty++
	case m.Resolved && !m.Rewrite:
		p.rep.AlreadyID++
	case m.Resolved:
		p.rewrite(m, end)
	case len(m.IDs) > 0:
		tally(p.amb, t, m.IDs)
	default:
		tally(p.kept, t, nil)
	}
}

func tally(into map[string]*ProjectKept, name string, ids []string) {
	if into[name] == nil {
		into[name] = &ProjectKept{Name: name, IDs: ids}
	}
	into[name].Rows++
}

// rewrite splices the id over the value and the name in beside it as
// project_label, indented as the project key is.
func (p *projectScan) rewrite(m projectMatch, end int) {
	if len(m.IDs) == 0 && m.Status == projectKindID {
		p.rep.KindToID++
	} else {
		p.rep.NameToID++
	}
	p.rep.Rewritten++
	name, _ := json.Marshal(m.Name)
	text := `"` + m.ID + `"`
	ks := p.stack[1].keyAt
	nl := bytes.LastIndexByte(p.b[:ks], '\n')
	if nl >= 0 && len(bytes.TrimSpace(p.b[nl+1:ks])) == 0 {
		text += ",\n" + string(p.b[nl+1:ks]) + `"project_label": ` + string(name)
	} else {
		text += `,"project_label":` + string(name)
	}
	p.out = append(p.out, splice{from: p.stringStart(end), to: end, text: text})
}

func (p *projectScan) token(tok json.Token, end int) {
	switch t := tok.(type) {
	case json.Delim:
		p.delim(t)
	case string:
		n := len(p.stack)
		if n > 0 && p.stack[n-1].obj && p.stack[n-1].key {
			p.stack[n-1].cur, p.stack[n-1].key, p.stack[n-1].keyAt = t, false, p.stringStart(end)
			return
		}
		if p.atProject() {
			p.value(t, end)
		}
		p.valueDone()
	default:
		if p.atProject() {
			p.rep.Rows++
			p.rep.Empty++
			p.hasProj = true
		}
		p.valueDone()
	}
}

func projectSplices(b []byte, projects []hostProject, rep *ProjectReport, kept, amb map[string]*ProjectKept) ([]splice, error) {
	p := &projectScan{key: projectProp, b: b, projects: projects, rep: rep, kept: kept, amb: amb}
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return p.out, nil
		}
		if err != nil {
			return nil, err
		}
		p.token(tok, int(dec.InputOffset()))
	}
}

func applySplices(b []byte, sp []splice) []byte {
	sort.Slice(sp, func(i, j int) bool { return sp[i].from > sp[j].from })
	out := append([]byte(nil), b...)
	for _, s := range sp {
		out = append(out[:s.from], append([]byte(s.text), out[s.to:]...)...)
	}
	return out
}

// ResolveProjects rewrites every lane's project reference that matches
// exactly one of projects to that project's id, keeping its name in
// project_label. References that match nothing or several, and every row
// without a project, stay byte for byte as they were; history entries are
// never read. A dry run writes nothing. A real run first copies the roots to
// backup (dir + ".bak-<date>-project-ids"), refuses to write when that
// fails, and is idempotent: ids are not rewritten.
func (s *Store) ResolveProjects(projects []hostProject, dry bool) (ProjectReport, error) {
	rep := ProjectReport{DryRun: dry, Unmatched: []ProjectKept{}, Ambiguous: []ProjectKept{}}
	err := s.locked(func() error {
		files, e := filepath.Glob(filepath.Join(s.Dir, "roots", "*.json"))
		if e != nil {
			return e
		}
		sort.Strings(files)
		kept, amb := map[string]*ProjectKept{}, map[string]*ProjectKept{}
		type edit struct {
			path string
			data []byte
			sp   []splice
		}
		var edits []edit
		for _, f := range files {
			b, e := os.ReadFile(f)
			if e != nil {
				return e
			}
			sp, e := projectSplices(b, projects, &rep, kept, amb)
			if e != nil {
				return fmt.Errorf("%s: %w", f, e)
			}
			if len(sp) > 0 {
				edits = append(edits, edit{path: f, data: b, sp: sp})
			}
		}
		for _, m := range kept {
			rep.Unmatched = append(rep.Unmatched, *m)
		}
		for _, m := range amb {
			rep.Ambiguous = append(rep.Ambiguous, *m)
		}
		byRows := func(x []ProjectKept) {
			sort.Slice(x, func(i, j int) bool {
				if x[i].Rows != x[j].Rows {
					return x[i].Rows > x[j].Rows
				}
				return x[i].Name < x[j].Name
			})
		}
		byRows(rep.Unmatched)
		byRows(rep.Ambiguous)
		if dry || len(edits) == 0 {
			return nil
		}
		next := make([][]byte, len(edits))
		for i, ed := range edits {
			next[i] = applySplices(ed.data, ed.sp)
			if !json.Valid(next[i]) {
				return fmt.Errorf("%s: the rewrite is not valid JSON; nothing written", ed.path)
			}
		}
		rep.Backup = s.Dir + ".bak-" + s.now().Format("20060102") + "-project-ids"
		if e := copyTree(filepath.Join(s.Dir, "roots"), filepath.Join(rep.Backup, "roots")); e != nil {
			return fmt.Errorf("backup %s failed, nothing written: %w", rep.Backup, e)
		}
		for i, ed := range edits {
			if e := writeFileAtomic(ed.path, next[i]); e != nil {
				return e
			}
		}
		return nil
	})
	return rep, err
}

// copyTree copies the regular files of src into dst (which must not exist).
func copyTree(src, dst string) error {
	if _, e := os.Stat(dst); e == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	if e := os.MkdirAll(dst, 0700); e != nil {
		return e
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() || !d.Type().IsRegular() {
			return e
		}
		rel, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		return writeFileAtomic(filepath.Join(dst, rel), b)
	})
}

func writeFileAtomic(path string, b []byte) error {
	tmp, e := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if e != nil {
		return e
	}
	_, e = tmp.Write(b)
	e = errors.Join(e, tmp.Chmod(0600), tmp.Sync(), tmp.Close())
	if e != nil {
		os.Remove(tmp.Name())
		return e
	}
	return os.Rename(tmp.Name(), path)
}

// rowProject sets a page row's project by name, with the stored id beside it.
func (s *Store) rowProject(data map[string]any, q Lane) {
	if q.Project != "" {
		data["project"], data["project_id"] = s.projectDisplay(q.Project, q.ProjectLabel), q.Project
	}
}
