package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/paths"
)

type memCreds map[string]string

func (m memCreds) Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", credstore.ErrNotFound
	}
	return v, nil
}
func (m memCreds) Set(k, v string) error { m[k] = v; return nil }
func (m memCreds) Delete(k string) error { delete(m, k); return nil }
func (m memCreds) Name() string          { return "test store" }

// fakeRunner answers external commands from a table keyed by "name arg1 arg2...".
type fakeRunner struct {
	out     map[string]string
	missing map[string]bool
	calls   []string
	// stdin records what each command received, keyed like out.
	stdin map[string]string
}

func (f *fakeRunner) Run(_ context.Context, dir, name string, args []string, in []byte) ([]byte, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, key)
	if f.stdin == nil {
		f.stdin = map[string]string{}
	}
	f.stdin[key] = string(in)
	if v, ok := f.out[key]; ok {
		return []byte(v), nil
	}
	if name == "git" && len(args) > 2 && args[0] == "remote" {
		if v, ok := f.out["remote "+dir]; ok {
			return []byte(v), nil
		}
	}
	return nil, errors.New(key + ": not faked")
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.missing[name] {
		return "", errors.New("not found")
	}
	return "/usr/local/bin/" + name, nil
}

type harness struct {
	app    *App
	out    *bytes.Buffer
	errOut *bytes.Buffer
	creds  memCreds
	run    *fakeRunner
	env    map[string]string
	home   string
	jira   *fakeJira
}

