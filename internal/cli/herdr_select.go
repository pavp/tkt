package cli

import (
	"context"
	"errors"
	"time"

	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdrcore"
	"github.com/pavp/tkt/internal/herdrport"
)

type herdrFlavor string

const (
	herdrClassic herdrFlavor = "Herdr"
	herdrDesktop herdrFlavor = "Herdr desktop"
)

type herdrChoice struct {
	flavor herdrFlavor
	// why is what decided the choice; empty when nothing did and tkt keeps Herdr, as before Herdr desktop.
	why string
}

var errBothHerdrs = errors.New("both Herdr and Herdr desktop are running: run tkt from a terminal inside the one to use")

// inDesktopTerminal reports whether tkt runs in a Herdr desktop terminal, which sets these variables.
func (a *App) inDesktopTerminal() bool {
	return a.Env.Getenv("HC_TERMINAL_ID") != "" || a.Env.Getenv("HC_TERMINAL_SOCKET") != "" || a.Env.Getenv("HC_SCOPE") != ""
}

// inHerdr reports whether tkt runs in a terminal of either Herdr.
func (a *App) inHerdr() bool { return a.inDesktopTerminal() || a.Env.Getenv("HERDR_ENV") == "1" }

// inHerdrPlugins reports whether tkt runs inside Herdr 0.9.5, the only Herdr with plugin panes.
func (a *App) inHerdrPlugins() bool {
	return !a.inDesktopTerminal() && a.Env.Getenv("HERDR_ENV") == "1"
}

// whichHerdr picks the Herdr whose terminal runs tkt, else the only one running; it never guesses between two.
func (a *App) whichHerdr(ctx context.Context) (herdrChoice, error) {
	const inTerminal, onlyOne = "running in one of its terminals", "the only one running"
	switch {
	case a.inDesktopTerminal():
		return herdrChoice{herdrDesktop, inTerminal}, nil
	case a.Env.Getenv("HERDR_ENV") == "1", a.Env.Getenv("HERDR_PLUGIN_ID") != "":
		return herdrChoice{herdrClassic, inTerminal}, nil
	case a.Env.Getenv("HERDR_SOCKET_PATH") != "" || a.HerdrRunning == nil:
		return herdrChoice{herdrClassic, ""}, nil
	}
	classic, desktop := a.HerdrRunning(ctx)
	switch {
	case classic && desktop:
		return herdrChoice{}, errBothHerdrs
	case classic:
		return herdrChoice{herdrClassic, onlyOne}, nil
	case desktop:
		return herdrChoice{herdrDesktop, onlyOne}, nil
	}
	return herdrChoice{herdrClassic, ""}, nil
}

func (a *App) chosenHerdr() HerdrAPI {
	ch, err := a.whichHerdr(context.Background())
	if err != nil {
		return failingHerdr{err}
	}
	if ch.flavor == herdrDesktop {
		c, err := herdrcore.New()
		if err != nil {
			return failingHerdr{err}
		}
		return c
	}
	return herdr.New(herdr.SocketPath(a.Env.Getenv, a.Env.Home))
}

func (a *App) herdrRunning(ctx context.Context) (classic, desktop bool) {
	classic = herdr.Reachable(herdr.SocketPath(a.Env.Getenv, a.Env.Home))
	if c, err := herdrcore.New(); err == nil {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		desktop = c.Running(ctx)
	}
	return classic, desktop
}

// failingHerdr stands for a Herdr tkt cannot use: every call reports why.
type failingHerdr struct{ err error }

func (f failingHerdr) Workspaces(context.Context) ([]herdrport.Workspace, error) { return nil, f.err }
func (f failingHerdr) CreateWorkspace(context.Context, string, string, bool) (herdrport.Workspace, herdrport.Pane, error) {
	return herdrport.Workspace{}, herdrport.Pane{}, f.err
}
func (f failingHerdr) FocusWorkspace(context.Context, string) error { return f.err }
func (f failingHerdr) FocusPane(context.Context, string) error      { return f.err }
func (f failingHerdr) ReportWorkspaceTokens(context.Context, string, string, map[string]*string, time.Duration) error {
	return f.err
}
func (f failingHerdr) ReportPaneTokens(context.Context, string, string, map[string]*string) error {
	return f.err
}
func (f failingHerdr) Agents(context.Context) ([]herdrport.Agent, error) { return nil, f.err }
func (f failingHerdr) Panes(context.Context) ([]herdrport.Pane, error)   { return nil, f.err }
func (f failingHerdr) StartAgent(context.Context, string, string, string, []string) error {
	return f.err
}
func (f failingHerdr) WaitAgent(context.Context, string, string, time.Duration) error { return f.err }
func (f failingHerdr) SplitPane(context.Context, string, string) (herdrport.Pane, error) {
	return herdrport.Pane{}, f.err
}
func (f failingHerdr) Prompt(context.Context, string, string) error { return f.err }
func (f failingHerdr) Watch(context.Context, []herdrport.Subscription, func(herdrport.Event)) error {
	return f.err
}
func (f failingHerdr) ClosePane(context.Context, string) error              { return f.err }
func (f failingHerdr) CloseWorkspace(context.Context, string) error         { return f.err }
func (f failingHerdr) Notify(context.Context, string, string) error         { return f.err }
func (f failingHerdr) OpenPluginPane(context.Context, herdr.PaneOpen) error { return f.err }
func (f failingHerdr) FocusPluginPane(context.Context, string) error        { return f.err }
func (f failingHerdr) ClosePluginPane(context.Context, string) error        { return f.err }
func (f failingHerdr) LinkPlugin(context.Context, string) error             { return f.err }
func (f failingHerdr) UnlinkPlugin(context.Context, string) error           { return f.err }
func (f failingHerdr) ReloadConfig(context.Context) error                   { return f.err }

// herdrDoctorDetail says which Herdr tkt uses, or why it will not choose, after the herdr check's detail.
func (a *App) herdrDoctorDetail(ctx context.Context, c check) check {
	ch, err := a.whichHerdr(ctx)
	switch {
	case err != nil:
		c.Status, c.Detail = statusWarn, c.Detail+" · "+err.Error()
	case ch.why != "":
		c.Detail += " · tkt uses " + string(ch.flavor) + " (" + ch.why + ")"
	}
	return c
}
