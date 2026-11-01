package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/monogatari"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/keytest"
)

func pageFixture(t *testing.T) *App {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "TUIOS_") || strings.HasPrefix(key, "CLAUDE_") || strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "PI_") || strings.HasPrefix(key, "OMP_") {
			t.Setenv(key, "")
		}
	}
	a, e := newApp(fixture(t), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.configure(); e != nil {
		t.Fatal(e)
	}
	a.store.BackupDaily = false
	a.store.Now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	a.run = func(_ context.Context, argv []string) ([]byte, error) {
		switch strings.Join(argv, " ") {
		case "tuios list-sessions --json":
			return []byte(`[{"name":"fixture-session"}]`), nil
		case "tuios list-windows --json -s fixture-session":
			return []byte(`{"windows":[{"window_id":"fixture-window","name":"Worker","harness":"codex"}]}`), nil
		case "tuios focus-window -s fixture-session fixture-window":
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected runner call: %q", argv)
	}
	return a
}
func seedPage(t *testing.T, a *App, raw json.RawMessage) {
	t.Helper()
	var seed struct {
		Lanes        []Spec
		Start        string
		Associations []struct {
			Lane string
			Pane Pane
		}
		States []struct{ Lane, State, Note string }
	}
	if e := json.Unmarshal(raw, &seed); e != nil {
		t.Fatal(e)
	}
	if _, e := a.store.Define(seed.Lanes); e != nil {
		t.Fatal(e)
	}
	if seed.Start != "" {
		if _, e := a.store.Start(seed.Start, Pane{}, "", ""); e != nil {
			t.Fatal(e)
		}
	}
	for _, st := range seed.States {
		if _, e := a.store.State(st.Lane, st.State, st.Note); e != nil {
			t.Fatal(e)
		}
	}
	for _, association := range seed.Associations {
		if _, err := a.store.Associate(association.Lane, association.Pane, false); err != nil {
			t.Fatal(err)
		}
	}
	a.store.Now = func() time.Time { return time.Date(2026, 1, 1, 13, 30, 0, 0, time.UTC) }
}
func TestLaneStories(t *testing.T) {
	stories, e := monogatari.LoadDir("cmd/o-story/stories/lanes")
	if e != nil {
		t.Fatal(e)
	}
	for _, story := range stories {
		t.Run(story.Name, func(t *testing.T) {
			var a *App
			var focused [][]string
			lib := monogatari.Library{Stories: stories, Apps: []monogatari.App{{Name: "o-lane", Fixtures: []monogatari.Fixture{{Name: "lanes", Build: func(env monogatari.Env) (monogatari.Built, error) {
				a = pageFixture(t)
				seedPage(t, a, env.Seed)
				if strings.HasPrefix(env.Story.Name, "lanes/live-") {
					stubTuios(t)
					t.Setenv("LANE_STUB_STORY", "1")
					production, err := newApp(a.dir, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
					if err != nil {
						return monogatari.Built{}, err
					}
					a.run = production.run
				}
				for name, value := range env.Story.Values {
					if e := a.r.Settings().Set(name, value); e != nil {
						return monogatari.Built{}, e
					}
				}
				run := a.run
				a.run = func(ctx context.Context, argv []string) ([]byte, error) {
					if len(argv) > 1 && argv[1] == "focus-window" {
						focused = append(focused, append([]string(nil), argv...))
					}
					return run(ctx, argv)
				}
				p, e := mountPage(a)
				if e != nil {
					return monogatari.Built{}, e
				}
				return monogatari.Built{Model: p.m, Program: p}, nil
			}}}}}}
			res := lib.Run(story, monogatari.RunOptions{Observe: func(pos monogatari.RunPosition) {
				if dir := os.Getenv("O_LANE_STORY_ARTIFACTS"); dir != "" {
					if e := os.MkdirAll(dir, 0700); e != nil {
						t.Fatal(e)
					}
					if e := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%02d.txt", filepath.Base(story.Path), pos.After)), []byte(ansi.Strip(pos.Session.View().Content)), 0600); e != nil {
						t.Fatal(e)
					}
				}
			}})
			if !res.OK() {
				for _, step := range res.Trace {
					t.Log(step.Event.String(), ansi.Strip(step.After))
				}
				t.Fatalf("%v\n%v", res.Err, res.Failed)
			}
			if story.Name == "lanes/live-off" {
				if data, err := os.ReadFile(filepath.Join(os.Getenv("LANE_STUB_DIR"), "calls")); err == nil {
					t.Fatalf("live off ran tuios: %s", data)
				}
			}
			if strings.HasSuffix(story.Name, "state-change") {
				q, e := a.store.Resolve("Implementation")
				if e != nil || q.State != "running" {
					t.Fatal(q, e)
				}
			}
			if strings.HasSuffix(story.Name, "associate-pane") {
				q, e := a.store.Resolve("Implementation")
				if e != nil || len(q.Panes) != 1 || q.Panes[0].Window != "fixture-window" || q.Panes[0].Session != "fixture-session" {
					t.Fatal(q, e)
				}
			}
			if story.Name == "lanes/focus-pane" {
				want := [][]string{{"tuios", "focus-window", "-s", "fixture-session", "fixture-window"}}
				if !reflect.DeepEqual(focused, want) {
					t.Fatalf("focus commands = %v, want %v", focused, want)
				}
			}
		})
	}
}
func TestPageProjection(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Root","wording":"owner exact words"},{"name":"Child","parent":"Root"},{"name":"Leaf","parent":"Child"}],"start":"Leaf"}`))
	q, _ := a.store.Resolve("Leaf")
	if _, e := a.store.Associate(q.ID, Pane{Session: "fixture-session", Window: "fixture-window", Role: "reviewer", Harness: "codex"}, false); e != nil {
		t.Fatal(e)
	}
	rows, e := laneRows(a.store, a.pageOptions())
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	if rows[1].Parent != rows[0].ID || rows[2].Parent != rows[1].ID {
		t.Fatal(rows)
	}
	if rows[0].Cells[1] != "planned" || rows[2].Cells[1] != "running" || rows[2].Cells[5] != "1h 30m" || rows[2].Cells[6] != "reviewer" {
		t.Fatal(rows)
	}
	if rows[2].Tones[0].Tone != ochazuke.ToneWorking {
		t.Fatal(rows[2].Tones)
	}
	if _, e = a.store.State(q.ID, "done", ""); e != nil {
		t.Fatal(e)
	}
	a.store.Now = func() time.Time { return time.Date(2026, 1, 1, 17, 0, 0, 0, time.UTC) }
	rows, e = laneRows(a.store, a.pageOptions())
	if e != nil || rows[2].Cells[5] != "1h 30m" {
		t.Fatal(rows, e)
	}
	for _, name := range []string{"lanes.durations", "lanes.pane-column", "lanes.state-tones", "lanes.history-in-detail", "lanes.tree", "lanes.project-column"} {
		if e = a.r.Settings().Set(name, false); e != nil {
			t.Fatal(e)
		}
	}
	o := a.pageOptions()
	rows, e = laneRows(a.store, o)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows[2].Cells) != 5 || len(rows[2].Tones) != 0 || rows[2].Parent != "" {
		t.Fatal(rows[2])
	}
	data := rows[2].Data.(map[string]any)
	if _, ok := data["history"]; ok {
		t.Fatal("history opt-out ignored")
	}
	if _, ok := data["duration"]; ok {
		t.Fatal("duration opt-out ignored")
	}
	p := lanePage(o)
	for _, section := range p.Detail.Sections {
		if section.Kind == "thread" {
			t.Fatal("history section opt-out ignored")
		}
	}
	if e = a.r.Settings().Set("lanes.detail", false); e != nil {
		t.Fatal(e)
	}
	if lanePage(a.pageOptions()).Detail != nil {
		t.Fatal("detail opt-out ignored")
	}
	// Closed-root filtering drops the entire tree, never orphaning children.
	for _, name := range []string{"Root", "Child"} {
		if _, e = a.store.State(name, "done", ""); e != nil {
			t.Fatal(e)
		}
	}
	if e = a.r.Settings().Set("lanes.hide-closed-roots", true); e != nil {
		t.Fatal(e)
	}
	rows, e = laneRows(a.store, a.pageOptions())
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
}
func TestPageCatalogMount(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Root"},{"name":"Child","parent":"Root"},{"name":"Leaf","parent":"Child"}]}`))
	b, e := ocli.Catalog(a.r, "o-lane", version)
	if e != nil {
		t.Fatal(e)
	}
	var doc ocli.CatalogDoc
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	if len(doc.Pages) != 1 || !doc.Pages[0].Lists[0].Tree {
		t.Fatal(doc.Pages)
	}
	m := ochazuke.NewModel(ochazuke.NewRuntime())
	m.Width, m.Height = 150, 36
	v, e := ocli.MountPage(m, doc, doc.Pages[0], ocli.PageConfig{})
	if e != nil {
		t.Fatal(e)
	}
	rows, e := laneRows(a.store, a.pageOptions())
	if e != nil {
		t.Fatal(e)
	}
	if e = v.SetRows(listID, rows); e != nil {
		t.Fatal(e)
	}
	if e = m.Runtime.OpenPage(pageID); e != nil {
		t.Fatal(e)
	}
	if e = v.List(listID).Focus(); e != nil {
		t.Fatal(e)
	}
	frame := ansi.Strip(m.View().Content)
	for _, label := range []string{"Root", "Child", "Leaf"} {
		if !strings.Contains(frame, label) {
			t.Fatal(frame)
		}
	}
	nodes := m.Runtime.Tree(listID).Nodes()
	if len(nodes) != 3 || nodes[2].Parent != listID+"/"+rows[1].ID {
		t.Fatal(nodes)
	}
	// The six domain key rows travel in the catalog; no application key switch.
	want := map[string]string{"enter": "show", "s": "choose-state", "g p": "focus-pane", "a": "choose-pane", "n": "note", "y": "yank-id"}
	for _, row := range doc.Bindings {
		if row.Noun == "lane" {
			if verb, ok := want[row.Keys]; ok && verb == row.Request.Verb {
				delete(want, row.Keys)
			}
		}
	}
	if len(want) > 0 {
		t.Fatal(want)
	}
}
func pageDeliver(t *testing.T, p *page, msg tea.Msg) {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			if cmd != nil {
				pageDeliver(t, p, cmd())
			}
		}
		return
	}
	_, cmd := p.Update(msg)
	if cmd != nil {
		pageDeliver(t, p, cmd())
	}
}
func pageKey(t *testing.T, p *page, key string) {
	t.Helper()
	_, cmd := p.Update(keytest.Msg(key, false, false))
	if cmd != nil {
		pageDeliver(t, p, cmd())
	}
}
func TestPageFocusAndYank(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Implementation"}]}`))
	if _, e := a.store.Associate("Implementation", Pane{Session: "fixture-session", Window: "fixture-window", Role: "implementer"}, false); e != nil {
		t.Fatal(e)
	}
	calls := 0
	a.run = func(_ context.Context, argv []string) ([]byte, error) {
		calls++
		if !reflect.DeepEqual(argv, []string{"tuios", "focus-window", "-s", "fixture-session", "fixture-window"}) {
			return nil, fmt.Errorf("unexpected %v", argv)
		}
		return nil, nil
	}
	if e := a.r.Settings().Set("lanes.live", false); e != nil {
		t.Fatal(e)
	}
	p, e := mountPage(a)
	if e != nil {
		t.Fatal(e)
	}
	p.m.Width, p.m.Height = 140, 38
	p.m.View()
	pageKey(t, p, "g")
	pageKey(t, p, "p")
	if calls != 1 {
		t.Fatalf("focus ran %d commands", calls)
	}
	pageKey(t, p, "y")
	q, _ := a.store.Resolve("Implementation")
	if got := a.r.Registers().Named["\""].Yank.Text; got != q.ID {
		t.Fatalf("yank %q want %q", got, q.ID)
	}
	pageKey(t, p, "enter")
	if a.r.TopModal() != "lane-detail" {
		t.Fatal("show did not open detail", a.r.TopModal())
	}
	pageKey(t, p, "esc")
	if a.r.TopModal() != "" {
		t.Fatal("detail did not close")
	}
}
func TestPagePaneChoices(t *testing.T) {
	a := pageFixture(t)
	choices, e := a.paneChoices(context.Background())
	if e != nil || len(choices) != 1 {
		t.Fatal(choices, e)
	}
	p, e := paneChoice(choices[0].ID)
	if e != nil || p.Session != "fixture-session" || p.Window != "fixture-window" {
		t.Fatal(p, e)
	}
	a.run = func(context.Context, []string) ([]byte, error) { return nil, errors.New("offline") }
	if _, e = a.paneChoices(context.Background()); e == nil {
		t.Fatal("missing runner error")
	}
}
func TestPageNewestRoots(t *testing.T) {
	a := pageFixture(t)
	old, e := a.store.Add(Spec{Name: "Older"})
	if e != nil {
		t.Fatal(e)
	}
	a.store.Now = func() time.Time { return old.Created.Add(time.Minute) }
	if _, e = a.store.Add(Spec{Name: "Newer"}); e != nil {
		t.Fatal(e)
	}
	rows, e := laneRows(a.store, a.pageOptions())
	if e != nil || !slices.Equal([]string{rows[0].Label, rows[1].Label}, []string{"Newer", "Older"}) {
		t.Fatal(rows, e)
	}
}

