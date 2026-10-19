package cli

import (
	"context"
	"errors"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
)

func (a *App) cache() board.Cache {
	return board.NewCache(a.Env.DataDir())
}

// loadSnapshot fetches a fresh board and caches it. When fetching fails and a cached board exists, it
// returns the cached one with the failure as stale reason, so an outage or an expired token never
// leaves the user without a board. cachedOnly skips the network.
func (a *App) loadSnapshot(ctx context.Context, c *config.Config, cachedOnly bool) (snap board.Snapshot, stale error, err error) {
	cache := a.cache()
	if cachedOnly {
		snap, ok, err := cache.Load()
		if err != nil {
			return board.Snapshot{}, nil, err
		}
		if !ok {
			return board.Snapshot{}, nil, errors.New("no cached board yet: run `tkt list` once while online")
		}
		return snap, nil, nil
	}
	snap, fetchErr := a.fetchSnapshot(ctx, c)
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
	return cached, fetchErr, nil
}

func (a *App) fetchSnapshot(ctx context.Context, c *config.Config) (board.Snapshot, error) {
	js, err := a.jiraClient(c)
	if err != nil {
		return board.Snapshot{}, err
	}
	return board.Fetch(ctx, c, js, a.NewGitHub(c), a.Now())
}
