package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

type geom struct {
	listW, listH, listRows       int
	detailW, detailH, detailRows int
	side                         bool
}

func (env *Env) geometry(a *App, w, h int) geom {
	if w <= 0 || h <= 0 {
		w, h = 120, 32
	}
	body := h - 3
	if a.Typing {
		body--
	}
	body = max(body, 4)
	g := geom{listW: w, listH: body, detailW: w, detailH: body}
	if w >= env.StackW {
		g.side = true
		g.listW = int(math.Round(float64(max(0, w-1)) * a.Ratio))
		g.detailW = max(0, w-1-g.listW)
	} else {
		g.listH = int(math.Round(float64(max(0, body-1)) * a.Ratio))
		g.detailH = max(0, body-1-g.listH)
	}
	g.listRows = max(g.listH-2, 1)
	g.detailRows = max(g.detailH-2, 1)
	return g
}

func textW(s string) int { return utf8.RuneCountInString(s) }

func textNode(t, tone string) ui.ViewNode {
	return ui.ViewNode{Kind: ui.KindText, Tone: tone, Props: map[string]any{"text": t}}
}

func clip(s string, w int) string {
	if r := []rune(s); w > 1 && len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s
}

func flatLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
}

func (env *Env) view(s core.State) ui.ViewNode {
	a := appOf(s)
	w := s.Width
	if w <= 0 {
		w = 120
	}
	g := env.geometry(&a, s.Width, s.Height)

	list := a.List.Copy()
	list.PageSize = g.listRows
	listNode := list.View(nodeList)
	listNode.Scope = "list"

	detail := a.Detail
	detail.PageSize = g.detailRows
	detailNode := detail.View(nodeDetail)

	axis := "horizontal"
	if !g.side {
		axis = "vertical"
	}
	body := ui.ViewNode{
		ID:       "body",
		Kind:     ui.KindSplit,
		Ratio:    a.Ratio,
		Gap:      1,
		Props:    map[string]any{"axis": axis},
		Children: []ui.ViewNode{listNode, detailNode},
	}

	children := []ui.ViewNode{a.titleLine(w), body}
	if a.Typing {
		children = append(children, a.inputNode())
	}
	children = append(children, a.infoLine(s, &list, w), env.statusLine(&a, w))
	root := ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{
		{ID: "page", Kind: ui.KindVStack, Children: children},
	}}
	for _, m := range s.Modals {
		root.Children = append(root.Children, env.modalView(s, m))
	}
	return root
}

func (a *App) titleLine(w int) ui.ViewNode {
	pane, tone := " LANES ", "badge"
	if a.Tab == tabPlans {
		pane = " PLANS "
	}
	if a.Focus == nodeDetail {
		pane, tone = " DETAIL ", "badge.accent"
	}
	parts := []string{" o-lane", fmt.Sprintf("%d lanes", len(a.Lanes)), fmt.Sprintf("%d plans", len(a.Plans))}
	if a.StatusFilter != "" {
		parts = append(parts, "status:"+a.StatusFilter)
	}
	if a.ProjectFilter != "" {
		parts = append(parts, "project:"+a.ProjectFilter)
	}
	if a.ShowArchived {
		parts = append(parts, "+archived")
	}
	line := strings.Join(parts, " · ") + " "
	from := textW(line)
	line += pane
	spans := []ui.ToneSpan{{From: 0, To: from, Tone: "title"}, {From: from, To: textW(line), Tone: tone}}
	n := textNode(clip(line, w), "title")
	n.Props["spans"] = spans
	return n
}

func (a *App) inputNode() ui.ViewNode {
	label := "/ filter [" + string(a.Match) + "]"
	line := a.QueryLine
	line.Sync(a.Query)
	return ui.ViewNode{ID: nodeFilter, Kind: ui.KindInput, Focusable: true, Title: label, Props: map[string]any{
		"value": a.Query, "cursor": line.Cursor,
		"placeholder": "type to filter · enter keeps · esc clears · tab/alt+m match mode · ↑↓ move",
	}}
}

