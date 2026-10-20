package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/tui"
)

func TestOpenURLPicksTheSystemOpener(t *testing.T) {
	h, _ := listHarness(t)
	h.app.Env.GOOS = "darwin"
	h.run.out["open https://example.atlassian.net/browse/DEMO-1"] = ""
	if err := h.app.openURL(context.Background(), "https://example.atlassian.net/browse/DEMO-1"); err != nil {
		t.Fatal(err)
	}
	h.app.Env.GOOS = "linux"
	h.run.out["xdg-open https://github.com/example-org/web/pull/1"] = ""
	if err := h.app.openURL(context.Background(), "https://github.com/example-org/web/pull/1"); err != nil {
		t.Fatal(err)
	}
	h.env["WSL_DISTRO_NAME"] = "Ubuntu"
	h.run.missing["wslview"] = true
	h.run.out["explorer.exe https://github.com/example-org/web/pull/2"] = ""
	if err := h.app.openURL(context.Background(), "https://github.com/example-org/web/pull/2"); err != nil {
		t.Fatal(err)
	}
	if err := h.app.openURL(context.Background(), "file:///etc/passwd"); err == nil || !strings.Contains(err.Error(), "only https") {
		t.Errorf("err = %v", err)
	}
}

func TestBoardActionsRefreshAndFix(t *testing.T) {
	h, _, _ := fixHarness(t)
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	msg := b.Refresh()().(tui.SnapshotMsg)
	if msg.Err != nil || len(msg.Snap.Tickets) == 0 {
		t.Fatalf("refresh = %+v", msg)
	}
	if got := b.Fix("DEMO-1")(); got != tui.StatusMsg("outside Herdr: run `tkt fix DEMO-1` to print the prompt") {
		t.Errorf("fix outside Herdr = %v", got)
	}
}

func TestCopyToClipboard(t *testing.T) {
	h, _ := listHarness(t)
	h.app.Env.GOOS = "darwin"
	h.run.out["pbcopy"] = ""
	if err := h.app.copyToClipboard(context.Background(), "DEMO-1"); err != nil {
		t.Fatal(err)
	}
	h.app.Env.GOOS = "linux"
	h.run.missing["wl-copy"] = true
	h.run.out["xclip -selection clipboard"] = ""
	if err := h.app.copyToClipboard(context.Background(), "DEMO-1"); err != nil {
		t.Fatal(err)
	}
	h.run.missing["xclip"], h.run.missing["xsel"] = true, true
	if err := h.app.copyToClipboard(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "no clipboard tool") {
		t.Errorf("err = %v", err)
	}
}

func TestBoardTicketPaneGoesToTheTicketWorkspace(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.env["HERDR_ENV"] = "1"
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w5","worktree":{"checkout_path":"/src/web-DEMO-1"}}]}`
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	if b.TicketPane("DEMO-1") == nil {
		t.Fatal("no command")
	}
	// tea.Sequence wraps the commands; run the first one directly through the same code path.
	open := b.ticketPaneOpen("DEMO-1")
	if msg := open(); msg != nil {
		t.Fatalf("open returned %v", msg)
	}
	if got := f.Last("workspace.focus"); got["workspace_id"] != "w5" {
		t.Errorf("focus = %v", got)
	}
	if got := f.Last("plugin.pane.open"); got["workspace_id"] != "w5" || got["cwd"] != "/src/web-DEMO-1" || got["env"].(map[string]any)["TKT_KEY"] != "DEMO-1" {
		t.Errorf("pane = %v", got)
	}
}

func TestBoardDetailRendersTheTicket(t *testing.T) {
	h, _ := workHarness(t)
	c, _ := h.app.LoadConfig()
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	msg := b.Detail("DEMO-7")().(tui.DetailMsg)
	if msg.Err != nil || !strings.Contains(msg.Text, "DEMO-7") || !strings.Contains(msg.Text, "Export users") {
		t.Errorf("detail = %+v", msg)
	}
}

