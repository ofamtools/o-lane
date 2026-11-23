// Command o-lane tracks work lanes and plans, built on Ochazuke v2.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/oden"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func progName() string {
	if parent := os.Getenv("ODEN_PARENT"); parent != "" {
		return parent + " lane"
	}
	return "o-lane"
}

func run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	argv, dataFlag, err := splitDataArgs(argv)
	if err != nil {
		fmt.Fprintln(stderr, "o-lane:", err)
		return 2
	}
	app := newOdenApp(argv, dataFlag, stdin, stdout, stderr)
	return oden.Report(stderr, progName(), app.Run(context.Background()))
}

func newOdenApp(argv []string, dataFlag string, stdin io.Reader, stdout, stderr io.Writer) *oden.App {
	openCLI := func() (*cli, error) {
		_, set := loadConfig(configDir())
		st, err := openStore(dataFlag, set, stderr)
		if err != nil {
			return nil, err
		}
		return &cli{
			st:          st,
			plansDir:    set.String("lanes.plans-dir", ""),
			importCols:  set.String("lanes.import-columns", defaultImportColumns),
			hideArchive: set.Bool("lanes.hide-archived", true),
			stdin:       stdin,
			stdout:      stdout,
			stderr:      stderr,
		}, nil
	}

	exportConfig := func(jsonl bool) ([]byte, error) {
		dir, _ := dataDir(dataFlag)
		cfg := map[string]any{
			"name":     "o-lane",
			"version":  Version,
			"config":   configDir(),
			"data":     dir,
			"settings": defaultSettings,
		}
		if jsonl {
			var b strings.Builder
			for _, k := range []string{"name", "version", "config", "data", "settings"} {
				line, err := json.Marshal(map[string]any{"key": k, "value": cfg[k]})
				if err != nil {
					return nil, err
				}
				b.Write(line)
				b.WriteByte('\n')
			}
			return []byte(b.String()), nil
		}
		return json.MarshalIndent(cfg, "", "  ")
	}

	info := ocli.AppInfo{
		Name:         "o-lane",
		Version:      Version,
		Summary:      "Work lane and plan tracker",
		Modes:        func() []core.Mode { return modes },
		Bindings:     func() []core.Binding { return defaultBindings() },
		Specs:        func() []core.CommandSpec { return commandSpecs() },
		ExportConfig: exportConfig,
		RunTUI: func(_ context.Context, _ []string) error {
			cfg, set := loadConfig(configDir())
			st, err := openStore(dataFlag, set, stderr)
			if err != nil {
				return err
			}
			env := newEnv(st, cfg)
			sh := newShell(env)
			_, err = tea.NewProgram(sh).Run()
			return err
		},
		RunSmoke: func(out io.Writer, _ bool) error {
			if code := runSmoke(out, stderr); code != 0 {
				return &oden.ExitError{Code: code}
			}
			return nil
		},
		Manifest: extManifest(),
		Capabilities: &oden.Capabilities{
			Name:         "o-lane",
			Version:      Version,
			Summary:      "Work lane and plan tracker",
			Capabilities: []string{"query", "catalog", "tui"},
		},
		Query: func(_ context.Context, kind string, args []string) (oden.ExtQueryResult, error) {
			c, err := openCLI()
			if err != nil {
				return oden.ExtQueryResult{}, err
			}
			return extQueryStore(c.st, kind, args)
		},
	}

	app := ocli.NewApp(info, argv, oden.WithStdio(stdin, stdout, stderr))

	laneChild := func(name string, aliases []string, summary, usage string, fn func(*cli, []string) error) oden.Command {
		return oden.Command{
			Name:    name,
			Aliases: aliases,
			Summary: summary,
			Usage:   usage,
			Run: func(_ context.Context, args []string) error {
				c, err := openCLI()
				if err != nil {
					return err
				}
				return fn(c, args)
			},
		}
	}

	app.Add(
		oden.Command{
			Name:    "lane",
			Summary: "define, inspect, and update work lanes",
			Children: []oden.Command{
				laneChild("define", []string{"add"}, "define one or more lanes", "[NAME] [--parent P] [--project P] [--plan FILE] [--lanes JSON|-] [--dry-run]", func(c *cli, args []string) error { return c.laneDefine(args) }),
				laneChild("list", []string{"ls"}, "list lanes with optional filters", "[--status S] [--project P] [--archived] [--format table|json]", func(c *cli, args []string) error { return c.laneList(args) }),
				laneChild("show", nil, "show full details, rollup, and history for a lane", "ID [--json]", func(c *cli, args []string) error { return c.laneShow(args) }),
				laneChild("state", []string{"status"}, "set a lane's lifecycle status", "ID STATUS [--note TEXT]", func(c *cli, args []string) error { return c.laneState(args) }),
				laneChild("note", []string{"annotate"}, "append a note to a lane's history", "ID TEXT", func(c *cli, args []string) error { return c.laneNote(args) }),
				laneChild("associate", nil, "associate a project, pane, or agent with a lane", "ID [--project REF] [--pane WIN] [--session S] [--host H] [--role R] [--remove]", func(c *cli, args []string) error { return c.laneAssociate(args, false) }),
				laneChild("unassociate", nil, "remove a project, pane, or agent association", "ID [--project REF] [--pane WIN]", func(c *cli, args []string) error { return c.laneAssociate(args, true) }),
				laneChild("archive", nil, "archive a lane or finished trees", "[ID] [--finished-trees] [--restore] [--dry-run] [--note TEXT]", func(c *cli, args []string) error { return c.laneArchive(args, false) }),
				laneChild("restore", nil, "restore an archived lane", "ID [--note TEXT]", func(c *cli, args []string) error { return c.laneArchive(args, true) }),
				laneChild("undo", nil, "undo the most recent store mutation", "", func(c *cli, args []string) error { return c.cmdLane(append([]string{"undo"}, args...)) }),
				laneChild("redo", nil, "redo the most recently undone mutation", "", func(c *cli, args []string) error { return c.cmdLane(append([]string{"redo"}, args...)) }),
				laneChild("rename", nil, "rename a lane", "ID NEW_NAME", func(c *cli, args []string) error { return c.cmdLane(append([]string{"rename"}, args...)) }),
				laneChild("reword", nil, "update a lane's owner wording", "ID TEXT", func(c *cli, args []string) error { return c.cmdLane(append([]string{"reword"}, args...)) }),
				laneChild("move", nil, "reparent a lane", "ID [PARENT]", func(c *cli, args []string) error { return c.cmdLane(append([]string{"move"}, args...)) }),
				laneChild("delete", nil, "delete a lane", "ID", func(c *cli, args []string) error { return c.cmdLane(append([]string{"delete"}, args...)) }),
			},
		},
		oden.Command{
			Name:    "plan",
			Summary: "import and inspect Markdown plans",
			Children: []oden.Command{
				laneChild("define", nil, "import a Markdown plan's Lanes table", "FILE [--name NAME] [--project REF] [--wording TEXT] [--dry-run]", func(c *cli, args []string) error { return c.cmdPlan(append([]string{"define"}, args...)) }),
				laneChild("list", []string{"ls"}, "list plans referenced in the store", "[--format table|json]", func(c *cli, args []string) error { return c.cmdPlan(append([]string{"list"}, args...)) }),
				laneChild("show", nil, "show a plan's summary and constituent lanes", "PLAN [--json]", func(c *cli, args []string) error { return c.cmdPlan(append([]string{"show"}, args...)) }),
			},
		},
		oden.Command{
			Name:    "rollup",
			Summary: "show subtree rollup counts and rolled statuses",
			Usage:   "[ID] [--project REF] [--format table|json]",
			Run: func(_ context.Context, args []string) error {
				c, err := openCLI()
				if err != nil {
					return err
				}
				return c.cmdRollup(args)
			},
		},
	)

	app.HelpText = usageText()
	return app
}