func (a *App) infoLine(s core.State, list *table.Model, w int) ui.ViewNode {
	var parts []string
	items := list.Items()
	total := len(a.base)
	pos := "empty"
	if len(items) > 0 {
		pos = fmt.Sprintf("%d/%d", list.SelectedIdx+1, len(items))
	}
	if len(list.Filtered()) != total {
		pos += fmt.Sprintf(" (%d of %d)", len(list.Filtered()), total)
	}
	parts = append(parts, pos)

	tone := "muted"
	if a.Query != "" || a.Typing {
		parts = append(parts, fmt.Sprintf("filter %s %q", a.Match, a.Query))
	}
	if a.FilterErr != "" {
		parts = append(parts, "regex error: "+a.FilterErr)
		tone = "error"
	}
	if a.List.SortField != "" && a.List.SortDir != table.SortNone {
		parts = append(parts, sortLabel(a))
	}
	if a.List.GroupField != "" {
		parts = append(parts, "grouped by "+cellNames[a.List.GroupField])
	}
	if in := s.Input; in.Count != nil || len(in.Prefix) > 0 {
		p := strings.Join(in.Prefix, " ")
		if in.Count != nil {
			p = strconv.Itoa(*in.Count) + " " + p
		}
		parts = append(parts, "pending "+strings.TrimSpace(p))
	}
	return textNode(clip(" "+strings.Join(parts, " · "), w), tone)
}

func (env *Env) statusLine(a *App, w int) ui.ViewNode {
	if a.Status != "" {
		return textNode(clip(" "+flatLine(a.Status), w), a.Tone)
	}
	hint := " ? help · : commands · 1/2 lanes/plans · / filter · s sort · g b group · g s status · g p project · a define · c state · n note · p assoc · D archive · u/U undo/redo · q quit"
	if a.Focus == nodeDetail {
		hint = " DETAIL · j/k scroll · ]/[ headings · t toc · esc/tab list · ? help"
	}
	return textNode(clip(hint, w), "muted")
}

func formWindow(m *form.Model, h, errLines int) *form.Model {
	if h <= 0 {
		h = 32
	}
	n := len(m.Specs)
	size := max(h-2-1-2-errLines, 1)
	if n <= size {
		return m
	}
	start := min(max(m.ActiveIdx-size/2, 0), n-size)
	w := *m
	w.Specs = m.Specs[start : start+size]
	w.ActiveIdx = m.ActiveIdx - start
	w.Title = fmt.Sprintf("%s · fields %d-%d of %d", m.Title, start+1, start+size, n)
	return &w
}

func (env *Env) modalView(s core.State, f core.Frame) ui.ViewNode {
	switch f.ID {
	case modalPalette:
		m, _ := f.Data.(*picker.Model)
		if m == nil {
			break
		}
		return ui.ViewNode{ID: "palette.overlay", Kind: ui.KindOverlay, Scope: "palette", Title: "Command palette · " + string(m.MatchMode),
			Props:    map[string]any{"widthPct": 70, "heightPct": 70},
			Children: []ui.ViewNode{m.View(modalPalette), textNode("enter runs · esc closes · tab/alt+m match mode · ↑↓ move", "muted")}}
	case modalChoose:
		ch, ok := f.Data.(choice)
		if !ok || ch.M == nil {
			break
		}
		return ui.ViewNode{ID: "choose.overlay", Kind: ui.KindOverlay, Scope: "choose", Title: ch.M.Title + " · " + string(ch.M.MatchMode),
			Props:    map[string]any{"widthPct": 70, "heightPct": 70},
			Children: []ui.ViewNode{ch.M.View(modalChoose), textNode("enter chooses · esc closes · tab/alt+m match mode · ↑↓ move", "muted")}}
	case modalHelp:
		off, _ := f.Data.(int)
		lines := env.helpLines()
		off = min(max(off, 0), len(lines))
		return ui.ViewNode{ID: modalHelp, Kind: ui.KindOverlay, Scope: "help", Focusable: true, Title: "Help · active key bindings",
			Props:    map[string]any{"widthPct": 80, "heightPct": 85},
			Children: []ui.ViewNode{textNode(strings.Join(lines[off:], "\n"), "text")}}
	case modalForm:
		lf, ok := f.Data.(laneFormData)
		if !ok || lf.M == nil {
			break
		}
		var errs []ui.ViewNode
		for _, sp := range lf.M.Specs {
			if e := lf.M.Errors[sp.Name]; e != "" {
				errs = append(errs, textNode(sp.Name+": "+e, "error"))
			}
		}
		win := formWindow(lf.M, s.Height, len(errs))
		kids := append([]ui.ViewNode{win.View(modalForm)}, errs...)
		kids = append(kids, textNode("type to edit · readline keys · ctrl+u clears · tab/↑↓ field · enter applies · esc cancels", "muted"))
		return ui.ViewNode{ID: "form.overlay", Kind: ui.KindOverlay, Scope: "form", Title: win.Title, Props: map[string]any{"widthPct": 70}, Children: kids}
	}
	return ui.ViewNode{ID: f.ID + ".overlay", Kind: ui.KindOverlay, Children: []ui.ViewNode{textNode(f.ID, "muted")}}
}

