package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/tui"
)

func (a *App) boardPaneFile() string { return filepath.Join(a.Env.DataDir(), "board-pane") }

func (a *App) boardStateFile() string { return filepath.Join(a.Env.DataDir(), "board-state.json") }

// loadBoardState reads what the board remembers; a missing or unreadable file starts afresh.
func (a *App) loadBoardState() tui.BoardState {
	var s tui.BoardState
	if b, err := os.ReadFile(a.boardStateFile()); err == nil && json.Unmarshal(b, &s) != nil {
		return tui.BoardState{}
	}
	return s
}

func (a *App) saveBoardState(s tui.BoardState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.Env.DataDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(a.boardStateFile(), b, 0o600)
}

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
