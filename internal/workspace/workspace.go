// Package workspace tracks where work on each ticket happens: worktrees the agent registered with
// `tkt link`, and branches discovered in local clones. tkt never creates branches or worktrees itself.
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/execx"
	"github.com/pavp/tkt/internal/paths"
	"github.com/pavp/tkt/internal/ticket"
)

type Source string

const (
	SourceAgent      Source = "linked by agent"
	SourceHook       Source = "linked by Herdr hook"
	SourceDiscovered Source = "discovered"
)

// Work is one branch of one repository that carries a ticket key, with its worktree when it has one.
type Work struct {
	Key      ticket.Key `json:"key"`
	Repo     string     `json:"repo"`
	Branch   string     `json:"branch"`
	Path     string     `json:"path,omitempty"`
	Source   Source     `json:"source"`
	LinkedAt time.Time  `json:"linked_at,omitempty"`
}

// Links persists the work agents register, in the data directory.
type Links struct {
	Path string
}

func NewLinks(dataDir string) Links {
	return Links{Path: filepath.Join(dataDir, "links.json")}
}

func (l Links) Load() ([]Work, error) {
	b, err := os.ReadFile(l.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read links: %w", err)
	}
	var out []Work
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("links file %s is corrupt: %w", l.Path, err)
	}
	return out, nil
}

// Add records w, replacing an earlier link for the same repository and branch.
func (l Links) Add(w Work) error {
	all, err := l.Load()
	if err != nil {
		return err
	}
	kept := all[:0]
	for _, x := range all {
		if x.Repo != w.Repo || x.Branch != w.Branch {
			kept = append(kept, x)
		}
	}
	kept = append(kept, w)
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Key != kept[j].Key {
			return kept[i].Key < kept[j].Key
		}
		return kept[i].Repo < kept[j].Repo
	})
	b, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	return paths.WritePrivateFile(l.Path, b)
}

// Clone is a local checkout of a configured repository.
type Clone struct {
	Repo string
	Dir  string
}

// Discover lists branches carrying a key of spaces in each clone: worktrees first (they have a path),
// then local and origin branches without a worktree.
func Discover(ctx context.Context, run execx.Runner, clones []Clone, spaces []string) ([]Work, error) {
	var out []Work
	for _, c := range clones {
		seen := map[string]bool{}
		wt, err := run.Run(ctx, c.Dir, "git", []string{"worktree", "list", "--porcelain"}, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Repo, err)
		}
		for _, e := range parseWorktrees(string(wt)) {
			if k, ok := ticket.KeyInBranch(e.branch, spaces); ok {
				seen[e.branch] = true
				out = append(out, Work{Key: k, Repo: c.Repo, Branch: e.branch, Path: e.path, Source: SourceDiscovered})
			}
		}
		refs, err := run.Run(ctx, c.Dir, "git", []string{"for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes/origin"}, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Repo, err)
		}
		for _, ref := range strings.Split(strings.TrimSpace(string(refs)), "\n") {
			branch := strings.TrimPrefix(ref, "origin/")
			if branch == "" || branch == "HEAD" || seen[branch] {
				continue
			}
			if k, ok := ticket.KeyInBranch(branch, spaces); ok {
				seen[branch] = true
				out = append(out, Work{Key: k, Repo: c.Repo, Branch: branch, Source: SourceDiscovered})
			}
		}
	}
	return out, nil
}

type worktreeEntry struct {
	path, branch string
}

func parseWorktrees(porcelain string) []worktreeEntry {
	var out []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.path != "" && cur.branch != "" {
			out = append(out, cur)
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(porcelain, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "":
			flush()
		}
	}
	flush()
	return out
}

// Merge combines registered links with discovered work. A link wins for its repository and branch, but
// a discovered worktree path fills in a link that has none, and a link to a worktree now on another branch
// is dropped.
func Merge(links, discovered []Work) []Work {
	type id struct{ repo, branch string }
	byID := map[id]Work{}
	var order []id
	add := func(w Work) {
		k := id{w.Repo, w.Branch}
		if _, ok := byID[k]; !ok {
			order = append(order, k)
		}
		byID[k] = w
	}
	checkedOut := map[string]string{}
	for _, w := range discovered {
		add(w)
		if w.Path != "" {
			checkedOut[filepath.Clean(w.Path)] = w.Branch
		}
	}
	for _, w := range links {
		// A link to a worktree that git now shows on another branch is stale: the agent switched branches there.
		if b, ok := checkedOut[filepath.Clean(w.Path)]; ok && w.Path != "" && b != w.Branch {
			continue
		}
		if d, ok := byID[id{w.Repo, w.Branch}]; ok && w.Path == "" {
			w.Path = d.Path
		}
		add(w)
	}
	out := make([]Work, 0, len(order))
	for _, k := range order {
		out = append(out, byID[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// For returns the work on key.
func For(all []Work, key ticket.Key) []Work {
	var out []Work
	for _, w := range all {
		if w.Key == key {
			out = append(out, w)
		}
	}
	return out
}

// StartPrompt is the first message the agent receives when a ticket starts in repoDir.
func StartPrompt(key ticket.Key, summary string) string {
	return fmt.Sprintf(`Work on Jira ticket %[1]s: %[2]s

1. Create a branch and a git worktree for this ticket, following this repository's own conventions for branch names, base branch and tooling. The branch name must contain %[1]s.
2. In the new worktree, save the ticket context and read it: tkt context %[1]s --images .ticket-assets > .ticket.md
   Keep .ticket.md and .ticket-assets/ out of git by adding them to .git/info/exclude.
3. Register the work from inside the worktree: tkt link %[1]s
4. Then implement the ticket.`, key, summary)
}

// HerdrStartPrompt is StartPrompt for an agent inside Herdr, which only shows worktrees it created as workspaces.
func HerdrStartPrompt(key ticket.Key, summary string) string {
	return StartPrompt(key, summary) + "\n\nYou are running inside Herdr: create the worktree with `herdr worktree create --cwd <this repository> --branch <branch> [--base <base>]` instead of `git worktree add`, so Herdr opens it as its own workspace."
}

// HerdrBranchPrompt is the first message for an agent that opens in a free checkout (no other agent, no local
// changes, on its default branch): it works there on a new branch instead of a worktree.
func HerdrBranchPrompt(key ticket.Key, summary string) string {
	return fmt.Sprintf(`Work on Jira ticket %[1]s: %[2]s

This checkout is free (no other agent, no local changes), so work in it and do not create a worktree.
1. Create a branch for this ticket in this checkout, following this repository's own conventions for branch names, base branch and tooling. The branch name must contain %[1]s.
2. Save the ticket context and read it: tkt context %[1]s --images .ticket-assets > .ticket.md
   Keep .ticket.md and .ticket-assets/ out of git by adding them to .git/info/exclude.
3. Register the work: tkt link %[1]s
4. Then implement the ticket.`, key, summary)
}

// ContinuePrompt is the first message when the agent opens in an existing worktree.
func ContinuePrompt(key ticket.Key, summary string) string {
	return fmt.Sprintf("Continue work on Jira ticket %s: %s. Refresh the context with `tkt context %s --images .ticket-assets > .ticket.md` and read .ticket.md first.", key, summary, key)
}
