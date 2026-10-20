package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pavp/tkt/internal/herdr"
)

func (a *App) boardPaneFile() string { return filepath.Join(a.Env.DataDir(), "board-pane") }

// claimBoardPane records the Herdr pane the board runs in, so open-board can bring that board to another
// workspace instead of leaving it behind; the returned func removes the record unless a newer board took it.
func (a *App) claimBoardPane() func() {
	pane, ws := a.Env.Getenv("HERDR_PANE_ID"), a.Env.Getenv("HERDR_WORKSPACE_ID")
	if a.Env.Getenv("HERDR_ENV") != "1" || pane == "" || ws == "" {
		return func() {}
	}
	record := []byte(pane + " " + ws)
	file := a.boardPaneFile()
	if os.MkdirAll(filepath.Dir(file), 0o700) != nil || os.WriteFile(file, record, 0o600) != nil {
		return func() {}
	}
	return func() {
		if b, err := os.ReadFile(file); err == nil && string(b) == string(record) {
			_ = os.Remove(file)
		}
	}
}

// herdrOpenBoard keeps one board: it focuses the board already open in this workspace, moves one open
// elsewhere here (Herdr would otherwise keep showing it there), or opens a new one. Herdr opens popups on
// the active pane and rejects a workspace_id for them.
func (a *App) herdrOpenBoard(ctx context.Context) error {
	h := a.NewHerdr()
	current := a.Env.Getenv("HERDR_WORKSPACE_ID")
	if b, err := os.ReadFile(a.boardPaneFile()); err == nil {
		if pane, ws, ok := strings.Cut(strings.TrimSpace(string(b)), " "); ok {
			if ws == current && h.FocusPluginPane(ctx, pane) == nil {
				return nil
			}
			_ = h.ClosePluginPane(ctx, pane)
		}
	}
	return h.OpenPluginPane(ctx, herdr.PaneOpen{PluginID: a.pluginID(), Entrypoint: "board"})
}
