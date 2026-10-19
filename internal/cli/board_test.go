package cli

import (
	"context"
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