func TestPageOptOutMounts(t *testing.T) {
	for _, name := range []string{"lanes.durations", "lanes.pane-column", "lanes.history-in-detail", "lanes.state-tones", "lanes.tree", "lanes.detail"} {
		t.Run(name, func(t *testing.T) {
			a := pageFixture(t)
			seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Root"},{"name":"Child","parent":"Root"}],"start":"Child"}`))
			if e := a.r.Settings().Set(name, false); e != nil {
				t.Fatal(e)
			}
			p, e := mountPage(a)
			if e != nil {
				t.Fatal(e)
			}
			p.m.Width, p.m.Height = 140, 34
			frame := ansi.Strip(p.m.View().Content)
			for _, text := range []string{"Root", "Child"} {
				if !strings.Contains(frame, text) {
					t.Fatal(frame)
				}
			}
			switch name {
			case "lanes.durations":
				if strings.Contains(frame, "Elapsed / duration") || strings.Contains(frame, "1h 30m") {
					t.Fatal(frame)
				}
			case "lanes.pane-column":
				for _, col := range p.v.List(listID).Columns() {
					if col.Title == "Panes" {
						t.Fatal(frame)
					}
				}
			case "lanes.tree":
				if a.r.Tree(listID) != nil {
					t.Fatal("flat setting retained tree")
				}
			case "lanes.detail":
				if p.v.Spec().Detail != nil {
					t.Fatal("retained detail")
				}
			case "lanes.history-in-detail":
				for _, section := range p.v.Spec().Detail.Sections {
					if section.Kind == "thread" {
						t.Fatal("retained history")
					}
				}
			case "lanes.state-tones":
				for _, row := range p.v.List(listID).Items() {
					for _, tone := range row.Tones {
						if tone.Cell == 1 {
							t.Fatal(row)
						}
					}
				}
			}
		})
	}
}
func TestPageSettingsRefreshAndNote(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Implementation"}],"start":"Implementation"}`))
	p, e := mountPage(a)
	if e != nil {
		t.Fatal(e)
	}
	p.m.Width, p.m.Height = 140, 34
	p.m.View()
	if e = a.r.Settings().Set("lanes.durations", false); e != nil {
		t.Fatal(e)
	}
	if e = a.r.Settings().Set("lanes.history-in-detail", false); e != nil {
		t.Fatal(e)
	}
	pageKey(t, p, "j")
	if strings.Contains(ansi.Strip(p.m.View().Content), "Elapsed / duration") {
		t.Fatal("duration setting did not refresh")
	}
	for _, section := range p.v.Spec().Detail.Sections {
		if section.Kind == "thread" {
			t.Fatal("history setting did not refresh")
		}
	}
	pageKey(t, p, "n")
	for _, c := range "Keep this note" {
		pageKey(t, p, string(c))
	}
	pageKey(t, p, "enter")
	q, e := a.store.Resolve("Implementation")
	if e != nil || q.History[len(q.History)-1].Text != "Keep this note" {
		t.Fatal(q, e)
	}
}
func TestPageStalePaneChoice(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "Implementation")
	f.a.run = func(context.Context, []string) ([]byte, error) { return nil, errors.New("chooser is offline") }
	// The picker may have closed since discovery. Saving an explicit selected
	// ref uses the same relation handler, without another daemon round trip.
	q := f.lane("choose-pane", "Implementation", "session%2Fone/window%3Ftwo?harness=codex", "--role", "reviewer")
	panes := q["panes"].([]any)
	pane := panes[0].(map[string]any)
	if pane["session"] != "session/one" || pane["window"] != "window?two" || pane["role"] != "reviewer" || pane["harness"] != "codex" {
		t.Fatal(panes)
	}
}

