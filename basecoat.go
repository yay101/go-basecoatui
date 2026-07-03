// Package basecoat provides a virtual filesystem that combines downloaded
// basecoat + tailwind CSS with user-provided component directories. It
// produces a single minified basecoat.css and basecoat.js, and
// automatically regenerates them when source files change.
//
// Two init entry points, each producing a UnionFS with the same two
// virtual files (basecoat.css, basecoat.js):
//
//   - Init(cacheDir, sources...) is parent mode. It downloads the
//     basecoat CDN bundle (basecoat.cdn.min.css, pinned to the latest
//     1.x) from jsdelivr into cacheDir, and fetches the latest
//     basecoat.js runtime from jsdelivr on every Init. basecoat.css =
//     styles + user basecoat/css/**/*.css. basecoat.js = runtime +
//     user basecoat/js/**/*.js.
//
//   - InitChild(sources...) is child mode. No network, no cache.
//     basecoat.css = user basecoat/css/**/*.css only. basecoat.js =
//     user basecoat/js/**/*.js only (no embedded runtime — the parent
//     has already loaded it; the child's JS calls basecoat.register()
//     to add its components to the global registry on page load).
//
// Callers render templates themselves via template.ParseFS(ufs, globs...).
package basecoat

import (
	_ "embed"
	"io/fs"
	"os"
	"sync"
)

//go:embed basecoatui/v0.3.11/basecoat.js
var embeddedBasecoatJS []byte

// watchable maps fs.FS values returned by Watch() back to their
// filesystem root paths, so Init can set up polling on them.
var watchable sync.Map

// Static disables the 2-second poll watcher. Generation runs once
// during Init and never again. Use in production.
var Static bool

// Watch wraps root in an io/fs.FS and registers it with the poll-based
// watcher. Use Watch when you want Init to auto-detect file changes in
// a directory and regenerate basecoat.css / basecoat.js.
func Watch(root string) fs.FS {
	f := os.DirFS(root)
	watchable.Store(f, root)
	return f
}

// FS is the union filesystem returned by Init. It satisfies the standard
// io/fs interfaces (Open, ReadDir, Stat) plus the basecoat-specific
// operations: regenerate the virtual CSS/JS and hot-swap sources.
//
// The reserved namespace is anything matching "basecoat" or "basecoat/..."
// (case-sensitive). UnionFS masks user content at those paths so the
// virtual basecoat.css and basecoat.js are the only entries with a
// "basecoat" prefix. Callers that mount this FS over HTTP should add a
// /basecoat/ -> 404 rule to make the reservation explicit at the
// routing layer.
//
// Callers do their own template parsing via template.ParseFS(ufs, globs...).
// The library no longer owns html/template: it's just an fs.FS, and
// anything not masked by the basecoat/ namespace is visible to glob.
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

	// Close stops the poll watcher goroutine.
	Close() error
}

// Init creates the union filesystem in parent mode: it downloads
// basecoat's CDN bundle (basecoat.cdn.min.css, pinned to the latest
// 1.x on jsdelivr) into cacheDir (cached on disk after the first run,
// never refreshed), and fetches the latest basecoat.js runtime from
// jsdelivr on every Init (the embedded //go:embed byte slice is used
// as a fallback when the network is down). basecoat.css = styles +
// user basecoat/css/**/*.css. basecoat.js = runtime + user
// basecoat/js/**/*.js. Starts a poll watcher for sources passed via
// Watch() unless Static is true.
//
// cacheDir is the local directory where downloaded assets are stored.
// sources is a list of fs.FS values — use basecoat.Watch() for any that
// should trigger regeneration on file changes.
func Init(cacheDir string, sources ...fs.FS) (FS, error) {
	stylesPath, err := ensureBasecoatStyles(cacheDir)
	if err != nil {
		return nil, err
	}

	jsPath, jsBytes, err := ensureBasecoatJS(cacheDir)
	if err != nil {
		// Network failure: fall back to the embedded runtime so the
		// library still produces a working basecoat.js. The cache
		// is not touched.
		jsPath = ""
		jsBytes = embeddedBasecoatJS
	}

	u := newUnionFS(sources, stylesPath, jsPath, jsBytes, cacheDir)
	u.Reload()

	if !Static {
		u.startWatcher()
	}
	return u, nil
}

// InitChild creates the union filesystem in child mode: no network, no
// cache. basecoat.css = user basecoat/css/**/*.css only.
// basecoat.js = user basecoat/js/**/*.js only (no embedded runtime —
// the parent has already loaded it, and the child's JS uses
// basecoat.register() to add its components to the global registry on
// page load). Starts a poll watcher for sources passed via Watch()
// unless Static is true.
func InitChild(sources ...fs.FS) (FS, error) {
	u := newUnionFS(sources, "", "", nil, "")
	u.Reload()

	if !Static {
		u.startWatcher()
	}
	return u, nil
}
