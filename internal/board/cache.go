package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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
func (c Cache) Load() (snap Snapshot, ok bool, err error) {
	b, err := os.ReadFile(c.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("read cache: %w", err)
	}
	if err := json.Unmarshal(b, &snap); err != nil {
		return Snapshot{}, false, fmt.Errorf("cache %s is corrupt (delete it to rebuild): %w", c.Path, err)
	}
	return snap, true, nil
}

func (c Cache) Save(s Snapshot) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return paths.WritePrivateFile(c.Path, b)
}
