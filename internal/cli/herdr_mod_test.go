package cli

import (
	"encoding/json"
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

func desktopModHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h, _ := desktopHarness(t)
	home := filepath.Join(t.TempDir(), "herdr-app")
	h.env["HERDR_APP_HOME"] = home
	h.run.missing["herdr-app"] = true
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return h, home
}

func TestHerdrInstallOnDesktopBindsCmdJToTheMod(t *testing.T) {
	tests := []struct {
		name, keys, answer string
		args               []string
		want               string
	}{
		{"asked and agreed", `{"global":{"cmd-t":"tab.new"}}`, "\n", nil, `"cmd-j": "mod.open:tkt"`},
		{"asked and declined", `{"global":{"cmd-t":"tab.new"}}`, "n\n", nil, ""},
		{"--keys adds it without asking", "", "", []string{"--keys"}, `"cmd-j": "mod.open:tkt"`},
		{"a key in use is left alone", `{"global":{"cmd-j":"space.next"}}`, "", []string{"--keys"}, `{"global":{"cmd-j":"space.next"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, home := desktopModHarness(t)
			h.app.OS = "darwin"
			path := filepath.Join(home, "keys.json")
			if tt.keys != "" {
				if err := os.WriteFile(path, []byte(tt.keys), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			h.app.Stdin = strings.NewReader(tt.answer)
			if code := h.exec(append([]string{"herdr", "install"}, tt.args...)...); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			b, _ := os.ReadFile(path)
			if tt.want == "" && strings.Contains(string(b), "mod.open:tkt") || tt.want != "" && !strings.Contains(string(b), tt.want) {
				t.Errorf("keys.json = %s, want %s", b, tt.want)
			}
			if tt.keys != "" && strings.Contains(tt.keys, "cmd-t") && !strings.Contains(string(b), `"cmd-t": "tab.new"`) && tt.want != "" {
				t.Errorf("another key was lost: %s", b)
			}
		})
	}
}

func TestHerdrUninstallOnDesktopRemovesOnlyItsKey(t *testing.T) {
	h, home := desktopModHarness(t)
	path := filepath.Join(home, "keys.json")
	if err := os.WriteFile(path, []byte(`{"global":{"cmd-t":"tab.new"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h.exec("herdr", "install", "--keys")
	h.exec("herdr", "uninstall")
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "mod.open:tkt") || !strings.Contains(string(b), `"cmd-t": "tab.new"`) {
		t.Errorf("keys.json after uninstall = %s", b)
	}
}

func TestExportModPrintsTheModAsATarArchive(t *testing.T) {
	h, _ := desktopModHarness(t)
	if code := h.exec("herdr", "export-mod"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	out := h.out.String()
	for _, name := range []string{"tkt/main.js", "tkt/mod.json", "tkt/lib/version.js"} {
		if !strings.Contains(out, name) {
			t.Errorf("the archive lacks %s", name)
		}
	}
}

func TestHerdrInstallWithTheWindowOnAnotherMachineSaysHowToGetTheMod(t *testing.T) {
	h, _ := desktopHarness(t)
	h.env["HERDR_APP_HOME"] = filepath.Join(t.TempDir(), "not-here")
	if code := h.exec("herdr", "install", "--no-keys"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.out.String(), "hc exec --on ") || !strings.Contains(h.out.String(), "tkt herdr export-mod | tar -x -C ~/.config/herdr-app/mods") {
		t.Errorf("output = %q", h.out)
	}
}

func TestExportModAsJSONGivesTheFilesAndVersion(t *testing.T) {
	h, _ := desktopModHarness(t)
	if code := h.exec("herdr", "export-mod", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	var got struct {
		Version string            `json:"version"`
		Files   map[string]string `json:"files"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatalf("output %q: %v", h.out, err)
	}
	if got.Version != Version || !strings.Contains(got.Files["main.js"], "export default class") || !strings.Contains(got.Files["lib/version.js"], Version) {
		t.Errorf("export = version %q, %d files", got.Version, len(got.Files))
	}
}

func TestHerdrInstallBindsCtrlShiftJWhereThereIsNoCmdKey(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			h, home := desktopModHarness(t)
			h.app.OS = goos
			if code := h.exec("herdr", "install", "--keys"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			b, _ := os.ReadFile(filepath.Join(home, "keys.json"))
			if !strings.Contains(string(b), `"ctrl-shift-j": "mod.open:tkt"`) || strings.Contains(string(b), "cmd-j") {
				t.Errorf("keys.json = %s, want ctrl-shift-j bound to the mod", b)
			}
			if !strings.Contains(h.out.String(), "ctrl-shift-j opens tkt") {
				t.Errorf("install said %q, want it to name ctrl-shift-j", h.out.String())
			}
		})
	}
}

func TestHerdrUninstallRemovesTheModKeyWhicheverItIs(t *testing.T) {
	h, home := desktopModHarness(t)
	path := filepath.Join(home, "keys.json")
	if err := os.WriteFile(path, []byte(`{"global":{"cmd-t":"tab.new","ctrl-shift-j":"mod.open:tkt"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h.app.OS = "darwin"
	h.exec("herdr", "uninstall")
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "mod.open:tkt") || !strings.Contains(string(b), `"cmd-t": "tab.new"`) {
		t.Errorf("keys.json after uninstall = %s", b)
	}
}
