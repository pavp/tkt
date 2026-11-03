package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdrcore"
	"github.com/pavp/tkt/internal/paths"
	"github.com/pavp/tkt/internal/tui"
)

func selectApp(env map[string]string, classic, desktop bool) *App {
	return &App{
		Env:          paths.Env{GOOS: "linux", Home: "/home/dev", Getenv: func(k string) string { return env[k] }},
		HerdrRunning: func(context.Context) (bool, bool) { return classic, desktop },
	}
}

func TestWhichHerdr(t *testing.T) {
	tests := []struct {
		name             string
		env              map[string]string
		classic, desktop bool
		want             herdrFlavor
		why              string
		err              error
	}{
		{"desktop terminal", map[string]string{"HC_TERMINAL_ID": "t3"}, true, true, herdrDesktop, "running in one of its terminals", nil},
		{"desktop scope only", map[string]string{"HC_SCOPE": "s1"}, false, false, herdrDesktop, "running in one of its terminals", nil},
		{"desktop socket only", map[string]string{"HC_TERMINAL_SOCKET": "/run/hc.sock"}, false, false, herdrDesktop, "running in one of its terminals", nil},
		{"herdr terminal", map[string]string{"HERDR_ENV": "1"}, true, true, herdrClassic, "running in one of its terminals", nil},
		{"herdr socket set", map[string]string{"HERDR_SOCKET_PATH": "/x/herdr.sock"}, false, true, herdrClassic, "", nil},
		{"both terminals prefer the desktop one", map[string]string{"HERDR_ENV": "1", "HC_TERMINAL_ID": "t3"}, false, false, herdrDesktop, "running in one of its terminals", nil},
		{"only herdr running", nil, true, false, herdrClassic, "the only one running", nil},
		{"only herdr desktop running", nil, false, true, herdrDesktop, "the only one running", nil},
		{"both running", nil, true, true, "", "", errBothHerdrs},
		{"none running keeps herdr", nil, false, false, herdrClassic, "", nil},
		{"herdr plugin hook", map[string]string{"HERDR_PLUGIN_ID": "tkt"}, true, true, herdrClassic, "running in one of its terminals", nil},
		{"herdr env other than 1 is not a herdr terminal", map[string]string{"HERDR_ENV": "0"}, true, true, "", "", errBothHerdrs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectApp(tt.env, tt.classic, tt.desktop).whichHerdr(context.Background())
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if got.flavor != tt.want || got.why != tt.why {
				t.Errorf("choice = %+v, want %q (%q)", got, tt.want, tt.why)
			}
		})
	}
}

func TestChosenHerdr(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "hc"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	if _, ok := selectApp(nil, false, false).chosenHerdr().(*herdr.Client); !ok {
		t.Error("none running: want the Herdr client")
	}
	if _, ok := selectApp(map[string]string{"HC_TERMINAL_ID": "t3"}, false, false).chosenHerdr().(*herdrcore.Client); !ok {
		t.Error("desktop terminal: want the Herdr desktop client")
	}
	both := selectApp(nil, true, true).chosenHerdr()
	if _, err := both.Agents(context.Background()); !errors.Is(err, errBothHerdrs) {
		t.Errorf("both running: Agents err = %v", err)
	}
	if err := plugins(both).ReloadConfig(context.Background()); !errors.Is(err, errBothHerdrs) {
		t.Errorf("both running: plugin err = %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := selectApp(nil, false, true).chosenHerdr().Panes(context.Background()); !errors.Is(err, herdrcore.ErrNotInstalled) {
		t.Errorf("desktop without hc: err = %v", err)
	}
}

func TestInsideHerdrRecognisesTheDesktop(t *testing.T) {
	tests := []struct {
		name            string
		env             map[string]string
		inside, plugins bool
	}{
		{"outside", nil, false, false},
		{"herdr terminal", map[string]string{"HERDR_ENV": "1"}, true, true},
		{"herdr socket only", map[string]string{"HERDR_SOCKET_PATH": "/x/herdr.sock"}, false, false},
		{"desktop terminal", map[string]string{"HC_TERMINAL_ID": "t3"}, true, false},
		{"both terminals", map[string]string{"HERDR_ENV": "1", "HC_TERMINAL_ID": "t3"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "")
			for k, v := range tt.env {
				h.env[k] = v
			}
			h.app.Executable = func() (string, error) { return "/opt/tkt/bin/tkt", nil }
			b := &boardActions{a: h.app, ctx: context.Background()}
			cmd, err := b.startCommand("DEMO-1", tui.StartOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := cmd.Args[4] == "herdr"; got != tt.inside {
				t.Errorf("board start through `tkt herdr start` = %v, want %v (%q)", got, tt.inside, cmd.Args)
			}
			if got := h.app.inHerdrPlugins(); got != tt.plugins {
				t.Errorf("inHerdrPlugins = %v, want %v", got, tt.plugins)
			}
		})
	}
}

func TestDoctorSaysWhichHerdrTktUses(t *testing.T) {
	tests := []struct {
		name             string
		env              map[string]string
		classic, desktop bool
		want             string
	}{
		{"none running", nil, false, false, "✓ herdr      /usr/local/bin/herdr"},
		{"herdr socket set", map[string]string{"HERDR_SOCKET_PATH": "/x/herdr.sock"}, false, false, "✓ herdr      /usr/local/bin/herdr"},
		{"desktop terminal", map[string]string{"HC_TERMINAL_ID": "t3"}, true, true, "✓ herdr      /usr/local/bin/herdr · tkt uses Herdr desktop (running in one of its terminals)"},
		{"only herdr running", nil, true, false, "✓ herdr      /usr/local/bin/herdr · tkt uses Herdr (the only one running)"},
		{"both running", nil, true, true, "! herdr      /usr/local/bin/herdr · " + errBothHerdrs.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, baseConfig)
			for k, v := range tt.env {
				h.env[k] = v
			}
			h.app.HerdrRunning = func(context.Context) (bool, bool) { return tt.classic, tt.desktop }
			h.exec("doctor")
			var line string
			for l := range strings.SplitSeq(h.out.String(), "\n") {
				if strings.Contains(l, " herdr ") {
					line = l
					break
				}
			}
			if line != tt.want {
				t.Errorf("herdr line = %q\nwant         %q", line, tt.want)
			}
		})
	}
}
