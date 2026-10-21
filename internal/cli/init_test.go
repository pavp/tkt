package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdr/herdrtest"
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
		"n",
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
	if len(c.Spaces[0].Repos) != 2 || c.Spaces[0].Repos[1].Name != "example-org/api" {
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
	h := initHarness(t, "y\nDEMO\n\n\nn\n")
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

func TestInitNamesTheEnvironmentToken(t *testing.T) {
	h := initHarness(t, "example.atlassian.net\ndev@example.com\nn\ntyped\n")
	h.env["TKT_JIRA_TOKEN"] = "from-env"
	h.exec("init")
	if !strings.Contains(h.errOut.String(), "Use the token in TKT_JIRA_TOKEN and save it in test store?") {
		t.Errorf("prompt = %q", h.errOut)
	}
}

func TestInitSkipsJiraQuestionsAfterAuth(t *testing.T) {
	h := initHarness(t, "DEMO\nexample-org/web\n~/src\nn\n\n")
	h.creds["jira:example.atlassian.net:dev@example.com"] = "stored"
	h.env["XDG_DATA_HOME"] = filepath.Join(h.home, "data")
	h.app.saveState(state{JiraSite: "https://example.atlassian.net", JiraEmail: "dev@example.com"})
	if code := h.exec("init"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	errOut := h.errOut.String()
	if strings.Contains(errOut, "Jira site") || strings.Contains(errOut, "account email") || !strings.Contains(errOut, "Using https://example.atlassian.net as dev@example.com with the stored token") {
		t.Errorf("asked for known Jira details:\n%s", errOut)
	}
	if !strings.Contains(errOut, "Ctrl-C cancels") {
		t.Error("no cancel hint")
	}
	if h.jira.token != "stored" {
		t.Errorf("validated with %q", h.jira.token)
	}
}

func TestInitInHerdrPausesThenOpensBoard(t *testing.T) {
	h := initHarness(t, "DEMO\n\n\nn\n\n")
	h.creds["jira:example.atlassian.net:dev@example.com"] = "stored"
	h.env["XDG_DATA_HOME"] = filepath.Join(h.home, "data")
	h.env["HERDR_PLUGIN_ID"] = "tkt"
	h.app.saveState(state{JiraSite: "https://example.atlassian.net", JiraEmail: "dev@example.com"})
	f := herdrtest.Start(t)
	h.app.NewHerdr = func() HerdrAPI { return herdr.New(f.Path) }
	if code := h.exec("init"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.errOut.String(), "! DEMO has no repositories") || !strings.Contains(h.errOut.String(), "Press Enter to open the board") {
		t.Errorf("stderr = %q", h.errOut)
	}
	if got := f.Last("plugin.pane.open"); got["entrypoint"] != "board" {
		t.Errorf("board not opened: %v", got)
	}
}

func TestInitPipedDoesNotSuggestTheGitEmail(t *testing.T) {
	h := newHarness(t, "")
	h.run.out["git config user.email"] = "g@example.com\n"
	h.app.Stdin = strings.NewReader("example.atlassian.net\n\n")
	h.exec("init")
	if strings.Contains(h.errOut.String(), "g@example.com") || !strings.Contains(h.errOut.String(), "an email is required") {
		t.Errorf("piped init should not suggest the git email: %q", h.errOut)
	}
}

func TestInitSuggestsReposAndDetectsTheGitHubAccount(t *testing.T) {
	h := initHarness(t, "example.atlassian.net\ndev@example.com\ntok-1\n1\n\n~/src\n")
	h.jira.issues = []jira.Issue{{ID: "101", Key: "DEMO-1"}, {ID: "102", Key: "DEMO-2"}}
	h.jira.devPRs = map[string][]jira.DevPullRequest{
		"101": {{Repo: "Example-Org/Web", Number: 9}},
		"102": {{Repo: "example-org/web", Number: 3}, {Repo: "example-org/api", Number: 4}},
	}
	h.run.out["gh auth status --hostname github.com"] = "  ✓ Logged in to github.com account personal (keyring)\n  - Active account: true\n  ✓ Logged in to github.com account dev-work (keyring)\n  - Active account: false\n"
	h.run.out["gh auth token --user personal"] = "t-personal"
	h.app.OrgMember = func(_ context.Context, tok, org string) (bool, error) {
		return tok == "gho_x" && org == "example-org", nil
	}
	if code := h.exec("init"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.errOut.String(), "Repositories for DEMO (owner/name, comma separated) [example-org/api, example-org/web]") {
		t.Errorf("no repository suggestion:\n%s", h.errOut)
	}
	c, err := config.Load(h.app.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Spaces[0].Repos) != 2 || c.GitHubAccount("example-org") != "dev-work" {
		t.Errorf("repos %+v, accounts %v", c.Spaces[0].Repos, c.GitHub.Accounts)
	}
	if !strings.Contains(h.errOut.String(), "✓ example-org via gh account dev-work") {
		t.Errorf("detection not reported:\n%s", h.errOut)
	}
}

func TestInitOffersGhLoginWithoutAnAccount(t *testing.T) {
	h := initHarness(t, "example.atlassian.net\ndev@example.com\ntok-1\n1\nexample-org/web\n~/src\ny\n")
	h.app.OrgMember = func(context.Context, string, string) (bool, error) { return true, nil }
	var launched []string
	h.app.Launch = func(_ string, name string, args []string) error {
		launched = append(launched, name+" "+strings.Join(args, " "))
		h.run.out["gh auth status --hostname github.com"] = "  ✓ Logged in to github.com account dev-work (keyring)\n  - Active account: true\n"
		return nil
	}
	if code := h.exec("init"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(launched) != 1 || launched[0] != "gh auth login" {
		t.Errorf("launched %v", launched)
	}
	if !strings.Contains(h.errOut.String(), "gh has no logged-in GitHub account") || !strings.Contains(h.errOut.String(), "✓ GitHub access for example-org") {
		t.Errorf("stderr:\n%s", h.errOut)
	}
}

func TestInitAsksForTeamBoards(t *testing.T) {
	answers := strings.Join([]string{
		"example.atlassian.net",
		"dev@example.com",
		"tok-1",
		"1",
		"example-org/web",
		"authorization center, access, nope, Center",
		"~/src",
		"n",
		"dev-work",
	}, "\n") + "\n"
	h := initHarness(t, answers)
	h.jira.boards = map[string][]jira.Board{"DEMO": {{ID: 418, Name: "User Access Center"}, {ID: 5205, Name: "Authorization Center"}}}
	if code := h.exec("init"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	c, err := config.Load(h.app.configPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []config.TeamBoard{{ID: 5205, Name: "Authorization Center"}, {ID: 418, Name: "User Access Center"}}
	if !reflect.DeepEqual(c.Spaces[0].TeamBoards, want) {
		t.Errorf("team boards = %+v", c.Spaces[0].TeamBoards)
	}
	errOut := h.errOut.String()
	for _, w := range []string{"Team boards for DEMO (names, comma separated; 2 scrum boards)", `! no scrum board of DEMO is called "nope"`, `! "Center" fits several boards of DEMO`} {
		if !strings.Contains(errOut, w) {
			t.Errorf("stderr missing %q:\n%s", w, errOut)
		}
	}
}
