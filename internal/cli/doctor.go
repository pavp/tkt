package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/repos"
)

type checkStatus string

const (
	statusOK   checkStatus = "ok"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
)

type check struct {
	Name   string      `json:"check"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail"`
	Hint   string      `json:"hint,omitempty"`
}

func newDoctorCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration, credentials, tools and repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := a.doctor(cmd)
			failed := false
			for _, c := range checks {
				failed = failed || c.Status == statusFail
			}
			if a.jsonOut {
				if err := a.printJSON(checks); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					mark := map[checkStatus]string{statusOK: "✓", statusWarn: "!", statusFail: "✗"}[c.Status]
					a.printf("%s %-10s %s\n", mark, c.Name, c.Detail)
					if c.Hint != "" {
						a.printf("  %-10s → %s\n", "", c.Hint)
					}
				}
			}
			if failed {
				return errQuiet
			}
			return nil
		},
	}
}

func (a *App) doctor(cmd *cobra.Command) []check {
	var checks []check
	c, err := a.LoadConfig()
	switch {
	case errors.Is(err, config.ErrNotConfigured):
		checks = append(checks, check{"config", statusFail, a.ConfigPath() + " not found", "run `tkt init`"})
	case err != nil:
		checks = append(checks, check{"config", statusFail, err.Error(), "fix the file or run `tkt init` again"})
	default:
		checks = append(checks, check{"config", statusOK, a.ConfigPath(), ""})
	}

	for _, tool := range []struct {
		name     string
		required bool
		why      string
	}{
		{"git", true, "needed to find worktrees and branches"},
		{"gh", true, "needed for GitHub access (`gh auth login`)"},
		{"claude", true, "needed by `tkt start` to open the agent"},
		{"herdr", false, "optional: enables the Herdr plugin"},
	} {
		if p, err := a.Run.LookPath(tool.name); err == nil {
			checks = append(checks, check{tool.name, statusOK, p, ""})
		} else if tool.required {
			checks = append(checks, check{tool.name, statusFail, "not found in PATH", tool.why})
		} else {
			checks = append(checks, check{tool.name, statusWarn, "not found in PATH", tool.why})
		}
	}

	if c == nil {
		return checks
	}
	js := a.jiraStatus(c)
	if js.Token == "present" {
		checks = append(checks, check{"jira", statusOK, fmt.Sprintf("%s · %s · token from %s", js.Site, js.Email, js.Source), ""})
	} else {
		checks = append(checks, check{"jira", statusFail, fmt.Sprintf("%s · %s · no token", js.Site, js.Email), "run `tkt auth jira`"})
	}
	for _, g := range a.githubStatuses(cmd, c) {
		if g.OK {
			checks = append(checks, check{"github", statusOK, fmt.Sprintf("%s via gh account %s", g.Owner, g.Account), ""})
		} else {
			checks = append(checks, check{"github", statusFail, fmt.Sprintf("%s via gh account %s", g.Owner, g.Account), g.Error})
		}
	}
	res := a.Resolver(c)
	for _, r := range c.Repos() {
		dir, err := res.Resolve(cmd.Context(), r)
		switch {
		case err == nil:
			checks = append(checks, check{"repo", statusOK, r.Name + " → " + dir, ""})
		case errors.Is(err, repos.ErrNotFound):
			checks = append(checks, check{"repo", statusWarn, r.Name + " not cloned locally", "`tkt start` offers to clone it; or set its path in the config"})
		default:
			checks = append(checks, check{"repo", statusFail, err.Error(), "fix its path in the config"})
		}
	}
	if len(c.Spaces) == 0 {
		checks = append(checks, check{"spaces", statusWarn, "no Jira spaces configured", "add [[spaces]] to the config or run `tkt init`"})
	}
	return checks
}
