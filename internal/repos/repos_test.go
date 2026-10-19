package repos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/config"
)

type fakeRunner struct {
	remotes map[string]string
	calls   [][]string
}

func (f *fakeRunner) Run(_ context.Context, dir, name string, args []string, _ []byte) ([]byte, error) {
	f.calls = append(f.calls, append([]string{dir, name}, args...))
	if name == "git" {
		if r, ok := f.remotes[dir]; ok {
			return []byte(r + "\n"), nil
		}
		return nil, errors.New("no remote")
	}
	if name == "gh" {
		return nil, os.MkdirAll(args[len(args)-1], 0o755)
	}
	return nil, errors.New("unexpected command")
}

func (f *fakeRunner) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func mkClone(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseRemote(t *testing.T) {
	tests := []struct {
		in, owner, name string
		ok              bool
	}{
		{"git@github.com:example-org/web.git", "example-org", "web", true},
		{"https://github.com/example-org/web", "example-org", "web", true},
		{"https://github.com/example-org/web.git/", "example-org", "web", true},
		{"ssh://git@github.com/example-org/web.git", "example-org", "web", true},
		{"git@gitlab.com:example-org/web.git", "", "", false},
		{"https://github.com/example-org", "", "", false},
	}
	for _, tt := range tests {
		owner, name, ok := ParseRemote(tt.in)
		if owner != tt.owner || name != tt.name || ok != tt.ok {
			t.Errorf("ParseRemote(%q) = %q, %q, %v", tt.in, owner, name, ok)
		}
	}
}

func TestResolveExplicitPath(t *testing.T) {
	dir := t.TempDir()
	r := Resolver{Run: &fakeRunner{}}
	got, err := r.Resolve(context.Background(), config.Repo{Name: "example-org/web", Path: dir})
	if err != nil || got != dir {
		t.Fatalf("Resolve = %q, %v", got, err)
	}
	_, err = r.Resolve(context.Background(), config.Repo{Name: "example-org/web", Path: filepath.Join(dir, "missing")})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveScansRootsByRemote(t *testing.T) {
	root := t.TempDir()
	other := mkClone(t, filepath.Join(root, "work", "other"))
	web := mkClone(t, filepath.Join(root, "work", "nested", "web-checkout"))
	mkClone(t, filepath.Join(root, "node_modules", "web"))
	f := &fakeRunner{remotes: map[string]string{
		other: "git@github.com:example-org/other.git",
		web:   "https://github.com/Example-Org/web.git",
	}}
	r := Resolver{Roots: []string{root}, Run: f}
	got, err := r.Resolve(context.Background(), config.Repo{Name: "example-org/web"})
	if err != nil || got != web {
		t.Fatalf("Resolve = %q, %v; want %q", got, err, web)
	}
}

func TestResolveNotFound(t *testing.T) {
	r := Resolver{Roots: []string{t.TempDir()}, Run: &fakeRunner{}}
	_, err := r.Resolve(context.Background(), config.Repo{Name: "example-org/web"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveRespectsDepth(t *testing.T) {
	root := t.TempDir()
	deep := mkClone(t, filepath.Join(root, "a", "b", "c", "web"))
	r := Resolver{Roots: []string{root}, Run: &fakeRunner{remotes: map[string]string{deep: "git@github.com:example-org/web.git"}}}
	if _, err := r.Resolve(context.Background(), config.Repo{Name: "example-org/web"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound beyond depth", err)
	}
}

func TestClone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "src")
	f := &fakeRunner{}
	r := Resolver{Roots: []string{root}, Run: f}
	got, err := r.Clone(context.Background(), config.Repo{Name: "example-org/web"})
	want := filepath.Join(root, "web")
	if err != nil || got != want {
		t.Fatalf("Clone = %q, %v", got, err)
	}
	last := f.calls[len(f.calls)-1]
	if strings.Join(last[1:], " ") != "gh repo clone example-org/web "+want {
		t.Errorf("command = %v", last)
	}
	if _, err := r.Clone(context.Background(), config.Repo{Name: "example-org/web"}); err == nil {
		t.Error("cloning over an existing directory should fail")
	}
	if _, err := (Resolver{Run: f}).Clone(context.Background(), config.Repo{Name: "example-org/web"}); err == nil {
		t.Error("cloning without roots should fail")
	}
}
