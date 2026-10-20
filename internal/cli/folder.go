package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pavp/tkt/internal/credstore"
)

// pickerEnv is what choosing a folder picker depends on, kept apart from App so it can be tested.
type pickerEnv struct {
	goos string
	wsl  bool
	home string
	has  map[string]bool
}

var osaQuoted = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// folderPicker returns the command that shows the system's folder chooser starting at start, or false
// when the system has none.
func folderPicker(env pickerEnv, start string) (string, []string, bool) {
	const prompt = "Choose the local clone"
	switch {
	case env.goos == "darwin":
		script := fmt.Sprintf(`POSIX path of (choose folder with prompt "%s" default location (POSIX file "%s"))`, prompt, osaQuoted.Replace(start))
		return "osascript", []string{"-e", script}, true
	case env.has["zenity"]:
		return "zenity", []string{"--file-selection", "--directory", "--title=" + prompt, "--filename=" + strings.TrimSuffix(start, "/") + "/"}, true
	case env.has["kdialog"]:
		return "kdialog", []string{"--getexistingdirectory", start, "--title", prompt}, true
	}
	return "", nil, false
}

// pickedPath cleans a chooser's output into a config path, writing the home folder as "~" so the
// config stays portable. Empty means the user cancelled.
func pickedPath(env pickerEnv, out string) string {
	p := strings.TrimSpace(out)
	if p == "" {
		return ""
	}
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	return tilde(env.home, p)
}

// tilde writes paths under home with "~", as the config keeps them.
func tilde(home, p string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (a *App) tildePath(p string) string { return tilde(a.Env.Home, p) }

func (a *App) pickerEnv() pickerEnv {
	env := pickerEnv{goos: a.Env.GOOS, home: a.Env.Home, has: map[string]bool{}}
	if a.Env.GOOS != "darwin" && credstore.IsWSL(a.Env.Getenv, os.ReadFile) {
		env.wsl = true
	}
	for _, tool := range []string{"zenity", "kdialog"} {
		if _, err := a.Run.LookPath(tool); err == nil {
			env.has[tool] = true
		}
	}
	return env
}

// nativePicker reports whether a folder chooser can open where the user sees it: never over SSH, where it
// would open on the remote desktop, nor under WSL, whose Windows dialog can hang without showing.
func (a *App) nativePicker() bool {
	if a.overSSH() {
		return false
	}
	env := a.pickerEnv()
	if env.wsl {
		return false
	}
	_, _, ok := folderPicker(env, "/")
	return ok
}

var errNoPicker = errors.New("no folder picker on this system (install zenity or kdialog); type the path instead")

// pickFolder shows the system's folder chooser and returns the chosen folder, or "" when cancelled.
func (a *App) pickFolder(ctx context.Context, start string) (string, error) {
	env := a.pickerEnv()
	start = a.Env.ExpandHome(start)
	if fi, err := os.Stat(start); err != nil || !fi.IsDir() {
		start = a.Env.Home
	}
	name, args, ok := folderPicker(env, filepath.Clean(start))
	if !ok {
		return "", errNoPicker
	}
	out, err := a.Run.Run(ctx, "", name, args, nil)
	if err != nil {
		if pickerCancelled(err) {
			return "", nil
		}
		return "", err
	}
	return pickedPath(env, string(out)), nil
}

// pickerCancelled recognizes a cancelled chooser: osascript reports error -128, zenity and kdialog exit
// with status 1.
func pickerCancelled(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "-128") || strings.Contains(msg, "exit status 1")
}
