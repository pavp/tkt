package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/ticket"
)

const sample = `
repo_roots = ["~/src"]

[jira]
site = "example.atlassian.net"
email = "dev@example.com"

[github.accounts]
example-org = "dev-work"

[[spaces]]
key = "DEMO"
name = "Demo"

[spaces.status_overrides]
"Code Review" = "review"

[[spaces.repos]]
repo = "example-org/web"

[[spaces.repos]]
repo = "example-org/api"
base = "develop"
path = "~/src/api"

[[spaces]]
key = "OPS"

[[spaces.repos]]
repo = "example-org/web"
`

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(write(t, sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Context.Comments != DefaultComments {
		t.Errorf("comments = %d, want %d", c.Context.Comments, DefaultComments)
	}
	demo, ok := c.Space("DEMO")
	if !ok {
		t.Fatal("DEMO missing")
	}
	if demo.Repos[0].Base != "" || demo.Repos[1].Base != "develop" {
		t.Errorf("bases = %q, %q", demo.Repos[0].Base, demo.Repos[1].Base)
	}
	if got := demo.Categories()["Code Review"]; got != ticket.InReview {
		t.Errorf("override = %q", got)
	}
	if got := c.GitHubAccount("example-org"); got != "dev-work" {
		t.Errorf("account = %q", got)
	}
	repos := c.Repos()
	if len(repos) != 2 || repos[0].Name != "example-org/api" || repos[1].Name != "example-org/web" {
		t.Errorf("Repos() = %+v", repos)
	}
	if repos[0].Owner() != "example-org" || repos[0].ShortName() != "api" {
		t.Errorf("owner/name = %q/%q", repos[0].Owner(), repos[0].ShortName())
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	tests := []struct {
		name, content, wantErr string
	}{
		{"unknown key", "[jira]\nsite='example.atlassian.net'\nemail='a@b.c'\ntoken='x'\n", "unknown keys: jira.token"},
		{"missing site", "[jira]\nemail='a@b.c'\n", "jira.site is required"},
		{"http site", "[jira]\nsite='http://example.atlassian.net'\nemail='a@b.c'\n", "must use https"},
		{"bad space key", "[jira]\nsite='example.atlassian.net'\nemail='a@b.c'\n[[spaces]]\nkey='demo'\n", `space key "demo"`},
		{"bad repo", "[jira]\nsite='example.atlassian.net'\nemail='a@b.c'\n[[spaces]]\nkey='DEMO'\n[[spaces.repos]]\nrepo='web'\n", "must look like owner/name"},
		{"bad override", "[jira]\nsite='example.atlassian.net'\nemail='a@b.c'\n[[spaces]]\nkey='DEMO'\n[spaces.status_overrides]\nX='qa'\n", "unknown column"},
		{"duplicate space", "[jira]\nsite='example.atlassian.net'\nemail='a@b.c'\n[[spaces]]\nkey='DEMO'\n[[spaces]]\nkey='DEMO'\n", "listed twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSiteURL(t *testing.T) {
	for in, want := range map[string]string{
		"example.atlassian.net":          "https://example.atlassian.net",
		"https://example.atlassian.net/": "https://example.atlassian.net",
		"  example.atlassian.net/jira ":  "https://example.atlassian.net",
	} {
		got, err := SiteURL(in)
		if err != nil || got != want {
			t.Errorf("SiteURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tkt", "config.toml")
	c := &Config{
		Jira:   Jira{Site: "example.atlassian.net", Email: "dev@example.com"},
		Spaces: []Space{{Key: "DEMO", Repos: []Repo{{Name: "example-org/web"}}}},
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Spaces[0].Repos[0].Base != "" || got.Jira.Email != "dev@example.com" {
		t.Errorf("round trip = %+v", got)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	err := Save(filepath.Join(t.TempDir(), "c.toml"), &Config{})
	if err == nil || !strings.Contains(err.Error(), "jira.site is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	if _, err := Load(filepath.Join("..", "..", "docs", "config.example.toml")); err != nil {
		t.Fatalf("docs/config.example.toml: %v", err)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	c := Config{
		RepoRoots: []string{"~/src"},
		GitHub:    GitHub{Accounts: map[string]string{"example-org": "dev"}},
		Spaces:    []Space{{Key: "DEMO", StatusOverrides: map[string]string{"Code Review": "review"}, Repos: []Repo{{Name: "example-org/web"}}}},
	}
	d := c.Clone()
	d.RepoRoots[0] = "~/other"
	d.GitHub.Accounts["example-org"] = "x"
	d.Spaces[0].Repos[0].Name = "example-org/api"
	d.Spaces[0].Repos = append(d.Spaces[0].Repos, Repo{Name: "example-org/new"})
	d.Spaces[0].StatusOverrides["Code Review"] = "done"
	if c.RepoRoots[0] != "~/src" || c.GitHub.Accounts["example-org"] != "dev" || c.Spaces[0].Repos[0].Name != "example-org/web" || len(c.Spaces[0].Repos) != 1 || c.Spaces[0].StatusOverrides["Code Review"] != "review" {
		t.Errorf("clone shares state with the original: %+v", c)
	}
}

func TestIgnoredRepositoriesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tkt", "config.toml")
	c := &Config{
		Jira:   Jira{Site: "example.atlassian.net", Email: "dev@example.com"},
		Spaces: []Space{{Key: "DEMO", Ignored: []string{"example-org/api"}}},
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := got.Spaces[0]
	if !s.Ignores("Example-Org/API") || s.Ignores("example-org/web") {
		t.Errorf("ignored = %v", s.Ignored)
	}
	clone := got.Clone()
	clone.Spaces[0].Ignored[0] = "changed"
	if got.Spaces[0].Ignored[0] != "example-org/api" {
		t.Error("Clone shares the ignored list")
	}
}
