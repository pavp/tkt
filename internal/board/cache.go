package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pavp/tkt/internal/paths"
)

// Cache keeps the last snapshot on disk so the board can show something instantly and survive outages.
type Cache struct {
	Path string
}

func NewCache(dataDir string) Cache {
	return Cache{Path: filepath.Join(dataDir, "board.json")}
}

// Load returns the cached snapshot; ok is false when there is none yet.
func (c Cache) Load() (snap Snapshot, ok bool, err error) { return loadJSON[Snapshot](c.Path) }

func (c Cache) Save(s Snapshot) error { return saveJSON(c.Path, s) }

// BacklogEvery is how often the daemon refreshes the backlog, and how old a copy the board opens without asking
// Jira: a backlog changes far less than a sprint, and each refresh costs a few calls per team board.
const BacklogEvery = 5 * time.Minute

// BacklogCache keeps the last backlog on disk, which the daemon refreshes and the board opens from.
type BacklogCache struct {
	Path string
}

func NewBacklogCache(dataDir string) BacklogCache {
	return BacklogCache{Path: filepath.Join(dataDir, "backlog.json")}
}

// Load returns the cached backlog; ok is false when there is none yet.
func (c BacklogCache) Load() (Backlog, bool, error) { return loadJSON[Backlog](c.Path) }

func (c BacklogCache) Save(b Backlog) error { return saveJSON(c.Path, b) }

func loadJSON[T any](path string) (v T, ok bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return v, false, nil
	}
	if err != nil {
		return v, false, fmt.Errorf("read cache: %w", err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, false, fmt.Errorf("cache %s is corrupt (delete it to rebuild): %w", path, err)
	}
	return v, true, nil
}

func saveJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return paths.WritePrivateFile(path, b)
}
