package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/herdrcore"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/workspace"
)

// unattended is a Start no terminal answers, as the daemon runs it: every question it would ask fails with
// workspace.NeedsAnswer instead, it opens agents in the Herdr the caller named, and settled runs once it asks nothing
// more.
type unattended struct {
	flavor  herdrFlavor
	herdr   HerdrAPI
	settled func()
}

type unattendedKey struct{}

func unattendedOf(ctx context.Context) (unattended, bool) {
	u, ok := ctx.Value(unattendedKey{}).(unattended)
	return u, ok
}

// needAnswer fails a question an unattended Start would have to ask.
func needAnswer(ctx context.Context, need string) error {
	if _, ok := unattendedOf(ctx); ok {
		return workspace.NeedsAnswer{Need: need}
	}
	return nil
}

func settle(ctx context.Context) {
	if u, ok := unattendedOf(ctx); ok && u.settled != nil {
		u.settled()
	}
}

// startHerdr is the Herdr a Start opens agents in.
func (a *App) startHerdr(ctx context.Context) HerdrAPI {
	if u, ok := unattendedOf(ctx); ok {
		return u.herdr
	}
	return a.NewHerdr()
}

// unattendedIn makes ctx an unattended Start in the Herdr named name ("desktop" or "classic"), which must be running:
// the daemon runs detached, so nothing else tells which Herdr to use when both run.
func (a *App) unattendedIn(ctx context.Context, name string, settled func()) (context.Context, error) {
	flavor := herdrClassic
	if name == "desktop" {
		flavor = herdrDesktop
	}
	set := a.daemonHerdrs()
	for _, look := range []func(context.Context) []daemonHerdr{set.current, set.recheck} {
		for _, h := range look(ctx) {
			if h.flavor == flavor {
				return context.WithValue(ctx, unattendedKey{}, unattended{flavor: flavor, herdr: h.api, settled: settled}), nil
			}
		}
	}
	if flavor == herdrDesktop && a.HerdrDesktopFound != nil {
		if err := a.HerdrDesktopFound(); errors.Is(err, herdrcore.ErrNotInstalled) {
			return ctx, fmt.Errorf("%w: %w", daemon.ErrHerdrUnavailable, err)
		}
	}
	return ctx, fmt.Errorf("%w: %s is not running", daemon.ErrHerdrUnavailable, flavor)
}

// daemonStartChoices serves GET /v1/tickets/{key}/start: what the board's Start modal asks, with the agents it offers.
func (a *App) daemonStartChoices(ctx context.Context, key ticket.Key, herdr string) (daemon.StartChoices, error) {
	ctx, err := a.unattendedIn(ctx, herdr, nil)
	if err != nil {
		return daemon.StartChoices{}, err
	}
	c, err := a.LoadConfig()
	if err != nil {
		return daemon.StartChoices{}, err
	}
	ch, err := a.startChoices(ctx, c, key, a.startHerdr(ctx))
	if err != nil {
		return daemon.StartChoices{}, err
	}
	out := daemon.StartChoices{Opens: ch.Opens}
	for _, w := range ch.Worktrees {
		out.Worktrees = append(out.Worktrees, daemon.StartWorktree{Repo: w.Repo, Branch: w.Branch, Path: w.Path, Agent: w.Agent})
	}
	for _, r := range ch.Repos {
		out.Repos = append(out.Repos, daemon.StartRepo{Name: r.Name, LastUsed: r.LastUsed, Checked: r.Checked})
	}
	kinds, last, err := a.agentChoices(ctx)
	out.Agents, out.LastAgent = kinds, last
	if err != nil {
		out.AgentsError = err.Error()
	}
	return out, nil
}

// daemonStart serves POST /v1/tickets/{key}/start with the Start the board runs, its answers given up front.
func (a *App) daemonStart(ctx context.Context, key ticket.Key, req daemon.StartRequest, settled func()) error {
	ctx, err := a.unattendedIn(ctx, req.Herdr, settled)
	if err != nil {
		return err
	}
	err = a.startInHerdr(ctx, key, workspace.StartOptions{Repos: req.Repos, Worktree: req.Worktree, Agent: req.Agent, Clone: req.Clone})
	if need := (workspace.NeedsAnswer{}); errors.As(err, &need) {
		return daemon.NeedsAnswerError{Need: need.Need}
	}
	return err
}
