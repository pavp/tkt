package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/ticket"
)

func newShowCmd(a *App) *cobra.Command {
	var watch bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "show [KEY]",
		Short: "Show a ticket with its pull requests, CI, description and latest comments",
		Long: "Show a ticket with its pull requests, CI, description and latest comments. Without KEY, the ticket\n" +
			"is taken from the current git branch. --watch redraws it every --interval until interrupted, which\n" +
			"is how the Herdr ticket pane keeps it live.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.LoadConfig()
			if err != nil {
				return err
			}
			key, err := a.resolveKey(cmd.Context(), c, args)
			if err != nil {
				return err
			}
			if !watch {
				d, err := a.ticketDetail(cmd.Context(), c, key)
				if err != nil {
					return err
				}
				if a.jsonOut {
					return a.printJSON(d)
				}
				a.renderShow(a.Stdout, d)
				return nil
			}
			if interval < 10*time.Second {
				return errors.New("--interval must be at least 10s")
			}
			fmt.Fprint(a.Stdout, "\033[H\033[2J")
			if t := a.daemonTicket(cmd.Context(), key); t != nil {
				a.renderHead(a.Stdout, *t)
				fmt.Fprintln(a.Stdout)
			}
			fmt.Fprintf(a.Stdout, "Loading %s…\n", key)
			for {
				var buf bytes.Buffer
				d, err := a.ticketDetail(cmd.Context(), c, key)
				if err != nil {
					fmt.Fprintf(&buf, "%s · could not refresh: %v\n", key, err)
				} else {
					a.renderShow(&buf, d)
				}
				fmt.Fprintf(&buf, "\nrefreshed %s · every %s · Ctrl-C to stop\n", a.Now().Local().Format("15:04:05"), interval)
				fmt.Fprint(a.Stdout, "\033[H\033[2J")
				_, _ = io.Copy(a.Stdout, &buf)
				if err := a.Sleep(cmd.Context(), interval); err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return err
				}
			}
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "redraw until interrupted")
	cmd.Flags().DurationVar(&interval, "interval", time.Minute, "refresh interval for --watch")
	return cmd
}

func (a *App) renderShow(w io.Writer, d detail) {
	a.renderHead(w, d.Ticket)
	section(w, "Description", d.Description, "No description.")
	if d.Acceptance != "" {
		section(w, "Acceptance criteria", d.Acceptance, "")
	}
	if len(d.Attachments) > 0 {
		fmt.Fprintf(w, "\nAttachments (%d)\n", len(d.Attachments))
		for _, at := range d.Attachments {
			fmt.Fprintf(w, "  %s  %s  %s\n", at.Filename, at.MimeType, humanSize(at.Size))
		}
	}
	if len(d.Comments) > 0 {
		fmt.Fprintf(w, "\nLatest comments\n")
		for _, cm := range d.Comments {
			fmt.Fprintf(w, "  %s · %s\n", cm.Author, cm.Created.Local().Format("Jan 2 15:04"))
			for _, l := range strings.Split(cm.Body, "\n") {
				fmt.Fprintf(w, "    %s\n", l)
			}
		}
	}
	for _, warn := range d.Warnings {
		fmt.Fprintf(w, "\n! %s\n", warn)
	}
}

func (a *App) renderHead(w io.Writer, t ticket.Ticket) {
	fmt.Fprintf(w, "%s · %s · %s\n%s\n%s\n", t.Key, t.Type, t.Status, t.Summary, t.URL)
	if reasons := t.Attention(); len(reasons) > 0 {
		parts := make([]string, len(reasons))
		for i, r := range reasons {
			parts[i] = string(r)
		}
		fmt.Fprintf(w, "! needs you: %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Pull requests")
	if len(t.PRs) == 0 {
		fmt.Fprintln(w, "  none yet")
	}
	for _, pr := range t.PRs {
		fmt.Fprintf(w, "  %s#%d  %s%s  %s\n", pr.Repo, pr.Number, prStateText(pr), reviewText(pr), pr.URL)
	}
}

func section(w io.Writer, title, body, empty string) {
	fmt.Fprintf(w, "\n%s\n", title)
	if body == "" {
		body = empty
	}
	for _, l := range strings.Split(body, "\n") {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

func prStateText(pr ticket.PullRequest) string {
	switch {
	case pr.State == ticket.PRMerged:
		return "merged"
	case pr.State == ticket.PRClosed:
		return "closed"
	case pr.Checks == ticket.ChecksFail:
		return "✕ CI failing"
	case pr.Checks == ticket.ChecksPending:
		return "○ CI running"
	case pr.Checks == ticket.ChecksPass:
		return "✓ checks pass"
	}
	return "open"
}

func reviewText(pr ticket.PullRequest) string {
	switch pr.Review {
	case ticket.ReviewChangesRequested:
		return " · changes requested"
	case ticket.ReviewApproved:
		return " · approved"
	case ticket.ReviewRequired:
		return " · review required"
	}
	return ""
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