func TestStateReason(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e := func(id, kind, to, text string) Entry { return Entry{ID: id, At: at, Kind: kind, To: to, Text: text} }
	for _, c := range []struct {
		name    string
		history []Entry
		want    string
	}{
		{"none", nil, ""},
		{"own text", []Entry{e("1", "create", "planned", ""), e("2", "note", "", "older"), e("3", "state", "parked", "waiting on owner")}, "waiting on owner"},
		{"note before change", []Entry{e("1", "create", "planned", ""), e("2", "note", "", "first"), e("3", "note", "", "needs review"), e("4", "state", "review", "")}, "needs review"},
		{"note before previous change", []Entry{e("1", "create", "planned", ""), e("2", "note", "", "old"), e("3", "state", "running", ""), e("4", "state", "review", "")}, ""},
		{"note after change", []Entry{e("1", "create", "planned", ""), e("2", "state", "parked", ""), e("3", "note", "", "later")}, ""},
		{"undone note", []Entry{e("1", "create", "planned", ""), e("2", "note", "", "undone"), e("3", "state", "parked", ""), {ID: "4", Kind: "undo", Undo: &UndoRecord{Entries: []string{"2"}}}}, ""},
	} {
		if got := stateReason(c.history); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := stateAbout("planned", "", "why"); got != "" {
		t.Fatalf("no description shows %q", got)
	}
	if got := stateAbout("parked", "Paused.", "waiting"); got != "**parked** — Paused.\n\nNote: waiting" {
		t.Fatal(got)
	}
	if got := stateAbout("parked", "Paused.", ""); got != "**parked** — Paused." {
		t.Fatal(got)
	}
}
func TestPageStateDescriptions(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Fresh"},{"name":"Paused"}],"states":[{"lane":"Paused","state":"parked","note":"waiting on owner"}]}`))
	about := func() map[string]string {
		rows, e := laneRows(a.store, a.pageOptions())
		if e != nil {
			t.Fatal(e)
		}
		out := map[string]string{}
		for _, row := range rows {
			text, _ := row.Data.(map[string]any)["state_about"].(string)
			out[row.Label] = text
		}
		return out
	}
	got := about()
	if got["Fresh"] != "" || !strings.Contains(got["Paused"], "**parked** — Paused on purpose") || !strings.HasSuffix(got["Paused"], "Note: waiting on owner") {
		t.Fatal(got)
	}
	if lanePage(a.pageOptions()).Detail.Sections[0].Field != "state_about" {
		t.Fatal("state description section is not first")
	}
	// A custom state table supplies its own descriptions; rows without one
	// stay quiet, purely from data.
	a.store.States = []State{{Name: "planned", Open: true, Description: "Not started."}, {Name: "parked", Open: true}}
	if got = about(); got["Fresh"] != "**planned** — Not started." || got["Paused"] != "" {
		t.Fatal(got)
	}
	if e := a.r.Settings().Set("lanes.state-descriptions", false); e != nil {
		t.Fatal(e)
	}
	if got = about(); got["Fresh"] != "" {
		t.Fatal("opt-out ignored", got)
	}
	for _, section := range lanePage(a.pageOptions()).Detail.Sections {
		if section.Field == "state_about" {
			t.Fatal("section opt-out ignored")
		}
	}
}
