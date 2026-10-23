package cli

import (
	"context"
	"errors"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
)

func (a *App) cache() board.Cache {
	return board.NewCache(a.Env.DataDir())
}

func (a *App) backlogCache() board.BacklogCache {
	return board.NewBacklogCache(a.Env.DataDir())
}

// loadSnapshot fetches a fresh board and caches it. When fetching fails and a cached board exists, it
// returns the cached one with the failure as stale reason, so an outage or an expired token never
// leaves the user without a board. cachedOnly skips the network.
func (a *App) loadSnapshot(ctx context.Context, c *config.Config, cachedOnly bool) (snap board.Snapshot, stale error, err error) {
	return a.loadSnapshotWith(ctx, c, cachedOnly, nil)
}

// loadSnapshotWith is loadSnapshot handing partial, when set, the board as Jira tells it while GitHub is
// still answering; that board is never cached.
func (a *App) loadSnapshotWith(ctx context.Context, c *config.Config, cachedOnly bool, partial func(board.Snapshot)) (snap board.Snapshot, stale error, err error) {
	cache := a.cache()
	if cachedOnly {
		snap, ok, err := cache.Load()
		if err != nil {
			return board.Snapshot{}, nil, err
		}
		if !ok {
			return board.Snapshot{}, nil, errors.New("no cached board yet: run `tkt list` once while online")
		}
		return board.DropIgnored(c, snap), nil, nil
	}
	snap, fetchErr := a.fetchSnapshotWith(ctx, c, partial)
	if fetchErr == nil {
		if err := cache.Save(snap); err != nil {
			snap.Warnings = append(snap.Warnings, "could not update the cache: "+err.Error())
		}
		return snap, nil, nil
	}
	cached, ok, err := cache.Load()
	if err != nil || !ok {
		return board.Snapshot{}, nil, fetchErr
	}
	return board.DropIgnored(c, cached), fetchErr, nil
}

// fetchSnapshot builds on the cached board, so unchanged tickets keep their development panel, and reads
// the token again once when Jira rejects the remembered one, since another command may have saved a new
// token while this one ran.
func (a *App) fetchSnapshot(ctx context.Context, c *config.Config) (board.Snapshot, error) {
	return a.fetchSnapshotWith(ctx, c, nil)
}

func (a *App) fetchSnapshotWith(ctx context.Context, c *config.Config, partial func(board.Snapshot)) (board.Snapshot, error) {
	for retried := false; ; retried = true {
		js, err := a.jiraClient(c)
		if err != nil {
			return board.Snapshot{}, err
		}
		prev, _, _ := a.cache().Load()
		snap, err := board.FetchWith(ctx, c, js, a.NewGitHub(c), a.Now(), prev, board.Options{Team: a.loadBoardState().Team, Partial: partial})
		if !errors.Is(err, jira.ErrUnauthorized) || retried {
			return snap, err
		}
		if site, serr := config.SiteURL(c.Jira.Site); serr == nil {
			a.forgetJiraToken(site, c.Jira.Email)
		}
	}
}
