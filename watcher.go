package basecoat

import (
	"io/fs"
	"path/filepath"
	"sync"
	"time"
)

// watchSource wraps a filesystem root with a map of last-known
// modification times. It detects changes by recursively polling the
// tree under root and comparing mtimes of every file and directory it
// sees. The recursion is required because on Linux (and other
// platforms) modifying a file inside a subdirectory does NOT update the
// parent directory's mtime — a shallow ReadDir(root) would miss every
// change below the top level, which is precisely where basecoat/css/
// and basecoat/js/ live.
type watchSource struct {
	root string
	mods map[string]time.Time
	mu   sync.Mutex
}

func newWatchSource(root string) *watchSource {
	return &watchSource{root: root, mods: make(map[string]time.Time)}
}

// changed returns true if any file or directory under root has a new
// modification time since the last call (or has appeared/disappeared).
// It updates its internal map on each call so repeated checks are
// idempotent. A walk error (e.g. root removed) reports as no change so
// the watcher doesn't fire on a source being temporarily unavailable.
func (w *watchSource) changed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	seen := make(map[string]bool, len(w.mods))
	var changed bool

	_ = filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if err == fs.SkipDir {
				return err
			}
			// Best-effort: a transient walk error (e.g. a file
			// deleted between ReadDir and Stat) should not abort
			// the whole sweep. Return nil so we keep walking the
			// rest of the tree.
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(w.root, p)
		if err != nil {
			rel = p
		}
		mod := info.ModTime()
		seen[rel] = true
		if prev, ok := w.mods[rel]; !ok || !prev.Equal(mod) {
			w.mods[rel] = mod
			changed = true
		}
		return nil
	})

	// Detect files/dirs that disappeared from the tree since the
	// last sweep. They are removed from the map so it does not grow
	// without bound, and their disappearance counts as a change.
	for name := range w.mods {
		if !seen[name] {
			delete(w.mods, name)
			changed = true
		}
	}

	return changed
}

// pollWatcher runs a goroutine that checks watchSource entries every
// interval (2s) and calls onChange when any of them has changed.
type pollWatcher struct {
	ws       []*watchSource
	interval time.Duration
	onChange func()
	done     chan struct{}
	once     sync.Once
}

func startPollWatcher(sources []*watchSource, onChange func()) *pollWatcher {
	pw := &pollWatcher{
		ws:       sources,
		interval: 2 * time.Second,
		onChange: onChange,
		done:     make(chan struct{}),
	}
	go pw.loop()
	return pw
}

func (pw *pollWatcher) loop() {
	ticker := time.NewTicker(pw.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, w := range pw.ws {
				if w.changed() {
					pw.onChange()
				}
			}
		case <-pw.done:
			return
		}
	}
}

// stop signals the polling goroutine to exit. Safe to call multiple times.
func (pw *pollWatcher) stop() {
	pw.once.Do(func() {
		close(pw.done)
	})
}
