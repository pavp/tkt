package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func envWith(goos string, vars map[string]string) Env {
	return Env{GOOS: goos, Home: "/home/u", Getenv: func(k string) string { return vars[k] }}
}

func TestDirectories(t *testing.T) {
	tests := []struct {
		name       string
		env        Env
		wantConfig string
		wantData   string
	}{
		{"linux defaults", envWith("linux", nil), "/home/u/.config/tkt", "/home/u/.local/share/tkt"},
		{"macOS data dir", envWith("darwin", nil), "/home/u/.config/tkt", "/home/u/Library/Application Support/tkt"},
		{"XDG overrides", envWith("darwin", map[string]string{"XDG_CONFIG_HOME": "/x/c", "XDG_DATA_HOME": "/x/d"}), "/x/c/tkt", "/x/d/tkt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.env.ConfigDir(); got != tt.wantConfig {
				t.Errorf("ConfigDir() = %q, want %q", got, tt.wantConfig)
			}
			if got := tt.env.DataDir(); got != tt.wantData {
				t.Errorf("DataDir() = %q, want %q", got, tt.wantData)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	e := envWith("linux", nil)
	for in, want := range map[string]string{"~": "/home/u", "~/dev": "/home/u/dev", "/abs": "/abs", "rel/~": "rel/~"} {
		if got := e.ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWritePrivateFileSetsModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "token")
	if err := WritePrivateFile(path, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", fi.Mode().Perm())
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", di.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "secret" {
		t.Errorf("content = %q, %v", got, err)
	}
}