// --- live help ------------------------------------------------------------------

type keyHelp struct{ Keys, Command, Desc string }

var helpSections = []struct {
	mode, title string
	chain       []core.ContextFrame
}{
	{"normal", "Lanes & Plans table", []core.ContextFrame{{Scope: "list"}, {Scope: "app"}}},
	{"normal", "Detail reader (tab / enter)", []core.ContextFrame{{Scope: "markdown"}, {Scope: "app"}}},
	{"input", "Filter input (/)", []core.ContextFrame{{Scope: "list"}, {Scope: "app"}}},
	{"palette", "Command palette (:)", []core.ContextFrame{{Scope: "palette"}, {Scope: "app"}}},
	{"choose", "Picker (c, g s, g p)", []core.ContextFrame{{Scope: "choose"}, {Scope: "app"}}},
	{"form", "Lane form (a, C, n, p)", []core.ContextFrame{{Scope: "form"}, {Scope: "app"}}},
	{"help", "Help", []core.ContextFrame{{Scope: "help"}, {Scope: "app"}}},
}

var classSamples = map[string]string{core.ClassDigit0: "0", core.ClassDigit: "5", core.ClassAlpha: "x", core.ClassPrintable: "x", core.ClassPaste: core.PasteKey, core.ClassAny: "x"}

func resolveWinner(km *core.Keymap, chain []core.ContextFrame, mode string, keys []string) *core.Binding {
	in := core.InputState{}
	for i, k := range keys {
		res := km.Resolve(core.KeyQuery{Chain: chain, Mode: mode, Input: in, Key: k, Text: k})
		in = res.Input
		last := i == len(keys)-1
		switch {
		case !last && res.Outcome == core.Pending:
		case last && (res.Outcome == core.Matched || res.Outcome == core.PushedLayer):
			return res.Trace.Winner
		case last && res.Outcome == core.Pending && res.Trace.Winner != nil:
			return res.Trace.Winner
		default:
			return nil
		}
	}
	return nil
}

func (env *Env) activeBindings(mode string, chain []core.ContextFrame) []keyHelp {
	var out []keyHelp
	seen := map[string]bool{}
	for _, bd := range env.Bindings {
		if bd.Hidden || bd.Unbind || (bd.Mode != mode && bd.Mode != "") || bd.Command == "" && bd.Layer == nil {
			continue
		}
		keys, label := bd.Keys, strings.Join(bd.Keys, " ")
		if len(keys) == 0 {
			sample, ok := classSamples[bd.Class]
			if !ok {
				continue
			}
			keys, label = []string{sample}, "‹"+bd.Class+"›"
			if bd.Class == core.ClassDigit0 {
				label = "0"
			}
		}
		if seen[label] {
			continue
		}
		win := resolveWinner(env.Km, chain, mode, keys)
		if win == nil || win.Command != bd.Command || !slices.Equal(win.Keys, bd.Keys) || win.Class != bd.Class {
			continue
		}
		seen[label] = true
		desc := bd.Hint
		if spec, ok := env.Km.Commands.Lookup(bd.Command); ok && spec.Description != "" {
			desc = spec.Description
		}
		out = append(out, keyHelp{Keys: label, Command: bd.Command, Desc: desc})
	}
	return out
}

func keysByCommand(env *Env) map[string][]string {
	out := map[string][]string{}
	for _, sec := range helpSections {
		if sec.mode != "normal" {
			continue
		}
		for _, k := range env.activeBindings("normal", sec.chain) {
			if !slices.Contains(out[k.Command], k.Keys) {
				out[k.Command] = append(out[k.Command], k.Keys)
			}
		}
	}
	return out
}

func (env *Env) helpLines() []string {
	var lines []string
	for _, sec := range helpSections {
		bs := env.activeBindings(sec.mode, sec.chain)
		if len(bs) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, sec.title)
		for _, k := range bs {
			lines = append(lines, fmt.Sprintf("  %-12s %s  (%s)", k.Keys, k.Desc, k.Command))
		}
	}
	lines = append(lines, "",
		"Counts prefix motions: 10j, 5G. Readline keys work in every input, picker and form.",
		"User bindings, theme and settings load from $XDG_CONFIG_HOME/o-lane or ~/.config/o-lane.")
	for _, n := range env.Notes {
		lines = append(lines, "config: "+n)
	}
	return lines
}
