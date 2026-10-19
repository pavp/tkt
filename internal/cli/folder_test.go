package cli

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWSLPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{`\\wsl.localhost\Ubuntu\home\dev\code\web`, "/home/dev/code/web"},
		{`\\wsl$\Ubuntu\home\dev`, "/home/dev"},
		{`\\WSL.LOCALHOST\Ubuntu`, "/"},
		{`C:\Users\dev\code`, "/mnt/c/Users/dev/code"},
		{`d:\work`, "/mnt/d/work"},
		{"/home/dev", "/home/dev"},
	}
	for _, tt := range tests {
		if got := wslPath(tt.in); got != tt.want {
			t.Errorf("wslPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFolderPickerCommand(t *testing.T) {
	name, args, ok := folderPicker(pickerEnv{goos: "darwin"}, "/Users/dev/src")
	if !ok || name != "osascript" || len(args) != 2 || !strings.Contains(args[1], `choose folder`) || !strings.Contains(args[1], `POSIX file "/Users/dev/src"`) {
		t.Errorf("darwin = %s %q %v", name, args, ok)
	}
	name, args, ok = folderPicker(pickerEnv{goos: "linux", wsl: true, distro: "Ubuntu", powershell: "powershell.exe"}, "/home/dev/src")
	if !ok || name != "powershell.exe" || args[len(args)-2] != "-EncodedCommand" {
		t.Errorf("wsl = %s %q %v", name, args, ok)
	}
	if script := decodePS(t, args[len(args)-1]); !strings.Contains(script, `\\wsl.localhost\Ubuntu\home\dev\src`) || !strings.Contains(script, "FolderBrowserDialog") {
		t.Errorf("wsl dialog should open in the Linux home:\n%s", script)
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
	wsl := pickerEnv{goos: "linux", wsl: true, home: "/home/dev"}
	if got := pickedPath(wsl, `\\wsl.localhost\Ubuntu\home\dev\code\web`+"\r\n"); got != "~/code/web" {
		t.Errorf("wsl = %q", got)
	}
	if got := pickedPath(wsl, `C:\work\web`); got != "/mnt/c/work/web" {
		t.Errorf("wsl windows folder = %q", got)
	}
	if got := pickedPath(env, ""); got != "" {
		t.Errorf("cancel = %q", got)
	}
}

func decodePS(t *testing.T, enc string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
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
