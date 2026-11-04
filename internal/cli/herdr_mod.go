package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pavp/tkt/internal/herdrmod"
)

// herdrAppHome is Herdr desktop's app folder on this machine: HERDR_APP_HOME, else ~/.config/herdr-app.
func (a *App) herdrAppHome() string {
	if h := a.Env.Getenv("HERDR_APP_HOME"); h != "" {
		return h
	}
	return filepath.Join(a.Env.Home, ".config", "herdr-app")
}

func (a *App) herdrModDir() string { return filepath.Join(a.herdrAppHome(), "mods", "tkt") }

// installDesktopMod writes the mod tkt carries into Herdr desktop's mods folder, replacing the previous copy
// whole, so a file the mod no longer has does not linger; Herdr reloads a mod when its files change.
func (a *App) installDesktopMod(ctx context.Context) error {
	home := a.herdrAppHome()
	if _, err := os.Stat(home); err != nil {
		return fmt.Errorf("Herdr desktop's app folder (%s) is not on this machine: run `tkt herdr install` on the machine that shows the Herdr desktop window", home)
	}
	dir := a.herdrModDir()
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	files := herdrmod.Files()
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		return writeModFile(filepath.Join(dir, path), b)
	})
	if err != nil {
		return err
	}
	if err := writeModFile(filepath.Join(dir, "lib", "version.js"), []byte(fmt.Sprintf("export const MOD_VERSION = %q;\n", Version))); err != nil {
		return err
	}
	if _, err := a.Run.LookPath("herdr-app"); err == nil {
		if out, err := a.Run.Run(ctx, "", "herdr-app", []string{"check", dir}, nil); err != nil {
			return fmt.Errorf("Herdr desktop does not load the tkt mod it was given (%s): %s", dir, out)
		}
	}
	a.printf("✓ Installed the tkt mod into Herdr desktop (%s)\n", dir)
	return nil
}

func writeModFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func (a *App) uninstallDesktopMod() error {
	if err := os.RemoveAll(a.herdrModDir()); err != nil {
		return err
	}
	a.printf("✓ Removed the tkt mod from Herdr desktop\n")
	return nil
}
