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
