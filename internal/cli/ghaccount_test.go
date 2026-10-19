package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/github"
)

const twoAccounts = "  ✓ Logged in to github.com account personal-dev (keyring)\n  - Active account: true\n  ✓ Logged in to github.com account work-dev (keyring)\n  - Active account: false\n"

func TestGitHubAccountDetectsAndRemembers(t *testing.T) {
	h := newHarness(t, "")
	h.run.out["gh auth status --hostname github.com"] = twoAccounts
	h.run.out["gh auth token --user personal-dev"] = "t-personal"
	h.run.out["gh auth token --user work-dev"] = "t-work"
	checks := 0
	member := func(_ context.Context, tok, org string) (bool, error) {
		checks++
		return tok == "t-work" && org == "example-org", nil
	}
	c := &config.Config{}
	if got := h.app.githubAccount(context.Background(), c, "example-org", member); got != "work-dev" {
		t.Fatalf("detected %q", got)
	}
	if st := h.app.loadState(); st.GitHubAccounts["example-org"] != "work-dev" {
		t.Errorf("not remembered: %+v", st.GitHubAccounts)
	}
	before := checks
	if got := h.app.githubAccount(context.Background(), c, "example-org", member); got != "work-dev" || checks != before {
		t.Errorf("second lookup should come from the state: %q, %d checks", got, checks-before)
	}
	c.GitHub.Accounts = map[string]string{"example-org": "configured"}
	if got := h.app.githubAccount(context.Background(), c, "example-org", member); got != "configured" {
		t.Errorf("the config should win: %q", got)
	}
	if got := h.app.githubAccount(context.Background(), &config.Config{}, "unknown-org", member); got != "" {
		t.Errorf("no account can read it: want the active account, got %q", got)
	}
}

func TestGitHubAccountWithOneAccountSkipsDetection(t *testing.T) {
	h := newHarness(t, "")
	h.run.out["gh auth status --hostname github.com"] = "  ✓ Logged in to github.com account solo (keyring)\n  - Active account: true\n"
	member := func(context.Context, string, string) (bool, error) {
		t.Error("membership checked with a single account")
		return false, nil
	}
	if got := h.app.githubAccount(context.Background(), &config.Config{}, "example-org", member); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestTokenRequestsRunGhAuthStatusOnce(t *testing.T) {
	h := newHarness(t, "")
	h.run.out["gh auth status --hostname github.com"] = "  ✓ Logged in to github.com account solo (keyring)\n  - Active account: true\n"
	h.run.out["gh auth token"] = "t-solo"
	tok := github.CachedTokens(func(ctx context.Context, o string) string {
		return h.app.githubAccount(ctx, &config.Config{}, o, h.app.memberCheck())
	}, func(ctx context.Context, a string) (string, error) { return github.Token(ctx, h.app.Run, a) })
	for i := 0; i < 10; i++ {
		if _, err := tok(context.Background(), "example-org"); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, c := range h.run.calls {
		if strings.HasPrefix(c, "gh auth status") {
			n++
		}
	}
	if n > 1 {
		t.Errorf("gh auth status ran %d times", n)
	}
}

func TestStateHoldsOnlyAccountNames(t *testing.T) {
	h := newHarness(t, "")
	h.run.out["gh auth status --hostname github.com"] = twoAccounts
	h.run.out["gh auth token --user personal-dev"] = "t-personal"
	h.run.out["gh auth token --user work-dev"] = "t-work"
	h.app.githubAccount(context.Background(), &config.Config{}, "example-org", func(_ context.Context, tok, _ string) (bool, error) { return tok == "t-work", nil })
	b, err := os.ReadFile(h.app.statePath())
	if err != nil || strings.Contains(string(b), "t-work") || strings.Contains(string(b), "t-personal") || !strings.Contains(string(b), "work-dev") {
		t.Errorf("state = %s (%v)", b, err)
	}
}
