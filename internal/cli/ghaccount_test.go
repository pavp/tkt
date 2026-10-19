package cli

import (
	"context"
	"testing"

	"github.com/pavp/tkt/internal/config"
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
