# go-basecoatui

Zero-dependency Go module that serves [Basecoat](https://basecoatui.com) — a Tailwind v4 component library — through a virtual `fs.FS`. It downloads the basecoat CSS + JS bundle from jsdelivr, layers in your own component CSS/JS, and exposes a single `basecoat.css` and `basecoat.js` alongside whatever else is in your source directories. A poll watcher regenerates the bundles on file changes.

The library is stdlib-only (`net/http`, `io/fs`, `embed`, `html/template`, `regexp`, ...). No third-party Go imports, no build step, no Node.

## Quick start

```go
package main

import (
	"log"
	"net/http"

	basecoat "github.com/yay101/go-basecoatui"
)

func main() {
	ufs, err := basecoat.Init("./cache",
		basecoat.Watch("./public"),
		basecoat.Watch("./components"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer ufs.Close()

	mux := http.NewServeMux()
	mux.Handle("/basecoat/", http.NotFoundHandler()) // reserved namespace
	mux.Handle("/", http.FileServer(http.FS(ufs)))
	log.Fatal(http.ListenAndServe(":8080", mux))
}
```

Your `public/index.html` just loads the two bundles:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>my app</title>
  <link rel="stylesheet" href="/basecoat.css">
</head>
<body>
  <!-- your markup -->
  <script src="/basecoat.js"></script>
</body>
</html>
```

`basecoat.css` ships the Tailwind v4 preflight + theme + basecoat component classes. `basecoat.js` (parent mode) prepends the Tailwind v4 browser build, which scans the DOM at runtime and generates the utility classes (`flex`, `grid`, `p-4`, ...) your markup uses — so a single `<script>` tag gives you both utilities and the component lifecycle.

## How it works

`Init` (parent mode) downloads three things from jsdelivr on first run and caches them under `{cacheDir}/basecoat/`:

| Asset | URL | Refreshed? |
|---|---|---|
| Styles | `basecoat-css@1/dist/basecoat.cdn.min.css` | Once (cached permanently) |
| Runtime | `basecoat-css/dist/js/all.min.js` | Every `Init` |
| Tailwind browser | `@tailwindcss/browser@4` | Every `Init` |

It then builds two virtual files at the root of the union FS:

- **`basecoat.css`** = downloaded styles + a border-color layer override + every `basecoat/css/**/*.css` across your sources (minified).
- **`basecoat.js`** = Tailwind browser build + downloaded runtime + lifecycle shim + every `basecoat/js/**/*.js` across your sources (minified).

Everything else in your source directories is served verbatim through the union FS. The `basecoat/` namespace is reserved — paths under it are masked from `Open`/`ReadDir`/`Stat` so user files never leak into the `/basecoat*` URL space.

## Source layout

Pass any number of source directories to `Init` (or `--source` to the CLI). The library picks files up by location inside the reserved `basecoat/` subtree:

```
my-project/
├── public/
│   └── index.html              # served at / — any HTML, images, etc.
└── components/
    └── basecoat/               # reserved namespace (masked from serving)
        ├── css/
        │   └── button.css      # merged into basecoat.css
        ├── js/
        │   └── onClick.js      # appended to basecoat.js
        └── html/
            └── card.html       # template fragment (not served — use Unmasked())
```

| Pattern | Effect |
|---|---|
| `basecoat/css/**/*.css` | Concatenated into `basecoat.css` and minified |
| `basecoat/js/**/*.js` | Appended to `basecoat.js` (after the runtime in parent mode) |
| `basecoat/html/**/*.html` | Masked from serving. Parse via `ufs.Unmasked()`. |
| anything else | Served verbatim through the union FS |

## Templates

The library doesn't own `html/template` — you parse templates yourself with `template.ParseFS`. For pages at the union root:

```go
t, err := template.ParseFS(ufs, "*.html")
```

For fragments kept under `basecoat/html/` (so they never appear at a URL), use `ufs.Unmasked()` — a read-only view of the same union that doesn't apply the `basecoat/` mask. `filepath.Match` has no `**` support, so pass two single-component globs:

```go
t, err := template.ParseFS(ufs.Unmasked(), "*.html", "basecoat/html/*.html")
```

One `ParseFS` call with both patterns picks up pages and fragments together, so `{{template "name" .}}` lookups resolve. `Unmasked()` shares the underlying sources and regenerated bundles with the parent `UnionFS` — `Reload`, `AddSource`, and `RemoveSource` on the parent apply to the view too. Use the masked `UnionFS` for HTTP serving; the unmasked view is for in-process template parsing only.

## Component JS

The basecoat runtime provides `window.basecoat.register(name, selector, init)`:

```js
basecoat.register('chat', '#my-chat:not([data-chat-initialized])', function(el) {
  el.addEventListener('submit', function(e) { /* ... */ });
  el.dataset.chatInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
```

After an `innerHTML` swap, re-initialise the new tree:

```js
basecoat.initAll();
```

Later `register()` calls override earlier ones with the same name — that's how users override built-in components.

### Lifecycle (destroy)

In parent mode, a small **lifecycle shim** is appended after the runtime. It wraps `register` to accept an optional `destroy(el)` callback and adds helpers for SPA shells that swap content:

```js
basecoat.register(name, selector, init, destroy?)   // 4th arg optional
basecoat.destroy(el)         // run destroy(el) for every initialised component at/below el
basecoat.destroyAll(root)   // sugar for destroy(root || document.body)
basecoat.unregister(name)   // stop calling destroy for name (init still runs)
```

Before an `innerHTML` swap:

```js
basecoat.destroyAll(main);
main.innerHTML = html;
basecoat.initAll(main);
```

Omit `destroy` for components with no teardown. The shim is idempotent (guarded by `window.basecoat.__lifecycle`), so double-loads are harmless. Child bundles don't append it — the parent page already loaded it.

## Parent vs child (SPA pattern)

`Init` is **parent mode**: downloads everything, serves the full bundle. `InitChild` is **child mode**: no network, no cache — just your user CSS/JS. The child's JS calls `basecoat.register()` to add its components to the global registry that the parent's runtime already set up.

```go
basecoat.Static = true

parent,  _ := basecoat.Init("./cache", basecoat.Watch("./public"))
team,    _ := basecoat.InitChild(basecoat.Watch("./team-svc"))
billing, _ := basecoat.InitChild(basecoat.Watch("./billing-svc"))

mux := http.NewServeMux()
mux.Handle("/basecoat.css",  serveFrom(parent, "basecoat.css"))
mux.Handle("/basecoat.js",   serveFrom(parent, "basecoat.js"))
mux.Handle("/team/basecoat.css",    serveFrom(team, "basecoat.css"))
mux.Handle("/team/basecoat.js",     serveFrom(team, "basecoat.js"))
mux.Handle("/billing/basecoat.css", serveFrom(billing, "basecoat.css"))
mux.Handle("/billing/basecoat.js",  serveFrom(billing, "basecoat.js"))
```

The parent's HTML loads its own bundles (which include the runtime), then loads each child's bundles. Each child is independently reloadable.

## Hot-swap sources

For setups where the source set isn't known at `Init` time (parent processes hosting child modules over a socket, plugin systems, multi-tenant hosts):

```go
ufs, _ := basecoat.Init("./cache")
ufs.AddSource("child-1", childFS1)   // any fs.FS
ufs.AddSource("child-2", childFS2)
ufs.Reload()                          // caller batches — no auto-reload
// later:
ufs.RemoveSource("child-1")
ufs.Reload()
```

`AddSource` does not auto-reload (caller batches). Sources added via `AddSource` are not watched by the poll watcher — the parent triggers `Reload` on external changes. `Reload` is concurrency-safe; `Open` always sees a complete previous or next version, never a half-built one.

## Configuration

Set these before calling `Init`:

| Variable | Default | Description |
|---|---|---|
| `Static` | `false` | Disable the poll watcher. Generation runs once. |
| `IncludeTailwindBrowser` | `true` | Download the Tailwind v4 browser build and prepend it to `basecoat.js` (parent mode). Set to `false` when you compile CSS locally with `@import "tailwindcss"; @import "basecoat-css";` so utilities are emitted at build time and the ~270KB browser build is redundant. |

## Download failures

All CDN downloads use a shared `http.Client` with a 30-second timeout. No retries, no checksums.

| Asset | CDN down, cache exists | CDN down, no cache |
|---|---|---|
| Styles | Cached copy reused | **Hard error** — `ErrStylesDownload` |
| JS runtime | Cached copy reused | **Hard error** — `ErrJSDownload` |
| Tailwind browser | Cached copy reused | **Soft error** — `ErrTailwindBrowserDownload` (proceeds without utilities) |

Sentinels are exported for `errors.Is`:

```go
ufs, err := basecoat.Init("./cache", basecoat.Watch("./public"))
switch {
case errors.Is(err, basecoat.ErrStylesDownload):
    // styles CDN unreachable and no cache — retry, abort, or build
    // a *UnionFS directly and pass basecoat.EmbeddedBasecoatCSS.
case errors.Is(err, basecoat.ErrJSDownload):
    // runtime CDN unreachable and no cache — same options with
    // basecoat.EmbeddedBasecoatJS.
}
```

`Init` surfaces errors rather than silently falling back to the embedded bytes. Callers that want the embedded fallback (`EmbeddedBasecoatCSS` / `EmbeddedBasecoatJS`, pinned to basecoat-css 1.0.2) must construct a `*UnionFS` directly and pass them as the `embeddedCSS` / `embeddedJS` arguments.

## CLI

Generates `basecoat.css` and `basecoat.js` without running a server — useful for build pipelines and CI:

```sh
# Parent: downloads styles + js, produces the full bundle
go run ./cmd/basecoat --mode=parent --source ./public --source ./components \
  --cache ./.basecoat-cache --output ./dist

# Child: no network, just user content
go run ./cmd/basecoat --mode=child --source ./team-svc --output ./dist
```

| Flag | Default | Description |
|---|---|---|
| `--mode` | `parent` | `parent` (downloads styles + js) or `child` (no network) |
| `--source` | — | Source directory (repeatable) |
| `--cache` | `./.basecoat-cache` | Download cache directory (parent mode only) |
| `--output` | `./dist` | Output directory for generated files |
| `--static` | `true` | Disable file watching |

Install globally:

```sh
go install github.com/yay101/go-basecoatui/cmd/basecoat@latest
```

## API reference

```go
// Init creates the union FS in parent mode (downloads everything).
func Init(cacheDir string, sources ...fs.FS) (FS, error)

// InitChild creates the union FS in child mode (no network, no runtime).
func InitChild(sources ...fs.FS) (FS, error)

// Watch wraps a directory in an fs.FS and registers it for polling.
func Watch(root string) fs.FS

// FS satisfies fs.FS, fs.ReadDirFS, fs.StatFS, plus:
type FS interface {
    Reload()                              // rebuild basecoat.css + basecoat.js
    AddSource(name string, src fs.FS)     // hot-add (no auto-reload)
    RemoveSource(name string) bool        // hot-remove (returns false if absent)
    Unmasked() fs.FS                      // read-only view without the basecoat/ mask
    Close() error                         // stop the poll watcher
}

// Embedded fallbacks (basecoat-css 1.0.2) for *UnionFS construction:
var EmbeddedBasecoatCSS []byte
var EmbeddedBasecoatJS  []byte

// Sentinel errors:
var ErrStylesDownload         error
var ErrJSDownload             error
var ErrTailwindBrowserDownload error
```

`*UnionFS` is also exported as the concrete implementation; tests and power users can construct it directly to pass embedded fallbacks.

## Examples

Two runnable examples live under `example/` and `spaexample/`:

```sh
cd example      && go run .   # single-page: 6 component cards, live reload
cd spaexample   && go run .   # two-page SPA: fetch + swap navigation, fragments
```

Both serve on `:8080`. The SPA example shows the full pattern: card fragments under `basecoat/html/`, page composite fragments, a fetch+swap navigator (`spa.js`) using `basecoat.destroyAll` / `initAll`, and `?fragment=1` rendering on the server side.

## Dependencies

**Zero.** Only the Go standard library.