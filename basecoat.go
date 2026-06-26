// Package basecoat provides a virtual filesystem that combines downloaded
// Basecoat + Tailwind CSS with user-provided component directories. It
// produces a single minified, tree-shaken basecoat.css and basecoat.js,
// and automatically regenerates them when source files change.
package basecoat

import (
	_ "embed"
	"errors"
	"fmt"
	"html/template"
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

// FS is the union filesystem returned by Init. It satisfies the standard
// io/fs interfaces (Open, ReadDir, Stat) plus the basecoat-specific
// operations: regenerate the virtual CSS/JS, hot-swap sources, and parse
// html/template files out of the source tree.
//
// The reserved namespace is anything matching "basecoat" or "basecoat/..."
// (case-sensitive). UnionFS masks user content at those paths so the
// virtual basecoat.css and basecoat.js are the only entries with a
// "basecoat" prefix. Callers that mount this FS over HTTP should add a
// /basecoat/ → 404 rule to make the reservation explicit at the routing
// layer.
type FS interface {
	fs.FS
	fs.ReadDirFS
	fs.StatFS

	// Reload rebuilds basecoat.css and basecoat.js from the current
	// set of sources. See *UnionFS.Reload for the concurrency contract.
	Reload()

	// AddSource registers src under name, replacing any existing
	// source with the same name. Does not auto-reload.
	AddSource(name string, src fs.FS)

	// RemoveSource drops the source with the given name. Returns
	// false if no such source was registered.
	RemoveSource(name string) bool

	// Template parses match as html/template files out of the union
	// FS, with all *.html files under any source's "basecoat/html/"
	// tree (recursive) parsed first as fragments. The result is cached
	// and reused until the next Reload.
	Template(match ...string) (*template.Template, error)

	// TemplateFuncs is like Template but registers funcs on the
	// template before parsing, so fragments and pages alike can call
	// those functions.
	TemplateFuncs(funcs template.FuncMap, match ...string) (*template.Template, error)

	// Close stops the poll watcher goroutine.
	Close() error
}

// Init creates the union filesystem, downloads and caches remote assets
// (if BasecoatVersion is set), generates the initial basecoat.css and
// basecoat.js, and starts the poll watcher (unless Static is true).
//
// cacheDir is the local directory where downloaded CSS files are stored.
// sources is a list of fs.FS values — use basecoat.Dir() for any that
// should trigger regeneration on file changes.
func Init(cacheDir string, sources ...fs.FS) (FS, error) {
	srcs := make([]sourceFS, 0, len(sources))
	srcIdx := make(map[string]sourceRef, len(sources))
	for i, s := range sources {
		name := fmt.Sprintf("init-%d", i)
		sf := sourceFS{name: name, fs: s}
		if root, ok := watchableRoot(s); ok {
			sf.root = root
			sf.ws = newWatchSource(sf.root)
		}
		srcs = append(srcs, sf)
		srcIdx[name] = sourceRef{index: len(srcs) - 1}
	}

	u := &UnionFS{
		sources:    srcs,
		sourceIdx:  srcIdx,
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

	u.Reload()

	if !Static {
		var watchSources []*watchSource
		for _, src := range srcs {
			if src.ws != nil {
				watchSources = append(watchSources, src.ws)
			}
		}
		if len(watchSources) > 0 {
			u.watcher = startPollWatcher(watchSources, u.Reload)
		}
	}

	return u, nil
}