func dataDir(flagVal string) (string, error) {
	if flagVal != "" {
		return filepath.Abs(flagVal)
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		base = filepath.Join(h, ".local", "share")
	}
	return filepath.Abs(filepath.Join(base, "o-lane"))
}

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "o-lane")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "o-lane")
	}
	return ""
}

func loadConfig(dir string) (Config, *config.Settings) {
	var cfg Config
	read := func(name string) []byte {
		if dir == "" {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			cfg.Notes = append(cfg.Notes, name+": "+err.Error())
		}
		return data
	}
	if data := read("bindings.json"); data != nil {
		if bs, err := config.LoadBindings(data); err != nil {
			cfg.Notes = append(cfg.Notes, err.Error())
		} else {
			cfg.Bindings = bs
		}
	}
	if data := read("themes.json"); data != nil {
		if th, err := config.LoadTheme(data, ui.DefaultDarkTheme()); err != nil {
			cfg.Notes = append(cfg.Notes, err.Error())
		} else {
			cfg.Theme = &th
		}
	}
	set, err := config.LoadSettings(read("settings.json"), defaultSettings)
	if err != nil {
		cfg.Notes = append(cfg.Notes, err.Error())
		set, _ = config.LoadSettings(nil, defaultSettings)
	}
	cfg.Settings = set
	return cfg, set
}

