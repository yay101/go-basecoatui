package basecoat

import (
	"os"
	"sync"
	"time"
)

// watchSource wraps a filesystem root with a map of last-known
// modification times. It detects changes by polling ReadDir.
type watchSource struct {
	root string
	mods map[string]time.Time
	mu   sync.Mutex
}

func newWatchSource(root string) *watchSource {
	return &watchSource{root: root, mods: make(map[string]time.Time)}
}

// changed returns true if any file in the directory has a new
// modification time since the last call. It updates its internal map
// on each call so repeated checks are idempotent.
func (w *watchSource) changed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	entries, err := os.ReadDir(w.root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := info.Name()
		mod := info.ModTime()
		if prev, ok := w.mods[p]; !ok || !prev.Equal(mod) {
			w.mods[p] = mod
			return true
		}
	}
	return false
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
