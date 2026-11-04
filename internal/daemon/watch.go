package daemon

import (
	"crypto/sha256"
	"os"
	"sync"
	"time"
)

type watchedFile struct {
	event, path string
}

func (s *Server) watchedFiles() []watchedFile {
	var files []watchedFile
	if s.opts.StateFile != "" && s.opts.SavedState != nil {
		files = append(files, watchedFile{"state.updated", s.opts.StateFile})
	}
	if s.opts.ConfigFile != "" && s.opts.Settings != nil {
		files = append(files, watchedFile{"settings.updated", s.opts.ConfigFile})
	}
	return files
}

// watch announces the watched files whose content changes until the returned func is called.
func (s *Server) watch() (stop func()) {
	files := s.watchedFiles()
	if s.opts.WatchEvery <= 0 || len(files) == 0 {
		return func() {}
	}
	for _, f := range files {
		s.noteFile(f.path)
	}
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		tick := time.NewTicker(s.opts.WatchEvery)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
			}
			for _, f := range files {
				if s.noteFile(f.path) {
					s.announce(f.event)
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); <-finished }) }
}

// noteFile records the file's content and reports whether it differs from what was recorded before; the
// daemon notes a file it writes itself, so its own change is announced once.
func (s *Server) noteFile(path string) bool {
	if path == "" {
		return false
	}
	b, _ := os.ReadFile(path)
	sum := sha256.Sum256(b)
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.sums == nil {
		s.sums = map[string][32]byte{}
	}
	old, had := s.sums[path]
	s.sums[path] = sum
	return had && old != sum
}

func (s *Server) announce(event string) {
	var data any
	switch event {
	case "state.updated":
		data = viewOf(s.opts.SavedState())
	case "settings.updated":
		c, err := s.opts.Settings()
		if err != nil {
			return
		}
		data = s.settingsView(c)
	}
	s.mu.Lock()
	s.publish(event, data)
	s.mu.Unlock()
}
