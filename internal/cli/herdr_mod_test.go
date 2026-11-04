package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHerdrInstallOnDesktopInstallsTheMod(t *testing.T) {
	h, _ := desktopHarness(t)
	home := filepath.Join(t.TempDir(), "herdr-app")
	h.env["HERDR_APP_HOME"] = home
	h.run.missing["herdr-app"] = true
	stale := filepath.Join(home, "mods", "tkt", "lib", "gone.js")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.exec("herdr", "install", "--no-keys"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	dir := filepath.Join(home, "mods", "tkt")
	for _, f := range []string{"main.js", "mod.json", "lib/client.js", "lib/menus.js"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not installed: %v", f, err)
		}
	}
	if v, err := os.ReadFile(filepath.Join(dir, "lib", "version.js")); err != nil || !strings.Contains(string(v), `export const MOD_VERSION = "`+Version+`";`) {
		t.Errorf("version.js = %q, %v", v, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a file the mod no longer has was left: %v", err)
	}
	if !strings.Contains(h.out.String(), "Installed the tkt mod into Herdr desktop") {
		t.Errorf("output = %q", h.out)
	}
	if code := h.exec("herdr", "uninstall"); code != 0 {
		t.Fatalf("uninstall exit %d: %s", code, h.errOut)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uninstall left the mod: %v", err)
	}
}

func TestHerdrInstallOnDesktopNeverTouchesOtherMods(t *testing.T) {
	h, _ := desktopHarness(t)
	home := filepath.Join(t.TempDir(), "herdr-app")
	h.env["HERDR_APP_HOME"] = home
	h.run.missing["herdr-app"] = true
	other := filepath.Join(home, "mods", "spaces", "main.js")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.exec("herdr", "install", "--no-keys")
	h.exec("herdr", "uninstall")
	if b, err := os.ReadFile(other); err != nil || string(b) != "theirs" {
		t.Errorf("another mod changed: %q, %v", b, err)
	}
}
