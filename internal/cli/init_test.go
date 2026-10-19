package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
)

func initHarness(t *testing.T, answers string) *harness {
	t.Helper()
	h := newHarness(t, "")
	h.app.configPath = filepath.Join(h.home, "cfg", "config.toml")
	h.app.Stdin = strings.NewReader(answers)
	h.jira.projects = []jira.Project{{Key: "DEMO", Name: "Demo"}, {Key: "OPS", Name: "Operations"}}
	h.jira.statuses = map[string][]jira.Status{"DEMO": {{Name: "To Do", Category: "new"}, {Name: "Code Review", Category: "indeterminate"}}}
	h.run.out["gh auth token --user dev-work"] = "gho_x"
	return h
}

func TestInitWritesConfig(t *testing.T) {
	answers := strings.Join([]string{
		"example.atlassian.net",
		"dev@example.com",
		"tok-1",
		"1, ops",
		"example-org/web, example-org/api",
		"",
		"~/src",
		"dev-work",
	}, "\n") + "\n"
	h := initHarness(t, answers)
	if code := h.exec("init"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if h.creds["jira:example.atlassian.net:dev@example.com"] != "tok-1" {
		t.Errorf("token not stored: %v", h.creds)
	}
	c, err := config.Load(h.app.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if c.Jira.Site != "example.atlassian.net" || c.Jira.Email != "dev@example.com" {
		t.Errorf("jira = %+v", c.Jira)
	}
	if len(c.Spaces) != 2 || c.Spaces[0].Key != "DEMO" || c.Spaces[1].Key != "OPS" {
		t.Fatalf("spaces = %+v", c.Spaces)
	}
	if len(c.Spaces[0].Repos) != 2 || c.Spaces[0].Repos[1].Name != "example-org/api" || c.Spaces[0].Repos[0].Base != "main" {
		t.Errorf("repos = %+v", c.Spaces[0].Repos)
	}
	if c.Spaces[0].StatusOverrides["Code Review"] != "review" {
		t.Errorf("overrides = %v", c.Spaces[0].StatusOverrides)
	}
	if c.GitHubAccount("example-org") != "dev-work" || strings.Join(c.RepoRoots, ",") != "~/src" {
		t.Errorf("github/roots = %v %v", c.GitHub.Accounts, c.RepoRoots)
	}
	for _, want := range []string{"✓ Connected as Dev Example", `DEMO: "Code Review" → In Review column`, "✓ GitHub access for example-org"} {
		if !strings.Contains(h.errOut.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, h.errOut)
		}
	}
	if !strings.Contains(h.out.String(), "Next: `tkt doctor`") {
		t.Errorf("stdout = %q", h.out)
	}
	fi, _ := os.Stat(h.app.configPath)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o", fi.Mode().Perm())
	}
}

func TestInitRejectedTokenWritesNothing(t *testing.T) {
	h := initHarness(t, "example.atlassian.net\ndev@example.com\nbad\n")
	h.jira.myselfErr = jira.ErrUnauthorized
	if code := h.exec("init"); code == 0 || !strings.Contains(h.errOut.String(), "jira rejected this token; nothing was saved") {
		t.Fatalf("exit %d, %q", code, h.errOut)
	}
	if len(h.creds) != 0 {
		t.Error("token saved")
	}
	if _, err := os.Stat(h.app.configPath); err == nil {
		t.Error("config written")
	}
}

func TestInitKeepsExistingConfigUnlessConfirmed(t *testing.T) {
	h := initHarness(t, "\n")
	if err := os.MkdirAll(filepath.Dir(h.app.configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.app.configPath, []byte(baseConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := h.exec("init"); code == 0 || !strings.Contains(h.errOut.String(), "kept the existing config") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
	got, _ := os.ReadFile(h.app.configPath)
	if string(got) != baseConfig {
		t.Error("existing config changed")
	}
}

func TestInitReusesStoredTokenAndDefaults(t *testing.T) {
	h := initHarness(t, "y\n\n\n\nDEMO\n\n\n")
	if err := os.MkdirAll(filepath.Dir(h.app.configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.app.configPath, []byte(baseConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	h.creds["jira:example.atlassian.net:dev@example.com"] = "stored"
	if code := h.exec("init"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if h.jira.token != "stored" {
		t.Errorf("validated with %q", h.jira.token)
	}
	c, err := config.Load(h.app.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if c.Jira.Email != "dev@example.com" || len(c.Spaces) != 1 || len(c.Spaces[0].Repos) != 0 || c.RepoRoots[0] != "~/dev" {
		t.Errorf("config = %+v", c)
	}
}

func TestInitNeedsAnswers(t *testing.T) {
	h := initHarness(t, "")
	if code := h.exec("init"); code == 0 || !strings.Contains(h.errOut.String(), "setup needs answers") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestInitRejectsUnknownSpace(t *testing.T) {
	h := initHarness(t, "example.atlassian.net\ndev@example.com\ntok\n9\n")
	if code := h.exec("init"); code == 0 || !strings.Contains(h.errOut.String(), `"9" is not one of the listed spaces`) {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}
