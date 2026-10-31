package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

func main() {
	os.Exit(oden.Report(os.Stderr, "o-lane", Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)))
}
func dataDir() (string, error) {
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
func globalData(args []string) ([]string, string, error) {
	var out []string
	dir := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			out = append(out, args[i:]...)
			break
		}
		if args[i] == "--data" {
			i++
			if i >= len(args) {
				return nil, "", errors.New("--data needs a directory")
			}
			dir = args[i]
			continue
		}
		if strings.HasPrefix(args[i], "--data=") {
			dir = strings.TrimPrefix(args[i], "--data=")
			continue
		}
		out = append(out, args[i])
	}
	var e error
	if dir == "" {
		dir, e = dataDir()
	} else {
		dir, e = filepath.Abs(dir)
	}
	return out, dir, e
}

func Main(args []string, in io.Reader, out, errOut io.Writer) error {
	args, dir, err := globalData(args)
	if err != nil {
		return err
	}
	a, err := newApp(dir, in, out, errOut)
	if err != nil {
		return err
	}
	return dispatch(a, args)
}

func dispatch(a *App, args []string) error {
	if len(args) == 0 {
		return runTUI(a, nil)
	}
	sh := a.shell()
	app := oden.New("o-lane", args, oden.WithBuiltins(), oden.WithStdio(a.in, a.out, a.errOut))
	ocli.Mount(app, sh)
	app.Add(oden.Command{Name: "view", Summary: "Open the lanes tree page", Run: func(_ context.Context, args []string) error { return runTUI(a, args) }})
	return app.Run(context.Background())
}
