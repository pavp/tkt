package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
)

func newBoardCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "board",
		Short: "Show the board for the Herdr popup (offers setup when tkt is not configured)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.LoadConfig()
			if errors.Is(err, config.ErrNotConfigured) {
				if _, err := a.readLine("tkt isn't set up · press Enter to set up (Ctrl-C to close) "); err != nil {
					return nil
				}
				return a.runInit(cmd)
			}
			if err != nil {
				return err
			}
			snap, stale, err := a.loadSnapshot(cmd.Context(), c, false)
			if err != nil {
				fmt.Fprintf(a.Stderr, "tkt: %v\n", err)
			} else {
				a.renderList(snap, stale)
			}
			if _, err := a.readLine("\nPress Enter to close "); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			return nil
		},
	}
}
