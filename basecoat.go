// Package basecoat provides a virtual filesystem that combines downloaded
// Basecoat + Tailwind CSS with user-provided component directories. It
// produces a single minified, tree-shaken basecoat.css and basecoat.js,
// and automatically regenerates them when source files change.
package basecoat

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

//go:embed basecoatui/v0.3.11/basecoat.js
var basecoatUI_v0311 []byte

// basecoatUIEmbeds maps resolved version strings to embedded JS binaries.
// The JS provides the basecoat runtime (component registry, MutationObserver,
// init system) and is prepended to every generated basecoat.js.
var basecoatUIEmbeds = map[string][]byte{
	"0.3.11": basecoatUI_v0311,
}

// Package-level configuration — set these before calling Init.
var (
	// BasecoatVersion is a semver constraint such as "^0.3.11".
	// When set, Init downloads and caches the matching version of
	// basecoat CSS and its corresponding Tailwind CSS release.
	// Leave empty to skip all downloads and serve only local assets.
	BasecoatVersion string

	// Static disables the 2-second poll watcher. Generation runs once
	// during Init and never again. Use in production.
	Static bool

	// AutoUpdate checks unpkg for a newer basecoat version during Init.
	// If a newer version exists, Init wraps ErrUpdateAvailable in its
	// returned error. The UnionFS is still fully usable in this case.
	AutoUpdate bool
)

// ErrUpdateAvailable is returned (wrapped in Init's error) when a newer
// basecoat package exists on unpkg. Only checked when AutoUpdate is true.
var ErrUpdateAvailable = errors.New("basecoat: update available")

// watchable maps fs.FS values returned by Dir() back to their
// filesystem root paths, so Init can set up polling on them.
var watchable sync.Map

// Dir wraps root in an io/fs.FS and registers it with the poll-based
// watcher. Use Dir when you want Init to auto-detect file changes in
// a directory and regenerate basecoat.css / basecoat.js.
func Dir(root string) fs.FS {
	f := os.DirFS(root)
	watchable.Store(f, root)
	return f
}

// Init creates the union filesystem, downloads and caches remote assets
// (if BasecoatVersion is set), generates the initial basecoat.css and
// basecoat.js, and starts the poll watcher (unless Static is true).
//
// cacheDir is the local directory where downloaded CSS files are stored.
// sources is a list of fs.FS values — use basecoat.Dir() for any that
// should trigger regeneration on file changes.
func Init(cacheDir string, sources ...fs.FS) (*UnionFS, error) {
	var srcs []sourceFS
	for _, s := range sources {
		sf := sourceFS{fs: s}
		if root, ok := watchable.Load(s); ok {
			sf.root = root.(string)
			sf.ws = newWatchSource(sf.root)
		}
		srcs = append(srcs, sf)
	}

	u := &UnionFS{
		sources:    srcs,
		cachePath:  cacheDir,
		static:     Static,
		embeddedJS: basecoatUI_v0311,
	}

	if BasecoatVersion != "" {
		rv, err := resolveVersion(BasecoatVersion)
		if err != nil {
			return nil, err
		}
		u.resolvedVer = rv

		basecoatPath, err := ensureCached(cacheDir, "basecoat", rv.ver, rv.entry.BasecoatURL)
		if err != nil {
			return nil, err
		}
		u.basecoatPath = basecoatPath

		emb, ok := basecoatUIEmbeds[rv.ver]
		if !ok {
			return nil, fmt.Errorf("basecoat: no embedded JS for version %s", rv.ver)
		}
		u.embeddedJS = emb

		if AutoUpdate {
			latest, err := checkLatest()
			if err == nil && isNewerVersion(latest, rv.ver) {
				return nil, fmt.Errorf("%w: basecoat %s is available (using %s)", ErrUpdateAvailable, latest, rv.ver)
			}
		}
	}

	u.regenerate()

	if !Static {
		var watchSources []*watchSource
		for _, src := range srcs {
			if src.ws != nil {
				watchSources = append(watchSources, src.ws)
			}
		}
		if len(watchSources) > 0 {
			u.watcher = startPollWatcher(watchSources, u.regenerate)
		}
	}

	return u, nil
}
