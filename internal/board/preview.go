package board

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/ticket"
)

// PreviewParts are the preview's details for a selected ticket: points, assignee, why it needs you (or that
// its checks pass), and its open pull requests.
func PreviewParts(t ticket.Ticket) []string {
	parts := []string{t.Assignee}
	if t.Assignee == "" {
		parts[0] = "unassigned"
	}
	switch {
	case t.Points == 1:
		parts = append([]string{"1 point"}, parts...)
	case t.Points != 0:
		parts = append([]string{strconv.FormatFloat(t.Points, 'f', -1, 64) + " points"}, parts...)
	}
	for _, r := range t.Attention() {
		parts = append(parts, string(r))
	}
	if len(t.Attention()) == 0 && t.Checks() == ticket.ChecksPass {
		parts = append(parts, "checks pass")
	}
	for _, pr := range t.PRs {
		if pr.State == ticket.PROpen {
			parts = append(parts, pr.Repo+"#"+fmt.Sprint(pr.Number))
		}
	}
	return parts
}

// ActionHint tells what the most useful key does for this ticket; feedback goes to "the" agent when the
// ticket has one, else to "an" agent.
func ActionHint(t ticket.Ticket, hasAgent bool) string {
	reasons := t.Attention()
	hint := ""
	switch {
	case t.Team:
		return "enter for details"
	case len(reasons) > 0 && reasons[0] == ticket.ReasonCIFailing:
		hint = "f sends the failure to the agent"
	case len(reasons) > 0:
		hint = "f sends the review comments to the agent"
	case len(t.PRs) == 0 && t.Category == ticket.ToDo:
		return "enter asks for repos and opens an agent"
	case len(t.PRs) == 0:
		return "enter opens the agent"
	default:
		return "p opens the PR ↗"
	}
	if !hasAgent {
		hint = strings.Replace(hint, "to the agent", "to an agent", 1)
	}
	return hint
}

// SyncState is how a board's last refresh went, for SyncLabel.
type SyncState struct {
	Loading, Partial, Failed bool
	FetchedAt                time.Time
}

// SyncLabel says how fresh the board is: refreshing, synced N min ago, or cached when the last refresh failed.
func SyncLabel(s SyncState, now time.Time) string {
	switch {
	case s.Loading && s.Partial:
		return "updating PRs…"
	case s.Loading:
		return "refreshing…"
	case s.FetchedAt.IsZero():
		return "not synced yet"
	case s.Failed:
		return "! cached · " + s.FetchedAt.Local().Format("15:04")
	}
	d := now.Sub(s.FetchedAt)
	switch {
	case d < time.Minute:
		return "✓ synced just now"
	case d < time.Hour:
		return fmt.Sprintf("✓ synced %d min ago", int(d.Minutes()))
	}
	return "✓ synced " + s.FetchedAt.Local().Format("15:04")
}

// BacklogLabel says how fresh the Backlog is: refreshing, loaded N min ago, or not loaded yet (a zero time).
func BacklogLabel(loading bool, fetchedAt, now time.Time) string {
	switch {
	case loading:
		return "refreshing backlog…"
	case fetchedAt.IsZero():
		return "not loaded yet"
	}
	d := now.Sub(fetchedAt)
	switch {
	case d < time.Minute:
		return "✓ loaded just now"
	case d < time.Hour:
		return fmt.Sprintf("✓ loaded %d min ago", int(d.Minutes()))
	}
	return "✓ loaded " + fetchedAt.Local().Format("15:04")
}
