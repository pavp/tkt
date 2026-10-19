package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func NewRootCmd(a *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "tkt",
		Short:         "Your sprint tickets across Jira spaces, with their pull requests and CI",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(a.Stdin)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.PersistentFlags().StringVar(&a.configPath, "config", a.configPath, "config file (default ~/.config/tkt/config.toml)")
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "print machine-readable JSON")
	root.AddCommand(newAuthCmd(a), newDoctorCmd(a), newListCmd(a), newShowCmd(a), newContextCmd(a), newLinkCmd(a), newStartCmd(a), newInitCmd(a), newHerdrCmd(a), newBoardCmd(a), newFixCmd(a))
	return root
}

// Execute runs tkt and returns the process exit code.
func Execute() int {
	a, err := NewApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tkt:", err)
		return 1
	}
	return run(a, os.Args[1:])
}

func run(a *App, args []string) int {
	root := NewRootCmd(a)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		if !errors.Is(err, errQuiet) {
			fmt.Fprintln(a.Stderr, "tkt:", err)
		}
		return 1
	}
	return 0
}