func TestListDirsListsFoldersOnly(t *testing.T) {
	h := newHarness(t, "")
	for _, d := range []string{"dev", "Docs", ".cache"} {
		if err := os.Mkdir(filepath.Join(h.home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h.home, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h.home, "dev"), filepath.Join(h.home, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h.home, "gone"), filepath.Join(h.home, "broken")); err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, ctx: context.Background()}
	cases := []struct {
		path    string
		want    string
		dir     string
		abs     string
		wantErr bool
	}{
		{"~/", ".cache,dev,Docs,link", "~/", h.home + "/", false},
		{h.home + "/", ".cache,dev,Docs,link", "~/", h.home + "/", false},
		{h.home + "/dev/../", ".cache,dev,Docs,link", "~/", h.home + "/", false},
		{"~/missing/", "", "~/missing/", h.home + "/missing/", true},
		{"/", "", "/", "/", false},
	}
	for _, c := range cases {
		msg := b.ListDirs(c.path)().(tui.DirsListedMsg)
		if c.path == "/" {
			if msg.Err != nil || msg.Dir != "/" || msg.Abs != "/" || len(msg.Names) == 0 {
				t.Errorf("ListDirs(/) = %+v", msg)
			}
			continue
		}
		if msg.Path != c.path || strings.Join(msg.Names, ",") != c.want || msg.Dir != c.dir || msg.Abs != c.abs || (msg.Err != nil) != c.wantErr {
			t.Errorf("ListDirs(%q) = %+v", c.path, msg)
		}
		if c.path == "~/" && strings.Join(msg.Broken, ",") != "broken" {
			t.Errorf("broken links = %v", msg.Broken)
		}
	}
}

func TestListDirsReportsUnreadableFolders(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	h := newHarness(t, "")
	locked := filepath.Join(h.home, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	b := &boardActions{a: h.app, ctx: context.Background()}
	msg := b.ListDirs("~/locked/")().(tui.DirsListedMsg)
	if !errors.Is(msg.Err, os.ErrPermission) || msg.Abs != locked+"/" {
		t.Errorf("ListDirs = %+v", msg)
	}
}

func TestNativePickerOnlyWhereTheUserSeesIt(t *testing.T) {
	cases := []struct {
		name   string
		goos   string
		env    map[string]string
		zenity bool
		want   bool
	}{
		{"macOS", "darwin", nil, false, true},
		{"macOS over SSH", "darwin", map[string]string{"SSH_CONNECTION": "10.0.0.2 5000 10.0.0.1 22"}, false, false},
		{"Linux with zenity", "linux", nil, true, true},
		{"Linux with zenity over SSH", "linux", map[string]string{"SSH_TTY": "/dev/pts/4"}, true, false},
		{"Linux without a chooser", "linux", nil, false, false},
		{"WSL", "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, "")
			h.app.Env.GOOS = c.goos
			for k, v := range c.env {
				h.env[k] = v
			}
			h.run.missing["zenity"] = !c.zenity
			h.run.missing["kdialog"] = true
			b := &boardActions{a: h.app, ctx: context.Background()}
			if got := b.NativePicker(); got != c.want {
				t.Errorf("NativePicker() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBoardOpenOverSSHCopiesTheLinkToTheLocalClipboard(t *testing.T) {
	const url = "https://example.atlassian.net/browse/DEMO-1"
	cases := []struct {
		name   string
		env    map[string]string
		osc52  bool
		opened bool
		status string
	}{
		{"local", nil, false, true, "opened " + url},
		{"over SSH", map[string]string{"SSH_CONNECTION": "10.0.0.2 5000 10.0.0.1 22"}, true, false, "✓ link copied · \x1b]8;;" + url + "\x1b\\" + url + "\x1b]8;;\x1b\\"},
		{"over SSH with only a tty", map[string]string{"SSH_TTY": "/dev/pts/4"}, true, false, "✓ link copied · \x1b]8;;" + url + "\x1b\\" + url + "\x1b]8;;\x1b\\"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, "")
			h.app.Env.GOOS = "darwin"
			h.run.out["open "+url] = ""
			for k, v := range c.env {
				h.env[k] = v
			}
			b := &boardActions{a: h.app, ctx: context.Background()}
			msg := b.Open(url)()
			if got := string(msg.(tui.StatusMsg)); got != c.status {
				t.Errorf("status = %q, want %q", got, c.status)
			}
			seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(url)) + "\a"
			if got := strings.Contains(h.out.String(), seq); got != c.osc52 {
				t.Errorf("OSC 52 written = %v, output %q", got, h.out.String())
			}
			if got := slices.Contains(h.run.calls, "open "+url); got != c.opened {
				t.Errorf("browser opened = %v, calls %v", got, h.run.calls)
			}
		})
	}
}