func openStore(dataFlag string, set *config.Settings, stderr io.Writer) (*Store, error) {
	dir, err := dataDir(dataFlag)
	if err != nil {
		return nil, err
	}
	actor, err := cliActor(os.Getenv)
	if err != nil {
		return nil, err
	}
	st := &Store{
		Dir:         dir,
		Actor:       actor,
		BackupDaily: set.Bool("backup.daily", true),
		BackupKeep:  set.Int("backup.keep", 14),
		BackupDays:  set.Int("retention.backups.days", 14),
		HistoryKeep: set.Int("history.keep", 200),
		PanesShared: !set.Bool("lanes.pane-one-lane", true),
		Warn:        func(msg string) { fmt.Fprintln(stderr, msg) },
	}
	if raw := set.String("lanes.statuses", defaultStatuses); raw != "" {
		if rows, err := parseStatuses(raw); err == nil {
			st.Statuses = rows
		}
	}
	if raw := set.String("lanes.status-presets", defaultStatusPresets); raw != "" {
		var presets map[string]string
		if json.Unmarshal([]byte(raw), &presets) == nil {
			st.StatusPresets = presets
		}
	}
	st.RestorePresets = set.Strings("lanes.restore-presets", []string{"reopen"})
	return st, nil
}

// --- smoke ---------------------------------------------------------------------

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;:?]*[A-Za-z]|\x1b\][^\a\x1b]*(\a|\x1b\\)`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

func maxLineW(s string) int {
	w := 0
	for _, l := range strings.Split(s, "\n") {
		w = max(w, textW(l))
	}
	return w
}

// runSmoke drives a scripted headless 120x32 session over a temporary --data
// directory through the real keymap, engine, shell and renderer.
func runSmoke(stdout, stderr io.Writer) int {
	tmp, err := os.MkdirTemp("", "o-lane-smoke-*")
	if err != nil {
		fmt.Fprintln(stderr, "smoke:", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	data := filepath.Join(tmp, "data")
	planFile := filepath.Join(tmp, "migration-plan.md")
	planSrc := strings.Join([]string{
		"# Ochazuke v2 Migration",
		"",
		"## Lanes",
		"",
		"| Lane | Scope | Repo | Estimate |",
		"| --- | --- | --- | --- |",
		"| Core domain | Preserve store and rollup | o-lane | 1h |",
		"| TUI shell | Rebuild on Ochazuke v2 | o-lane | 2h |",
	}, "\n") + "\n"
	if err := os.WriteFile(planFile, []byte(planSrc), 0o644); err != nil {
		fmt.Fprintln(stderr, "smoke:", err)
		return 1
	}

	st := &Store{Dir: data, Actor: Owner(), BackupDaily: false}
	cols, _ := parseImportColumns(defaultImportColumns)
	if _, _, err := defineFromPlan(st, planFile, "", "", "migrate o-lane to v2", "", "ofam", false, cols); err != nil {
		fmt.Fprintln(stderr, "smoke:", err)
		return 1
	}
	if _, err := st.Status("core-domain", "done", "store tests green"); err != nil {
		fmt.Fprintln(stderr, "smoke:", err)
		return 1
	}
	if _, err := st.Status("tui-shell", "running", "building TUI"); err != nil {
		fmt.Fprintln(stderr, "smoke:", err)
		return 1
	}
	// Custom unknown status must never be refused.
	if _, err := st.Add(Spec{Name: "Experimental Spike", Project: "research"}); err != nil {
		fmt.Fprintln(stderr, "smoke:", err)
		return 1
	}
	if _, err := st.Status("experimental-spike", "incubating", "custom status accepted"); err != nil {
		fmt.Fprintln(stderr, "smoke:", err)
		return 1
	}

	env := newEnv(st, Config{})
	sh := newShell(env)
	d := newDriver(sh, 120, 32)
	app := func() App { return appOf(sh.State) }
	frame := func() string { return plain(sh.View().Content) }

	var results []string
	fails := 0
	check := func(name string, ok bool) {
		mark := "ok  "
		if !ok {
			mark, fails = "FAIL", fails+1
		}
		results = append(results, mark+" "+name)
	}

	f := frame()
	check("frame is 120x32", len(strings.Split(f, "\n")) == 32 && maxLineW(f) <= 120)
	check("loads 4 lanes and 1 plan", len(app().Lanes) == 4 && len(app().Plans) == 1 && strings.Contains(f, " LANES "))

	// Filter with / and readline ctrl+u, then esc.
	d.keys("/")
	d.typeText("Spike")
	check("/ filters lanes while typing", app().Typing && app().filteredCount() == 1)
	d.keys("ctrl+u")
	check("readline ctrl+u clears filter", app().Query == "" && app().filteredCount() == 4)
	d.keys("esc")
	check("esc exits filter mode", !app().Typing)

	// Sort and group.
	d.keys("s")
	check("s sorts by name", app().List.SortField == cellName && app().List.SortDir == table.SortAsc)
	d.keys("g", "b")
	check("g b groups by status", app().List.GroupField == cellStatus)
	d.keys("g", "b", "g", "b", "g", "b")
	check("g b cycles back to ungrouped", app().List.GroupField == "")

	// Switch to Plans tab and back.
	d.keys("2")
	check("2 switches to plans tab", app().Tab == tabPlans && strings.Contains(frame(), " PLANS ") && app().filteredCount() == 1)
	d.keys("1")
	check("1 returns to lanes tab", app().Tab == tabLanes)

	// Define a lane via form (`a`), then undo (`u`) and redo (`U`).
	d.keys("a")
	d.typeText("Smoke Lane")
	d.keys("enter")
	check("a defines lane via form", len(app().Lanes) == 5)
	d.keys("u")
	check("u undoes lane define", len(app().Lanes) == 4)
	d.keys("U")
	check("U redoes lane define", len(app().Lanes) == 5)

	// Focus detail pane and return.
	d.keys("enter")
	check("enter focuses detail pane", app().Focus == nodeDetail && strings.Contains(frame(), " DETAIL "))
	d.keys("esc")
	check("esc returns to table", app().Focus == nodeList)

	// Palette & help.
	d.keys(":")
	check(": opens palette", len(sh.State.Modals) == 1 && sh.State.Modals[0].ID == modalPalette)
	d.keys("esc", "?")
	check("? opens live help", len(sh.State.Modals) == 1 && strings.Contains(frame(), "filter.open"))
	d.keys("esc")

	// Catalog & query wire protocol check.
	catBytes, err := json.Marshal(manifest())
	check("catalog declares lane.lane and lane.plan", err == nil && strings.Contains(string(catBytes), laneKind) && strings.Contains(string(catBytes), planKind))
	qr, err := queryStore(st, "incubating", laneKind, false)
	check("query returns custom-status lane record", err == nil && qr.Total == 1 && qr.Items[0].Kind == laneKind)

	f = frame()
	for _, line := range strings.Split(f, "\n") {
		fmt.Fprintln(stdout, strings.TrimRight(line, " "))
	}
	fmt.Fprintln(stdout)
	for _, r := range results {
		fmt.Fprintln(stdout, r)
	}
	fmt.Fprintf(stdout, "smoke: %d/%d checks passed · %d lanes · %d plans · %d renders\n", len(results)-fails, len(results), len(app().Lanes), len(app().Plans), sh.Renders)
	if fails > 0 {
		return 1
	}
	return 0
}
