package credstore

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

type memStore map[string]string

func (m memStore) Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}
func (m memStore) Set(k, v string) error { m[k] = v; return nil }
func (m memStore) Delete(k string) error { delete(m, k); return nil }
func (m memStore) Name() string          { return "memory" }

func TestJiraKey(t *testing.T) {
	got := JiraKey("https://example.atlassian.net", "Dev@Example.com")
	if got != "jira:example.atlassian.net:dev@example.com" {
		t.Errorf("JiraKey = %q", got)
	}
}

func TestJiraTokenPrefersEnvironment(t *testing.T) {
	s := memStore{JiraKey("https://example.atlassian.net", "dev@example.com"): "stored"}
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }

	v, src, err := JiraToken(s, getenv, "https://example.atlassian.net", "dev@example.com")
	if err != nil || v != "stored" || src != SourceStore {
		t.Fatalf("store lookup = %q, %q, %v", v, src, err)
	}
	env[EnvJiraToken] = " from-env "
	v, src, err = JiraToken(s, getenv, "https://example.atlassian.net", "dev@example.com")
	if err != nil || v != "from-env" || src != SourceEnv {
		t.Fatalf("env lookup = %q, %q, %v", v, src, err)
	}
	delete(env, EnvJiraToken)
	_, _, err = JiraToken(memStore{}, getenv, "https://example.atlassian.net", "dev@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing token err = %v", err)
	}
}

func TestIsWSL(t *testing.T) {
	none := func(string) string { return "" }
	noFile := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if IsWSL(none, noFile) {
		t.Error("plain Linux detected as WSL")
	}
	if !IsWSL(func(k string) string {
		if k == "WSL_DISTRO_NAME" {
			return "Ubuntu"
		}
		return ""
	}, noFile) {
		t.Error("WSL_DISTRO_NAME not detected")
	}
	if !IsWSL(none, func(string) ([]byte, error) { return []byte("5.15.153.1-microsoft-standard-WSL2"), nil }) {
		t.Error("osrelease not detected")
	}
}

type psCall struct {
	args  []string
	stdin []byte
}

type fakePS struct {
	calls []psCall
	out   string
	err   error
}

func (f *fakePS) Run(_ context.Context, _, name string, args []string, stdin []byte) ([]byte, error) {
	if name != "powershell.exe" {
		return nil, errors.New("unexpected " + name)
	}
	f.calls = append(f.calls, psCall{args, stdin})
	return []byte(f.out), f.err
}

func (f *fakePS) LookPath(string) (string, error) { return "", nil }

func decode(t *testing.T, args []string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestWSLSetSendsValueOnStdinOnly(t *testing.T) {
	f := &fakePS{}
	if err := NewWSL(f).Set("jira:example.atlassian.net:dev@example.com", "s3cret"); err != nil {
		t.Fatal(err)
	}
	c := f.calls[0]
	if string(c.stdin) != "s3cret" {
		t.Errorf("stdin = %q", c.stdin)
	}
	script := decode(t, c.args)
	if strings.Contains(script, "s3cret") || strings.Contains(strings.Join(c.args, " "), "s3cret") {
		t.Error("value leaked into the command line")
	}
	if !strings.Contains(script, "[TktCred]::Write('tkt:jira:example.atlassian.net:dev@example.com'") {
		t.Errorf("script does not target the credential:\n%s", script)
	}
	if c.args[0] != "-NoProfile" || c.args[1] != "-NonInteractive" || c.args[2] != "-EncodedCommand" {
		t.Errorf("args = %v", c.args[:3])
	}
}

func TestWSLGet(t *testing.T) {
	v, err := NewWSL(&fakePS{out: "tok"}).Get("jira:example.atlassian.net:dev@example.com")
	if err != nil || v != "tok" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if _, err := NewWSL(&fakePS{err: errors.New("powershell.exe: exit status 3")}).Get("jira:x:y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing credential err = %v", err)
	}
}

func TestWSLDelete(t *testing.T) {
	if err := NewWSL(&fakePS{out: "True"}).Delete("jira:x:y"); err != nil {
		t.Fatal(err)
	}
	if err := NewWSL(&fakePS{out: "False"}).Delete("jira:x:y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestWSLRejectsUnsafeKeys(t *testing.T) {
	f := &fakePS{}
	if err := NewWSL(f).Set("jira:x'; Remove-Item C:\\ -Recurse; '", "v"); err == nil {
		t.Fatal("expected an error for a key with quotes")
	}
	if len(f.calls) != 0 {
		t.Error("powershell ran with an unsafe key")
	}
}

func TestWindowsExeFallsBackToTheSystemPath(t *testing.T) {
	onDisk := map[string]bool{
		"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe": true,
		"/mnt/c/Windows/System32/clip.exe":                              true,
	}
	exists := func(p string) bool { return onDisk[p] }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	found := func(n string) (string, error) { return "/usr/bin/" + n, nil }
	tests := []struct {
		name, exe string
		look      func(string) (string, error)
		want      string
	}{
		{"on PATH", "powershell.exe", found, "powershell.exe"},
		{"Windows path not appended to PATH", "powershell.exe", missing, "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"},
		{"clip", "clip.exe", missing, "/mnt/c/Windows/System32/clip.exe"},
		{"unknown stays as is", "explorer.exe", missing, "explorer.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WindowsExe(tt.exe, tt.look, exists); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

type pathlessPS struct{ ran []string }

func (p *pathlessPS) Run(_ context.Context, _, name string, _ []string, _ []byte) ([]byte, error) {
	p.ran = append(p.ran, name)
	return nil, nil
}
func (p *pathlessPS) LookPath(string) (string, error) { return "", errors.New("not found") }

func TestWSLFindsPowershellOutsideThePath(t *testing.T) {
	r := &pathlessPS{}
	w := NewWSL(r)
	w.exists = func(p string) bool { return p == "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe" }
	if err := w.Set("jira:example.atlassian.net:dev@example.com", "v"); err != nil {
		t.Fatal(err)
	}
	if len(r.ran) != 1 || r.ran[0] != "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe" {
		t.Errorf("ran %v", r.ran)
	}
}
