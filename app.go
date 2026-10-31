package main

import (
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"io"
)

const version = "0.1.0-dev"

type App struct {
	dir         string
	r           *ochazuke.Runtime
	in          io.Reader
	out, errOut io.Writer
}

func newApp(dir string, in io.Reader, out, errOut io.Writer) (*App, error) {
	a := &App{dir: dir, r: ochazuke.NewRuntime(), in: in, out: out, errOut: errOut}
	a.r.Config().App = "o-lane"
	if err := a.r.DeclareSetting(appSettings()...); err != nil {
		return nil, err
	}
	a.r.Define("lane").Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return ochazuke.Done([]any{}) }, "List lanes").Fetch().Agent(ochazuke.AgentAllow)
	return a, nil
}
func (a *App) shell() *ocli.Shell {
	sh := ocli.New(a.r, "o-lane", a.in, a.out, a.errOut)
	sh.Version = version
	return sh
}
