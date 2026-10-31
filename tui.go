package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

type page struct {
	a             *App
	m             *ochazuke.Model
	v             *ocli.PageView
	dirty         bool
	streaming     bool
	cancelLive    context.CancelFunc
	readerContext context.Context
	liveDone      chan struct{}
	generation    uint64
}
type pageTick struct{}
type liveChanged struct{ generation uint64 }

func mountPage(a *App) (*page, error) {
	if e := a.configure(); e != nil {
		return nil, e
	}
	data, e := ocli.Catalog(a.r, "o-lane", version)
	if e != nil {
		return nil, e
	}
	var doc ocli.CatalogDoc
	if e = json.Unmarshal(data, &doc); e != nil {
		return nil, e
	}
	// Refresh settings-dependent declarations before mounting, including when
	// a story supplies session values after constructing the app.
	spec := lanePage(a.pageOptions())
	for i := range doc.Nouns {
		if doc.Nouns[i].Name == "lane" {
			doc.Nouns[i].Columns = a.pageOptions().columns()
		}
	}
	m := ochazuke.NewModel(a.r)
	m.Title = "o-lane"
	v, e := ocli.MountPage(m, doc, spec, ocli.PageConfig{})
	if e != nil {
		return nil, e
	}
	p := &page{a: a, m: m, v: v}
	// The initial projection seeds the cache before the first frame. Its reader
	// starts only for an interactive page, never for catalog/CLI/smoke calls.
	if a.r.Settings().Bool("lanes.live") {
		lanes, err := a.store.List()
		if err != nil {
			return nil, err
		}
		panes := allPanes(lanes)
		a.reader = newLiveReader(a.run, panes, snapshotScopes(context.Background(), panes, a.run))
	}
	a.changed = func() { p.dirty = true }
	a.rowsLoaded = func(rows []ocli.Row, err error) {
		if err == nil {
			err = p.v.SetRows(listID, rows)
		}
		if err != nil {
			a.r.UI().Status(err.Error())
		}
	}
	for _, name := range []string{"lanes.durations", "lanes.hide-closed-roots", "lanes.pane-column", "lanes.state-tones", "lanes.history-in-detail", "lanes.detail", "lanes.live", "lanes.live-tones", "lanes.live-states", "lanes.live-precedence", "lanes.live-roles"} {
		a.r.Settings().Observe(name, func(string, any) { p.dirty = true })
	}
	a.showUI = func(q View) error {
		for _, item := range p.v.List(listID).Items() {
			if item.ID == listID+"/"+q.ID {
				return openLaneDetail(a.r, q.Name, item.Props)
			}
		}
		return nil
	}

	quit := ochazuke.Do("app.quit")
	quit.Instance = pageID
	quit.Mode = "normal"
	quit.Keys = "q"
	if e = a.r.Bind(quit); e != nil {
		return nil, e
	}
	if e = a.r.OpenPage(pageID); e != nil {
		return nil, e
	}
	rows, e := loadLaneRows(context.Background(), a.store, a.pageOptions(), a.reader, a.run)
	if e != nil {
		return nil, e
	}
	if e = v.SetRows(listID, rows); e != nil {
		return nil, e
	}
	if e = v.List(listID).Focus(); e != nil {
		return nil, e
	}
	return p, nil
}
func (p *page) reload() tea.Cmd {
	o := p.a.pageOptions()
	p.v.List(listID).SetColumns(o.columns()...)
	if current, next := p.v.Spec().Detail, lanePage(o).Detail; current != nil && next != nil {
		*current = *next
	}
	if _, err := p.a.r.Execute(ochazuke.Request{Noun: "lane", Verb: "rows", Target: pageID}); err != nil {
		p.a.r.UI().Status(err.Error())
	}
	return p.a.r.PendingCommands()
}
func (p *page) tick() tea.Cmd {
	every := p.a.r.Settings().Duration("lanes.refresh")
	if every <= 0 {
		return nil
	}
	return tea.Tick(every, func(time.Time) tea.Msg { return pageTick{} })
}
func (p *page) Init() tea.Cmd {
	return tea.Batch(p.m.Init(), p.a.r.PendingCommands(), p.tick(), p.syncLive())
}
func (p *page) stopLive() {
	if p.cancelLive != nil {
		p.cancelLive()
		p.cancelLive = nil
	}
	p.a.reader = nil
	p.generation++
}
func (p *page) syncLive() tea.Cmd {
	if !p.streaming {
		return nil
	}
	if !p.a.r.Settings().Bool("lanes.live") {
		if p.cancelLive != nil {
			p.stopLive()
		}
		return nil
	}
	if p.cancelLive != nil {
		return nil
	}
	p.generation++
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelLive = cancel
	p.readerContext = ctx
	reader := p.a.reader
	if reader == nil {
		reader = newLiveReader(p.a.run, nil, liveSnapshots{})
		p.a.reader = reader
	}
	previous := p.liveDone
	p.liveDone = make(chan struct{})
	done := p.liveDone
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		reader.Run(ctx)
	}()
	return p.waitLive(reader, p.generation, ctx)
}
func (p *page) waitLive(reader *liveReader, generation uint64, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-reader.changed:
			return liveChanged{generation}
		case <-ctx.Done():
			return nil
		}
	}
}
func (p *page) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if change, ok := msg.(liveChanged); ok {
		if change.generation != p.generation || p.cancelLive == nil {
			return p, nil
		}
		reader := p.a.reader
		// The wait is cancelled with the reader; keep its context independent of
		// framework jobs so cancelling a replaced row load cannot stop the stream.
		return p, tea.Batch(p.reload(), p.waitLive(reader, p.generation, p.readerContext))
	}
	if _, ok := msg.(pageTick); ok {
		return p, tea.Batch(p.reload(), p.tick())
	}
	_, cmd := p.m.Update(msg)
	if p.dirty {
		p.dirty = false
		cmd = tea.Batch(cmd, p.syncLive(), p.reload())
	}
	return p, cmd
}
func (p *page) View() tea.View { return p.m.View() }
func runTUI(a *App, args []string) error {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	smoke := fs.Bool("smoke", false, "Render one frame and exit")
	w := fs.Int("width", 120, "Frame width")
	h := fs.Int("height", 30, "Frame height")
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if fs.NArg() > 0 {
		return errors.New("unexpected view arguments")
	}
	p, e := mountPage(a)
	if e != nil {
		return e
	}
	if *smoke {
		p.m.Width, p.m.Height = *w, *h
		_, e = fmt.Fprintln(a.out, strings.TrimRight(ansi.Strip(p.m.View().Content), "\n"))
		return e
	}
	p.streaming = true
	defer func() {
		p.stopLive()
		if p.liveDone != nil {
			<-p.liveDone
		}
	}()
	_, e = tea.NewProgram(p, tea.WithInput(a.in), tea.WithOutput(a.out)).Run()
	return e
}