func newHarness(t *testing.T, configTOML string) *harness {
	t.Helper()
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.toml")
	if configTOML != "" {
		if err := os.WriteFile(cfgPath, []byte(configTOML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := &harness{
		out:    &bytes.Buffer{},
		errOut: &bytes.Buffer{},
		creds:  memCreds{},
		run:    &fakeRunner{out: map[string]string{}, missing: map[string]bool{}},
		env:    map[string]string{},
		home:   home,
		jira:   &fakeJira{},
	}
	h.app = &App{
		Stdin:        strings.NewReader(""),
		Stdout:       h.out,
		Stderr:       h.errOut,
		Env:          paths.Env{GOOS: "linux", Home: home, Getenv: func(k string) string { return h.env[k] }},
		Run:          h.run,
		Creds:        h.creds,
		IsTerminal:   func() bool { return false },
		ReadPassword: func() ([]byte, error) { return nil, errors.New("no terminal") },
		NewJira: func(_, _, token string) JiraAPI {
			h.jira.token = token
			return h.jira
		},
		configPath: cfgPath,
	}
	return h
}

func (h *harness) exec(args ...string) int {
	h.out.Reset()
	h.errOut.Reset()
	return run(h.app, args)
}

const baseConfig = `
repo_roots = ["~/src"]

[jira]
site = "example.atlassian.net"
email = "dev@example.com"

[github.accounts]
example-org = "dev-work"

[[spaces]]
key = "DEMO"

[[spaces.repos]]
repo = "example-org/web"
`

func TestAuthJiraStoresTokenFromStdin(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.app.Stdin = strings.NewReader("  tok-123  \n")
	if code := h.exec("auth", "jira"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := h.creds["jira:example.atlassian.net:dev@example.com"]; got != "tok-123" {
		t.Errorf("stored %q", got)
	}
	if !strings.Contains(h.out.String(), "✓ Connected to https://example.atlassian.net as Dev Example") ||
		!strings.Contains(h.out.String(), "✓ Jira token saved in test store for dev@example.com on https://example.atlassian.net") {
		t.Errorf("output = %q", h.out)
	}
	if h.jira.token != "tok-123" {
		t.Errorf("validated with %q", h.jira.token)
	}
	if st := h.app.loadState(); st.JiraSite != "https://example.atlassian.net" || st.JiraEmail != "dev@example.com" {
		t.Errorf("account not remembered: %+v", st)
	}
	if strings.Contains(h.out.String()+h.errOut.String(), "tok-123") {
		t.Error("token printed")
	}
}

func TestAuthJiraHiddenPromptOnTerminal(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.app.IsTerminal = func() bool { return true }
	h.app.ReadPassword = func() ([]byte, error) { return []byte("typed"), nil }
	if code := h.exec("auth", "jira"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if h.creds["jira:example.atlassian.net:dev@example.com"] != "typed" {
		t.Error("token not stored")
	}
	if !strings.Contains(h.errOut.String(), "input hidden") || !strings.Contains(h.errOut.String(), atlassianTokenURL) {
		t.Errorf("prompt = %q", h.errOut)
	}
}

func TestAuthJiraFlagsWithoutConfig(t *testing.T) {
	h := newHarness(t, "")
	h.app.Stdin = strings.NewReader("tok\n")
	if code := h.exec("auth", "jira", "--site", "other.atlassian.net", "--email", "Me@Example.com"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if h.creds["jira:other.atlassian.net:me@example.com"] != "tok" {
		t.Errorf("creds = %v", h.creds)
	}
}

func TestAuthJiraErrors(t *testing.T) {
	h := newHarness(t, "")
	if code := h.exec("auth", "jira"); code == 0 || !strings.Contains(h.errOut.String(), "pass --site and --email, or run `tkt init`") {
		t.Errorf("unknown account: exit %d, %q", code, h.errOut)
	}
	h = newHarness(t, baseConfig)
	h.app.Stdin = strings.NewReader("\n")
	if code := h.exec("auth", "jira"); code == 0 || !strings.Contains(h.errOut.String(), "no token entered") {
		t.Errorf("empty token: exit %d, %q", code, h.errOut)
	}
	if len(h.creds) != 0 {
		t.Error("stored an empty token")
	}
}

func TestAuthJiraRejectedTokenIsNotSaved(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.jira.myselfErr = jira.ErrUnauthorized
	h.app.Stdin = strings.NewReader("wrong\n")
	if code := h.exec("auth", "jira"); code == 0 || !strings.Contains(h.errOut.String(), "jira rejected this token for dev@example.com; nothing was saved") {
		t.Fatalf("exit %d, %q", code, h.errOut)
	}
	if len(h.creds) != 0 {
		t.Error("rejected token was saved")
	}
}

func TestAuthJiraUnreachableStillSaves(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.jira.myselfErr = errors.New("dial tcp: no route")
	h.app.Stdin = strings.NewReader("tok\n")
	if code := h.exec("auth", "jira"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.errOut.String(), "could not reach Jira") || h.creds["jira:example.atlassian.net:dev@example.com"] != "tok" {
		t.Errorf("stderr %q, creds %v", h.errOut, h.creds)
	}
	if strings.Contains(h.out.String(), "Connected") {
		t.Error("claimed a verified connection")
	}
}

func TestAuthStatus(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.run.out["gh auth token --user dev-work"] = "ghp_x\n"
	if code := h.exec("auth", "status"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	out := h.out.String()
	if !strings.Contains(out, "✗ jira") || !strings.Contains(out, "run `tkt auth jira`") {
		t.Errorf("missing jira token not reported: %q", out)
	}
	if !strings.Contains(out, "✓ github  example-org via gh account dev-work") {
		t.Errorf("github not reported: %q", out)
	}
	if strings.Contains(out, "ghp_x") {
		t.Error("GitHub token printed")
	}

	h.creds["jira:example.atlassian.net:dev@example.com"] = "t"
	h.exec("--json", "auth", "status")
	var got struct {
		Jira   jiraStatus     `json:"jira"`
		GitHub []githubStatus `json:"github"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, h.out)
	}
	if got.Jira.Token != "present" || got.Jira.Source != "test store" || len(got.GitHub) != 1 || !got.GitHub[0].OK {
		t.Errorf("json = %+v", got)
	}

	h.env[credstore.EnvJiraToken] = "env-token"
	h.exec("auth", "status")
	if !strings.Contains(h.out.String(), "token from TKT_JIRA_TOKEN") {
		t.Errorf("env source not reported: %q", h.out)
	}
}

func TestDoctorReportsProblemsAndFails(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.run.missing["claude"] = true
	h.run.missing["herdr"] = true
	code := h.exec("doctor")
	if code == 0 {
		t.Fatal("doctor passed with missing token and claude")
	}
	out := h.out.String()
	for _, want := range []string{
		"✓ config",
		"✗ claude",
		"needed by `tkt start`",
		"! herdr",
		"✗ jira",
		"✗ github",
		"! repo       example-org/web not cloned locally",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
	if h.errOut.Len() != 0 {
		t.Errorf("doctor wrote an extra error: %q", h.errOut)
	}
}

func TestDoctorPassesWhenReady(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.creds["jira:example.atlassian.net:dev@example.com"] = "t"
	h.run.out["gh auth token --user dev-work"] = "ghp_x"
	clone := filepath.Join(h.home, "src", "web")
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.run.out["remote "+clone] = "git@github.com:example-org/web.git"
	if code := h.exec("--json", "doctor"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.out)
	}
	var checks []check
	if err := json.Unmarshal(h.out.Bytes(), &checks); err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if c.Status == statusFail {
			t.Errorf("failing check: %+v", c)
		}
	}
	if !strings.Contains(h.out.String(), "example-org/web → "+clone) {
		t.Errorf("repo not resolved:\n%s", h.out)
	}
}

func TestDoctorWithoutConfig(t *testing.T) {
	h := newHarness(t, "")
	if code := h.exec("doctor"); code == 0 {
		t.Fatal("doctor passed without config")
	}
	if !strings.Contains(h.out.String(), "run `tkt init`") {
		t.Errorf("output = %q", h.out)
	}
}

func TestVersionFlag(t *testing.T) {
	h := newHarness(t, "")
	if code := h.exec("--version"); code != 0 || !strings.Contains(h.out.String(), "tkt version dev") {
		t.Errorf("exit %d, %q", code, h.out)
	}
}

const clipToken = "ATATT3xFfGF0-example-token-0123456789abcdef"

func terminalHarness(t *testing.T, config string, passwords ...string) *harness {
	h := newHarness(t, config)
	h.app.IsTerminal = func() bool { return true }
	h.app.ReadPassword = func() ([]byte, error) {
		if len(passwords) == 0 {
			return nil, errors.New("no more input")
		}
		p := passwords[0]
		passwords = passwords[1:]
		return []byte(p), nil
	}
	return h
}

func TestAuthJiraTakesTheTokenFromTheClipboard(t *testing.T) {
	h := terminalHarness(t, baseConfig, "")
	h.run.out["wl-paste -n"] = clipToken + "\n"
	h.app.Stdin = strings.NewReader("\n")
	if code := h.exec("auth", "jira"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := h.creds["jira:example.atlassian.net:dev@example.com"]; got != clipToken {
		t.Errorf("stored %q", got)
	}
	if !slices.Contains(h.run.calls, "xdg-open "+atlassianTokenURL) {
		t.Errorf("token page not opened: %v", h.run.calls)
	}
	if !strings.Contains(h.errOut.String(), "Use the token on the clipboard ("+strconv.Itoa(len(clipToken))+" characters)? (Y/n)") {
		t.Errorf("no clipboard confirmation: %q", h.errOut)
	}
	if v, ok := h.run.stdin["wl-copy"]; !ok || v != "" {
		t.Errorf("clipboard not cleared after saving: %v", h.run.calls)
	}
	if strings.Contains(h.out.String()+h.errOut.String(), clipToken) {
		t.Error("token printed")
	}
}

func TestAuthJiraClipboardDeclinedFallsBackToTheHiddenPrompt(t *testing.T) {
	h := terminalHarness(t, baseConfig, "", "typed-token-value")
	h.run.out["wl-paste -n"] = clipToken
	h.app.Stdin = strings.NewReader("n\n")
	if code := h.exec("auth", "jira"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := h.creds["jira:example.atlassian.net:dev@example.com"]; got != "typed-token-value" {
		t.Errorf("stored %q", got)
	}
	if _, ok := h.run.stdin["wl-copy"]; ok {
		t.Error("clipboard cleared although its token was not used")
	}
}

func TestAuthJiraLeavesTheClipboardWhenTheTokenIsRejected(t *testing.T) {
	h := terminalHarness(t, baseConfig, "")
	h.run.out["wl-paste -n"] = clipToken
	h.app.Stdin = strings.NewReader("\n")
	h.jira.myselfErr = jira.ErrUnauthorized
	if code := h.exec("auth", "jira"); code == 0 {
		t.Fatal("a rejected token should fail")
	}
	if _, ok := h.run.stdin["wl-copy"]; ok {
		t.Error("clipboard cleared although nothing was saved")
	}
}

func TestAuthJiraAsksForTheAccountWithGitEmailAsDefault(t *testing.T) {
	h := terminalHarness(t, "", "typed-token-value")
	h.run.out["git config user.email"] = "me@work.example\n"
	h.app.Stdin = strings.NewReader("example.atlassian.net\n\n")
	if code := h.exec("auth", "jira"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.errOut.String(), "Atlassian account email [me@work.example]") {
		t.Errorf("email prompt = %q", h.errOut)
	}
	if h.creds["jira:example.atlassian.net:me@work.example"] != "typed-token-value" {
		t.Errorf("creds = %v", h.creds)
	}
}

func TestAuthJiraPipedIgnoresTheLastLogin(t *testing.T) {
	h := newHarness(t, "")
	h.app.saveState(state{JiraSite: "https://example.atlassian.net", JiraEmail: "dev@example.com"})
	h.app.Stdin = strings.NewReader("tok\n")
	if code := h.exec("auth", "jira"); code == 0 || !strings.Contains(h.errOut.String(), "jira site and email are unknown") {
		t.Errorf("piped auth should still need flags or a config: exit %d, %q", code, h.errOut)
	}
	if len(h.creds) != 0 {
		t.Errorf("stored a token for the last login: %v", h.creds)
	}
}

func TestAuthJiraOffersTheLastLoginInATerminal(t *testing.T) {
	h := terminalHarness(t, "", "typed-token-value")
	h.app.saveState(state{JiraSite: "https://example.atlassian.net", JiraEmail: "dev@example.com"})
	h.run.out["git config user.email"] = "me@personal.example\n"
	h.app.Stdin = strings.NewReader("\n\n")
	if code := h.exec("auth", "jira"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.errOut.String(), "[https://example.atlassian.net]") || !strings.Contains(h.errOut.String(), "Atlassian account email [dev@example.com]") {
		t.Errorf("last login should be offered as defaults: %q", h.errOut)
	}
	if h.creds["jira:example.atlassian.net:dev@example.com"] != "typed-token-value" {
		t.Errorf("creds = %v", h.creds)
	}
}
