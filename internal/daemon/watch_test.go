package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
)

func watchServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	stateFile, configFile := filepath.Join(dir, "board-state.json"), filepath.Join(dir, "config.toml")
	s, err := New(Options{Dir: dir, Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour,
		StateFile: stateFile, ConfigFile: configFile, WatchEvery: 5 * time.Millisecond,
		SavedState: func() board.SavedState {
			var st board.SavedState
			b, _ := os.ReadFile(stateFile)
			_ = json.Unmarshal(b, &st)
			return st
		},
		SaveState: func(board.SavedState) error { return nil },
		Settings:  func() (config.Config, error) { return config.Config{RepoRoots: []string{"~/src"}}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, stateFile, configFile
}

// writeAtomic writes as the TUI and the daemon do, through a rename, so a check never reads half a file.
func writeAtomic(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func watchedEvent(t *testing.T, cl *client, want string) map[string]any {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg := <-cl.out:
			var ev map[string]any
			if err := json.Unmarshal(msg, &ev); err != nil {
				t.Fatal(err)
			}
			if ev["type"] == want {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", want)
		}
	}
}

func TestTheDaemonAnnouncesFilesAnotherProgramChanges(t *testing.T) {
	s, stateFile, configFile := watchServer(t)
	cl := s.join()
	stop := s.watch()
	defer stop()
	if err := writeAtomic(stateFile, `{"team":true}`); err != nil {
		t.Fatal(err)
	}
	ev := watchedEvent(t, cl, "state.updated")
	if data, _ := ev["data"].(map[string]any); data["team"] != true {
		t.Errorf("state.updated = %v, want the board's view with team on", ev)
	}
	if err := writeAtomic(configFile, "repo_roots = [\"~/src\"]\n"); err != nil {
		t.Fatal(err)
	}
	if ev := watchedEvent(t, cl, "settings.updated"); ev["data"] == nil {
		t.Errorf("settings.updated = %v, want the settings", ev)
	}
}

func TestTheDaemonDoesNotAnnounceAFileThatDidNotChange(t *testing.T) {
	s, stateFile, _ := watchServer(t)
	if err := writeAtomic(stateFile, `{"team":true}`); err != nil {
		t.Fatal(err)
	}
	cl := s.join()
	stop := s.watch()
	time.Sleep(50 * time.Millisecond)
	if err := writeAtomic(stateFile, `{"team":true}`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	stop()
	for len(cl.out) > 0 {
		var ev map[string]any
		_ = json.Unmarshal(<-cl.out, &ev)
		if ev["type"] == "state.updated" {
			t.Errorf("announced a file whose content did not change: %v", ev)
		}
	}
}
