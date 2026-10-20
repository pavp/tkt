package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

func newListCmd(a *App) *cobra.Command {
	var space string
	var needsYou, cached bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your sprint tickets across spaces, with pull requests and CI",
		Long: "List the tickets assigned to you in open sprints of every configured space, with their pull\n" +
			"requests and CI, plus pull requests that request your review. Items that need you come first.\n" +
			"When Jira or GitHub cannot be reached, the last cached board is shown and marked as stale.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.LoadConfig()
			if err != nil {
				return err
			}
			if space != "" {
				if _, ok := c.Space(space); !ok {
					return fmt.Errorf("space %s is not configured (configured: %s)", space, strings.Join(c.SpaceKeys(), ", "))
				}
			}
			// The daemon's board is current and its sync already wrote the Herdr sidebar.
			snap, fromDaemon := board.Snapshot{}, false
			if !cached {
				snap, fromDaemon = a.daemonSnapshot(cmd.Context())
			}
			var stale error
			if !fromDaemon {
				snap, stale, err = a.loadSnapshot(cmd.Context(), c, cached)
				if err != nil {
					return err
				}
				if stale == nil && !cached {
					a.refreshSidebarQuietly(cmd.Context(), c, snap)
				}
			}
			snap = snap.Filter(space).WithoutBots()
			if needsYou {
				snap = onlyNeedsYou(snap)
			}
			if a.jsonOut {
				out := map[string]any{"snapshot": snap, "needs_you": snap.NeedsYou(), "stale": stale != nil}
				if stale != nil {
					out["stale_reason"] = stale.Error()
				}
				return a.printJSON(out)
			}
			a.renderList(snap, stale)
			return nil
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "only this Jira space, e.g. DEMO")
	cmd.Flags().BoolVar(&needsYou, "needs-you", false, "only tickets blocked on you (failing CI or requested changes)")
	cmd.Flags().BoolVar(&cached, "cached", false, "show the cached board without contacting Jira or GitHub")
	return cmd
}

func onlyNeedsYou(s board.Snapshot) board.Snapshot {
	out := s
	out.Tickets, out.Reviews = nil, nil
	for _, t := range s.Tickets {
		if len(t.Attention()) > 0 {
			out.Tickets = append(out.Tickets, t)
		}
	}
	return out
}

func (a *App) renderList(s board.Snapshot, stale error) {
	now := a.Now()
	if len(s.Sprints) > 0 {
		parts := make([]string, len(s.Sprints))
		for i, sp := range s.Sprints {
			parts[i] = board.SprintLabel(sp, now)
		}
		a.printf("%s\n\n", strings.Join(parts, "   "))
	}

	needs := s.NeedsYou()
	if len(needs) > 0 {
		a.printf("NEEDS YOU\n")
		tw := tabwriter.NewWriter(a.Stdout, 0, 2, 2, ' ', 0)
		for _, n := range needs {
			fmt.Fprintf(tw, "  !\t%s\t%s\t%s\n", n.Ref, n.Reason, truncate(n.Title, 60))
		}
		tw.Flush()
		a.printf("\n")
	}

	if len(s.Reviews) > 0 {
		a.printf("REVIEW REQUESTED\n")
		tw := tabwriter.NewWriter(a.Stdout, 0, 2, 2, ' ', 0)
		for _, pr := range s.Reviews {
			fmt.Fprintf(tw, "  #%d\t%s\n", pr.Number, truncate(pr.Repo+" · "+pr.Title, 70))
		}
		tw.Flush()
		a.printf("\n")
	}

	if len(s.Tickets) == 0 {
		a.printf("No tickets in your open sprints.\n")
	} else {
		tw := tabwriter.NewWriter(a.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "KEY\tSTATUS\tPRS\tTITLE")
		for _, t := range board.Sorted(s.Tickets) {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Key, t.Status, prSummary(t.PRs), truncate(t.Summary, 60))
			for _, st := range t.Subtasks {
				fmt.Fprintf(tw, "  ↳ %s\t%s\t%s\t%s\n", st.Key, st.Status, prSummary(st.PRs), truncate(st.Summary, 56))
			}
		}
		tw.Flush()
	}

	for _, w := range s.Warnings {
		fmt.Fprintf(a.Stderr, "! %s\n", w)
	}
	if stale != nil {
		fmt.Fprintf(a.Stderr, "! showing the cached board from %s: %v\n", s.FetchedAt.Local().Format("Jan 2 15:04"), stale)
	} else {
		a.printf("\nsynced %s\n", ago(s.FetchedAt, now))
	}
}

func prSummary(prs []ticket.PullRequest) string {
	if len(prs) == 0 {
		return "—"
	}
	open, merged, closed := ticket.SplitPRs(prs)
	parts := make([]string, 0, len(open)+1)
	for _, pr := range open {
		_, repo, _ := strings.Cut(pr.Repo, "/")
		parts = append(parts, fmt.Sprintf("%s#%d %s", repo, pr.Number, prMark(pr)))
	}
	if f := ticket.FinishedSummary(merged, closed); f != "" {
		parts = append(parts, f)
	}
	return strings.Join(parts, " ")
}

func prMark(pr ticket.PullRequest) string {
	switch {
	case pr.State == ticket.PRMerged:
		return "merged"
	case pr.State == ticket.PRClosed:
		return "closed"
	case pr.Checks == ticket.ChecksFail:
		return "✕"
	case pr.Checks == ticket.ChecksPending:
		return "○"
	case pr.Checks == ticket.ChecksPass:
		return "✓"
	}
	return "·"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return t.Local().Format("Jan 2 15:04")
}
