package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Env holds the process facts the directory layout depends on, so tests can vary them.
type Env struct {
	GOOS   string
	Home   string
	Getenv func(string) string
}

func Current() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return Env{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv}, nil
}

func (e Env) ConfigDir() string {
	if x := e.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "tkt")
	}
	return filepath.Join(e.Home, ".config", "tkt")
}

func (e Env) ConfigFile() string {
	return filepath.Join(e.ConfigDir(), "config.toml")
}

func (e Env) DataDir() string {
	if x := e.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "tkt")
	}
	if e.GOOS == "darwin" {
		return filepath.Join(e.Home, "Library", "Application Support", "tkt")
	}
	return filepath.Join(e.Home, ".local", "share", "tkt")
}

// ExpandHome turns a leading "~" into the home directory.
func (e Env) ExpandHome(p string) string {
	if p == "~" {
		return e.Home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(e.Home, p[2:])
	}
	return p
}

// EnsurePrivateDir creates dir with mode 0700 and tightens it if it already exists with wider permissions.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict %s: %w", dir, err)
	}
	return nil
}

// WritePrivateFile writes data atomically with mode 0600.
func WritePrivateFile(path string, data []byte) error {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
