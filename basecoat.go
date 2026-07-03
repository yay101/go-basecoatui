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

// EmbeddedBasecoatJS is the //go:embed'd basecoat runtime, used as a
// last-resort fallback when the CDN download fails and no cache copy
// is available. Init no longer falls back to it silently — it returns
// an error wrapped with ErrJSDownload instead. Callers that want to
// proceed with the embedded runtime can construct a *UnionFS directly,
// passing EmbeddedBasecoatJS as the embeddedJS argument.
//
//go:embed basecoatui/v0.3.11/basecoat.js
var EmbeddedBasecoatJS []byte

// watchable maps fs.FS values returned by Watch() back to their
// filesystem root paths, so Init can set up polling on them.
var watchable sync.Map

// Static disables the 2-second poll watcher. Generation runs once
// during Init and never again. Use in production.
var Static bool

// IncludeTailwindBrowser controls whether Init (parent mode) downloads
// the Tailwind v4 browser build and prepends it to basecoat.js. The
// basecoat CDN styles bundle is compiled with `@import "tailwindcss"
// source(none)`, so it ships the Tailwind v4 preflight + theme +
// basecoat component classes but ZERO utility classes. The browser
// build scans the DOM at runtime and generates the utilities (flex,
// grid, p-4, ...) the page uses for layout.
//
// Defaults to true so a freshly-Init'd parent renders correctly out of
// the box with a single <script src="/basecoat.js"> tag. Set to false
// when your build pipeline compiles CSS locally with
// `@import "tailwindcss"; @import "basecoat-css";` so Tailwind scans
// your source files and emits the utilities at build time — the
// browser build is then redundant and just adds ~270KB of runtime
// work.
//
// Has no effect in child mode (InitChild never downloads it).
var IncludeTailwindBrowser = true

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

	// Unmasked returns an fs.FS view of the same union that does NOT
	// mask the reserved basecoat/ namespace. Use it for
	// template.ParseFS when you want globs like
	// "basecoat/html/*.html" to find fragments the masked UnionFS
	// hides from serving. The view satisfies fs.FS, fs.ReadDirFS,
	// and fs.StatFS. It shares the underlying sources and regenerated
	// basecoat.css / basecoat.js with the parent: Reload, AddSource,
	// and RemoveSource on the parent apply to the view too. The view
	// is read-only — call mutation methods on the parent UnionFS.
	// Callers that mount over HTTP should keep using the masked
	// UnionFS for the file server; the unmasked view is for in-
	// process template parsing only.
	Unmasked() fs.FS

	// Close stops the poll watcher goroutine.
	Close() error
}

// Init creates the union filesystem in parent mode: it downloads
// basecoat's CDN bundle (basecoat.cdn.min.css, pinned to the latest
// 1.x on jsdelivr) into cacheDir (cached on disk after the first run,
// never refreshed), and fetches the latest basecoat.js runtime from
// jsdelivr on every Init. basecoat.css = styles + user
// basecoat/css/**/*.css. basecoat.js = runtime + user
// basecoat/js/**/*.js. Starts a poll watcher for sources passed via
// Watch() unless Static is true.
//
// When IncludeTailwindBrowser is true (the default), Init also
// downloads the Tailwind v4 browser build and prepends it to
// basecoat.js. The basecoat CDN styles bundle ships the Tailwind v4
// preflight + theme + basecoat component classes but ZERO utility
// classes (it is compiled with `@import "tailwindcss" source(none)`),
// so the browser build is what generates the layout utilities (flex,
// grid, p-4, ...) at runtime by scanning the DOM. Set
// IncludeTailwindBrowser = false when your build pipeline compiles
// CSS locally with `@import "tailwindcss"; @import "basecoat-css";`.
//
// All downloads are bounded by a 30s timeout so a stalled CDN
// connection surfaces as an error instead of hanging forever. A
// styles download failure (with no cache) is a hard error. A JS
// download failure is a hard error too — but if a previous cache copy
// exists it is reused silently (the runtime is just a version behind).
// A tailwind browser download failure is a SOFT error: Init proceeds
// without it, so a CDN outage on the tailwind endpoint does not take
// the server down (the page loses Tailwind utilities but basecoat
// components still render). Callers that want to proceed with the
// embedded runtime fallback when no cache exists can construct a
// *UnionFS directly.
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
		// No cached runtime and the CDN is unreachable. Surface the
		// error rather than silently producing a bundle built on a
		// stale embedded fallback. Callers that explicitly want the
		// embedded fallback can build a *UnionFS themselves.
		return nil, err
	}

	// The Tailwind browser build is optional — a CDN failure here
	// degrades gracefully (no utilities) rather than aborting Init.
	var twPath string
	var twBytes []byte
	if IncludeTailwindBrowser {
		twPath, twBytes, err = ensureTailwindBrowser(cacheDir)
		if err != nil {
			// Soft failure: log nothing, just proceed without the
			// browser build. The page will lack Tailwind utilities
			// but basecoat components still work.
			twPath, twBytes = "", nil
		}
	}

	u := newUnionFS(sources, stylesPath, jsPath, jsBytes, twPath, twBytes, cacheDir)
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
	u := newUnionFS(sources, "", "", nil, "", nil, "")
	u.Reload()

	if !Static {
		u.startWatcher()
	}
	return u, nil
}
