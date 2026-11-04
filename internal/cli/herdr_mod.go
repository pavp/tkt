package cli

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/herdrmod"
)

// modKey opens the mod in Herdr desktop; Herdr desktop takes cmd chords only, and cmd-t is its new tab.
const modKey, modCommand = "cmd-j", "mod.open:tkt"

// herdrAppHome is Herdr desktop's app folder on this machine: HERDR_APP_HOME, else ~/.config/herdr-app.
func (a *App) herdrAppHome() string {
	if h := a.Env.Getenv("HERDR_APP_HOME"); h != "" {
		return h
	}
	return filepath.Join(a.Env.Home, ".config", "herdr-app")
}

func (a *App) herdrModDir() string { return filepath.Join(a.herdrAppHome(), "mods", "tkt") }

// modFiles is the mod tkt carries, with lib/version.js naming this tkt's version so the mod can tell when the
// tkt it talks to differs.
func modFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	files := herdrmod.Files()
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(files, path)
		out[path] = b
		return err
	})
	out["lib/version.js"] = []byte(fmt.Sprintf("export const MOD_VERSION = %q;\n", Version))
	return out, err
}

// installDesktopMod writes the mod into Herdr desktop's mods folder, replacing the previous copy whole, so a
// file the mod no longer has does not linger; Herdr reloads a mod when its files change. Herdr desktop's window
// may run on another machine of the core, which this one cannot write to: that machine then fetches the mod.
func (a *App) installDesktopMod(ctx context.Context) (bool, error) {
	home := a.herdrAppHome()
	if _, err := os.Stat(home); err != nil {
		a.printf("Herdr desktop's window runs on another machine (no %s here). In a terminal of that machine, run:\n\n"+
			"  hc exec --on <this machine's name in Herdr> -- tkt herdr export-mod | tar -x -C ~/.config/herdr-app/mods\n\n"+
			"then press %s there to open tkt; the mod keeps itself in step with this tkt afterwards.\n", home, modKey)
		return false, nil
	}
	dir := a.herdrModDir()
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	files, err := modFiles()
	if err != nil {
		return false, err
	}
	for path, b := range files {
		if err := writeModFile(filepath.Join(dir, path), b); err != nil {
			return false, err
		}
	}
	if _, err := a.Run.LookPath("herdr-app"); err == nil {
		if out, err := a.Run.Run(ctx, "", "herdr-app", []string{"check", dir}, nil); err != nil {
			return false, fmt.Errorf("Herdr desktop does not load the tkt mod it was given (%s): %s", dir, out)
		}
	}
	a.printf("✓ Installed the tkt mod into Herdr desktop (%s)\n", dir)
	return true, nil
}

// exportMod writes the mod as a tar archive of a tkt/ folder, for the machine that shows Herdr desktop's window.
func (a *App) exportMod(w io.Writer) error {
	files, err := modFiles()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	tw := tar.NewWriter(w)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: "tkt/" + n, Mode: 0o644, Size: int64(len(files[n])), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(files[n]); err != nil {
			return err
		}
	}
	return tw.Close()
}

func (a *App) desktopKeysPath() string { return filepath.Join(a.herdrAppHome(), "keys.json") }

func (a *App) readDesktopKeys() (map[string]map[string]string, []byte, error) {
	keys := map[string]map[string]string{}
	b, err := os.ReadFile(a.desktopKeysPath())
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &keys); err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", a.desktopKeysPath(), err)
		}
	}
	return keys, b, nil
}

func (a *App) writeDesktopKeys(keys map[string]map[string]string, before []byte) error {
	path := a.desktopKeysPath()
	if len(before) > 0 {
		if err := os.WriteFile(path+".bak-tkt", before, 0o644); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
	}
	b, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// addDesktopKey binds cmd-j to open the mod, asking first unless yes, and never replacing a key in use.
func (a *App) addDesktopKey(yes bool) error {
	keys, before, err := a.readDesktopKeys()
	if err != nil {
		return err
	}
	switch now := keys["global"][modKey]; {
	case now == modCommand:
		a.printf("✓ Already bound: %s opens tkt\n", modKey)
		return nil
	case now != "":
		a.printf("! %s is taken by %q; bind %s to another cmd key in %s yourself to open tkt\n", modKey, now, modCommand, a.desktopKeysPath())
		return nil
	}
	if !yes {
		ans, err := a.readLine(fmt.Sprintf("Add %s to open tkt in Herdr desktop? [Y/n] ", modKey))
		if err != nil || strings.HasPrefix(strings.ToLower(ans), "n") {
			a.printf("Skipped the key; `tkt herdr install --keys` adds it later\n")
			return nil
		}
	}
	if keys["global"] == nil {
		keys["global"] = map[string]string{}
	}
	keys["global"][modKey] = modCommand
	if err := a.writeDesktopKeys(keys, before); err != nil {
		return err
	}
	a.printf("✓ %s opens tkt in Herdr desktop\n", modKey)
	return nil
}

func (a *App) removeDesktopKey() error {
	keys, before, err := a.readDesktopKeys()
	if err != nil || keys["global"][modKey] != modCommand {
		return err
	}
	delete(keys["global"], modKey)
	return a.writeDesktopKeys(keys, before)
}

func newExportModCmd(a *App) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "export-mod",
		Short: "Print the Herdr desktop mod as a tar archive, for the machine that shows Herdr desktop's window",
		Long: "Print the Herdr desktop mod as a tar archive of a tkt/ folder, for the machine that shows Herdr desktop's\n" +
			"window: hc exec --on <this machine> -- tkt herdr export-mod | tar -x -C ~/.config/herdr-app/mods.\n" +
			"--json prints {version, files}, which the mod reads to keep itself in step with this tkt.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if !asJSON {
				return a.exportMod(a.Stdout)
			}
			files, err := modFiles()
			if err != nil {
				return err
			}
			text := map[string]string{}
			for n, b := range files {
				text[n] = string(b)
			}
			b, err := json.Marshal(map[string]any{"version": Version, "files": text})
			if err != nil {
				return err
			}
			a.printf("%s\n", b)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print {version, files} instead of a tar archive")
	return cmd
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
	if err := a.removeDesktopKey(); err != nil {
		return err
	}
	a.printf("✓ Removed the tkt mod from Herdr desktop\n")
	return nil
}
