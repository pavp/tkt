package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestFolderPickerCommand(t *testing.T) {
	name, args, ok := folderPicker(pickerEnv{goos: "darwin"}, "/Users/dev/src")
	if !ok || name != "osascript" || len(args) != 2 || !strings.Contains(args[1], `choose folder`) || !strings.Contains(args[1], `POSIX file "/Users/dev/src"`) {
		t.Errorf("darwin = %s %q %v", name, args, ok)
	}
	name, args, ok = folderPicker(pickerEnv{goos: "linux", has: map[string]bool{"zenity": true}}, "/home/dev")
	if !ok || name != "zenity" || !strings.Contains(strings.Join(args, " "), "--directory") {
		t.Errorf("zenity = %s %q %v", name, args, ok)
	}
	name, args, ok = folderPicker(pickerEnv{goos: "linux", has: map[string]bool{"kdialog": true}}, "/home/dev")
	if !ok || name != "kdialog" || args[0] != "--getexistingdirectory" {
		t.Errorf("kdialog = %s %q %v", name, args, ok)
	}
	if _, _, ok := folderPicker(pickerEnv{goos: "linux"}, "/home/dev"); ok {
		t.Error("no picker should be offered without zenity or kdialog")
	}
}

func TestPickedPath(t *testing.T) {
	env := pickerEnv{goos: "darwin", home: "/Users/dev"}
	if got := pickedPath(env, "/Users/dev/code/web/\n"); got != "~/code/web" {
		t.Errorf("darwin = %q", got)
	}
	if got := pickedPath(env, ""); got != "" {
		t.Errorf("cancel = %q", got)
	}
}

func TestPickerCancelled(t *testing.T) {
	for msg, want := range map[string]bool{
		"osascript: exit status 1: 0:93: execution error: User canceled. (-128)": true,
		"zenity: exit status 1":                    true,
		"osascript: exit status 2: not authorized": false,
	} {
		if got := pickerCancelled(errors.New(msg)); got != want {
			t.Errorf("%q: got %v", msg, got)
		}
	}
}