// The modal uses the framework's scroll window and binding rows, as o-questions
// does; the portable detail sections remain the page's main inspection view.
type laneDetail struct {
	title, text string
	detail      *ochazuke.Detail
}

func (v *laneDetail) ModalTitle() string { return v.title }
func (v *laneDetail) ModalBlock(_ ochazuke.Styles, w, h int) ochazuke.Block {
	body, _ := v.detail.Window(ansi.Hardwrap(v.text, w, true), 0, h)
	return ochazuke.Text(body)
}
func openLaneDetail(r *ochazuke.Runtime, title string, data any) error {
	inst, e := r.OpenModal("lane-detail", ochazuke.Payload{}, ochazuke.ReplaceOpen())
	if e != nil {
		return e
	}
	d, e := r.AddDetail("lane-detail/text", nil, ochazuke.Parent(inst.ID))
	if e != nil {
		_ = r.Pop()
		return e
	}
	inst.Component = &laneDetail{title: title, text: laneDetailText(data), detail: d}
	return nil
}

func laneDetailText(value any) string {
	data, _ := value.(map[string]any)
	var text strings.Builder
	for _, name := range []string{"name", "state", "live_state", "wording", "scope", "done_when", "pane_detail", "repo", "base", "branch", "worktree", "estimate", "duration", "depends", "plan", "task", "tests", "links", "started", "finished"} {
		v := data[name]
		if v == nil || v == "" {
			continue
		}
		fmt.Fprintf(&text, "%s\n%v\n\n", strings.ReplaceAll(name, "_", " "), v)
	}
	if entries, ok := data["history"].([]any); ok {
		text.WriteString("History\n")
		for _, entry := range entries {
			en, _ := entry.(map[string]any)
			fmt.Fprintf(&text, "%v · %v\n%v\n\n", en["author"], en["at"], en["body"])
		}
	}
	return text.String()
}
