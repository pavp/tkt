package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/herdrplugin"
	"github.com/pavp/tkt/internal/paths"
)

// syncHerdrPlugin brings an installed Herdr plugin up to this release, which a daemon of a new release does as it
// starts, so an upgrade needs no `tkt herdr install`: the manifest is rewritten and linked again, and a shortcut tkt
// moved follows inside tkt's own block of the config, backed up first. A shortcut the user never accepted is only
// announced, once, since adding it would write their config unasked. A dev build, or no plugin, changes nothing.
// Herdr's startup hook runs it too, and the shortcuts are retried until they succeed for this release.
func (a *App) syncHerdrPlugin(ctx context.Context) {
	if _, ok := parseVersion(Version); !ok {
		return
	}
	dir := filepath.Join(a.Env.DataDir(), "herdr-plugin")
	manifest := filepath.Join(dir, herdrplugin.FileName)
	current, err := os.ReadFile(manifest)
	if err != nil {
		return
	}
	synced, _ := os.ReadFile(a.herdrSyncedFile())
	fresh := strings.Contains(string(current), "version = "+strconv.Quote(Version))
	if fresh && strings.TrimSpace(string(synced)) == Version {
		return
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(a.Stderr, "%s herdr plugin: %s\n", a.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	}
	if !fresh {
		exe, err := a.Executable()
		if err != nil {
			logf("locate the tkt binary: %v", err)
			return
		}
		if err := paths.WritePrivateFile(manifest, []byte(herdrplugin.Render(exe, Version))); err != nil {
			logf("%v", err)
			return
		}
		if err := a.NewHerdr().LinkPlugin(ctx, dir); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already") {
			logf("link: %v", err)
		}
	}
	if err := a.syncHerdrKeys(ctx); err != nil {
		logf("keybindings: %v (retried at the next start)", err)
		return
	}
	_ = paths.WritePrivateFile(a.herdrSyncedFile(), []byte(Version+"\n"))
}

func (a *App) herdrSyncedFile() string { return filepath.Join(a.Env.DataDir(), "herdr-synced") }

// syncHerdrKeys moves the shortcuts tkt retired inside its own block and announces the ones the user never added.
func (a *App) syncHerdrKeys(ctx context.Context) error {
	path := a.herdrConfigPath()
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	defaults, err := a.Run.Run(ctx, "", a.herdrBin(), []string{"--default-config"}, nil)
	if err != nil {
		return err
	}
	kept, dropped := herdrplugin.DropRetired(string(current))
	plan, err := herdrplugin.PlanKeys(kept, string(defaults))
	if err != nil {
		return err
	}
	var moved, fresh []herdrplugin.Binding
	for _, b := range plan.Add {
		if slices.ContainsFunc(dropped, func(d herdrplugin.Binding) bool { return d.Action == b.Action }) {
			moved = append(moved, b)
		} else {
			fresh = append(fresh, b)
		}
	}
	a.announceHerdrKeys(fresh)
	if len(dropped) == 0 {
		return nil
	}
	if err := os.WriteFile(path+".bak-tkt", current, 0o644); err != nil {
		return err
	}
	next := kept
	if len(moved) > 0 {
		next = herdrplugin.AddBlock(kept, moved)
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return err
	}
	return a.NewHerdr().ReloadConfig(ctx)
}

func (a *App) herdrNoticeFile() string  { return filepath.Join(a.Env.DataDir(), "herdr-notice") }
func (a *App) herdrOfferedFile() string { return filepath.Join(a.Env.DataDir(), "herdr-keys-offered") }

// announceHerdrKeys leaves a notice for the board about shortcuts it never offered before; each is offered once.
func (a *App) announceHerdrKeys(bs []herdrplugin.Binding) {
	offered, _ := os.ReadFile(a.herdrOfferedFile())
	seen := strings.Fields(string(offered))
	var news []herdrplugin.Binding
	for _, b := range bs {
		if !slices.Contains(seen, b.Key) {
			news = append(news, b)
			seen = append(seen, b.Key)
		}
	}
	if len(news) == 0 {
		return
	}
	_ = paths.WritePrivateFile(a.herdrOfferedFile(), []byte(strings.Join(seen, "\n")+"\n"))
	_ = paths.WritePrivateFile(a.herdrNoticeFile(), []byte("new Herdr shortcut "+herdrplugin.Describe(news)+" · run `tkt herdr install` to add it"))
}

// takeHerdrNotice returns the notice the plugin's sync left for the board, once.
func (a *App) takeHerdrNotice() string {
	b, err := os.ReadFile(a.herdrNoticeFile())
	if err != nil {
		return ""
	}
	_ = os.Remove(a.herdrNoticeFile())
	return string(b)
}
