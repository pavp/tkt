package cli

import (
	"errors"
	"io/fs"
	"testing"
)

func TestStableExecutablePrefersTheSameBinaryOnPath(t *testing.T) {
	links := map[string]string{
		"/opt/homebrew/bin/tkt":                  "/opt/homebrew/Cellar/tkt/0.1.0/bin/tkt",
		"/opt/homebrew/Cellar/tkt/0.1.0/bin/tkt": "/opt/homebrew/Cellar/tkt/0.1.0/bin/tkt",
		"/tmp/tkt":                               "/tmp/tkt",
	}
	eval := func(p string) (string, error) {
		if r, ok := links[p]; ok {
			return r, nil
		}
		return "", &fs.PathError{Op: "lstat", Path: p, Err: fs.ErrNotExist}
	}
	tests := []struct {
		name, exe, onPath, want string
	}{
		{"homebrew symlink survives upgrades", "/opt/homebrew/Cellar/tkt/0.1.0/bin/tkt", "/opt/homebrew/bin/tkt", "/opt/homebrew/bin/tkt"},
		{"another tkt on PATH is not used", "/tmp/tkt", "/opt/homebrew/bin/tkt", "/tmp/tkt"},
		{"not on PATH", "/opt/homebrew/Cellar/tkt/0.1.0/bin/tkt", "", "/opt/homebrew/Cellar/tkt/0.1.0/bin/tkt"},
		{"an upgrade removed the running binary", "/opt/homebrew/Cellar/tkt/0.0.9/bin/tkt", "/opt/homebrew/bin/tkt", "/opt/homebrew/bin/tkt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			look := func(string) (string, error) {
				if tt.onPath == "" {
					return "", errors.New("not found")
				}
				return tt.onPath, nil
			}
			got, err := stableExecutable(tt.exe, look, eval)
			if err != nil || got != tt.want {
				t.Errorf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestStableExecutableFailsWhenTheRemovedBinaryIsNotOnPath(t *testing.T) {
	eval := func(p string) (string, error) { return "", &fs.PathError{Op: "lstat", Path: p, Err: fs.ErrNotExist} }
	look := func(string) (string, error) { return "", errors.New("not found") }
	if got, err := stableExecutable("/opt/homebrew/Cellar/tkt/0.0.9/bin/tkt", look, eval); err == nil {
		t.Errorf("got %q, want an error", got)
	}
}
