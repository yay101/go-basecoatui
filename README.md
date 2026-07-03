# go-basecoatui

Zero-dependency Go module that provides a virtual filesystem combining downloaded [Basecoat](https://basecoatui.com) CSS with user-provided component directories. Produces a single minified `basecoat.css` and `basecoat.js`, and automatically regenerates them when source files change.

The library ships the **complete basecoat + Tailwind v4 bundle** (component classes and utility classes) — no separate Tailwind browser script needed. Your `index.html` just loads `basecoat.css` and `basecoat.js` and you're done.

## Features

- **UnionFS** — implements `io/fs.FS`, `fs.ReadDirFS`, and `fs.StatFS`; layers multiple source directories; injects virtual `basecoat.css` and `basecoat.js`
- **Minification** — strips comments and whitespace from CSS and JS
- **Two init modes** — `Init` (parent: downloads the basecoat shell) and `InitChild` (no network, just user content for SPA sub-apps)
- **Live reload** — 2-second poll watcher regenerates on file changes (disable with `Static` mode for production)
- **Hot-swap sources** — `AddSource` / `RemoveSource` / `Reload` for parents that host child modules over a socket
- **Templates via stdlib** — call `template.ParseFS(ufs, globs...)` yourself; the library doesn't own `html/template`
- **Reserved namespace** — `basecoat/...` is masked at the FS layer so user files never leak into the `/basecoat*` URL space

## Usage

The library exposes a virtual `fs.FS` that serves a single `basecoat.css`
(complete basecoat + tailwind bundle, with your user CSS appended) and
a single `basecoat.js` (basecoat runtime + your `basecoat/js/**/*.js`
files, minified). You parse HTML yourself with
`template.ParseFS(ufs, globs...)`.

`Init` returns a `basecoat.FS` interface that embeds `fs.FS`,
`fs.ReadDirFS`, and `fs.StatFS` plus the basecoat-specific operations
(`Reload`, `AddSource`, `RemoveSource`, `Close`). It drops straight into
`http.FileServer(http.FS(ufs))`.

```go
import (
    "log"
    "net/http"

    basecoat "github.com/yay101/go-basecoatui"
)

func main() {
    // Parent mode: downloads the basecoat CDN bundle
    // (basecoat.cdn.min.css, pinned to the latest 1.x) and the latest
    // basecoat.js runtime from jsdelivr.
    ufs, err := basecoat.Init("./cache",
        basecoat.Watch("./public"),
        basecoat.Watch("./components"),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer ufs.Close()

    mux := http.NewServeMux()
    mux.Handle("/", http.FileServer(http.FS(ufs)))
    // /basecoat/ is reserved — anything that isn't the two virtual
    // files at the root (/basecoat.css, /basecoat.js) 404s explicitly.
    mux.Handle("/basecoat/", http.NotFoundHandler())
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

Your `public/index.html` just loads the two bundles and is done —
`basecoat.css` already includes the Tailwind v4 preflight and theme
layer:

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>my app</title>
<link rel="stylesheet" href="/basecoat.css">
</head>
<body>
<!-- your markup here -->
</body>
</html>
```

## How the CSS and JS are built

`basecoat.css` is the concatenation of:
1. The basecoat CDN bundle fetched from
   `https://cdn.jsdelivr.net/npm/basecoat-css@1/dist/basecoat.cdn.min.css`
   (the URL is pinned to `@1`, so jsdelivr serves the latest 1.x
   release; downloaded once on first `Init`, cached at
   `{cacheDir}/basecoat/styles.css`, never refreshed). This is the
   complete basecoat + Tailwind v4 bundle — component classes *and*
   utility classes, preflight and theme included.
2. Every `basecoat/css/**/*.css` across your sources, in source order,
   recursively. Concatenated and minified.

`basecoat.js` is the concatenation of:
1. The basecoat.js runtime fetched from
   `https://cdn.jsdelivr.net/npm/basecoat-css/dist/js/all.min.js` (the
   URL is unpinned, so jsdelivr serves the latest published version
   on every `Init`). If the download fails, a previously cached copy
   at `{cacheDir}/basecoat/basecoat.js` is used instead (the runtime
   is just a version behind). If no cache exists, `Init` returns an
   error — see "Download failures" below.
2. The **lifecycle shim** (`lifecycle.js`, embedded via
   `lifecycle.go`) that wraps `basecoat.register` with an optional
   `destroy(el)` and adds `basecoat.destroy` / `destroyAll` /
   `unregister`. Parent mode only — see "Component lifecycle (destroy)".
3. Every `basecoat/js/**/*.js` across your sources, in source order,
   recursively. Concatenated and minified.

There is no tree-shaking. The prebuilt bundle is small enough that
stripping unused rules from your user CSS would save bytes at the
cost of a much more complex pipeline. The minifier still strips
comments and whitespace.

## File inclusion rules

There is no required folder structure beyond the reserved `basecoat/`
subdirectory. The library picks files up by location inside `basecoat/`.
You can pass any number of source directories to `Init` (or `--source`
to the CLI) and arrange them however you like:

| File pattern | What it does |
|---|---|
| `basecoat/css/**/*.css` (recursive) | Concatenated into `basecoat.css` and minified |
| `basecoat/js/**/*.js` (recursive) | Appended to `basecoat.js` (after the runtime in parent mode, alone in child mode) |
| `basecoat/html/**/*.html` (recursive) | Masked at the union FS. Use the raw source `fs.FS` to glob fragments with a second `template.ParseFS` call. |
| anything else | Served verbatim through the union FS — use this for HTML, images, etc. |

Everything else in a source is ignored at generation time — it still
passes through as a regular file served by `http.FileServer` because
`UnionFS` is a layered `fs.FS`, **except** paths under `basecoat/`,
which 404 (the namespace is reserved).

A typical layout:

```
my-project/
├── public/
│   ├── index.html                # page template, parseable via template.ParseFS
│   └── card.html                 # fragment, also served by the FS
└── components/
    └── basecoat/
        ├── css/
        │   ├── button.css        # merged into basecoat.css
        │   └── card.css
        └── js/
            ├── onClick.js        # appended to basecoat.js
            └── todo.js
```

Both `public/` and `components/` are passed to `Init`; the library
doesn't care that one is HTML-only and the other is CSS+JS. The
generated `basecoat.css` is the concatenation of the downloaded
basecoat CSS plus every `basecoat/css/**/*.css` across all sources —
minified. The generated `basecoat.js` is the embedded basecoat
runtime plus every `basecoat/js/**/*.js` across all sources — minified.

If you'd rather keep fragments under a reserved `basecoat/html/`
folder (so they never appear at a URL), they're masked from the
union FS — see "Fragments in a reserved folder" below for the
two-glob pattern.

## Templates

The library no longer owns `html/template` parsing. Callers do it
themselves with `template.ParseFS(ufs, globs...)`, which walks the
union FS and matches the glob:

```go
t, err := template.ParseFS(ufs, "**/*.html")
if err != nil { /* ... */ }
_ = t.Execute(w, data)
```

The glob picks up every `.html` file across all sources, *except*
paths under `basecoat/` (the namespace is reserved). Put your
fragments anywhere outside `basecoat/` — e.g. at the source root, or
in a `components/` or `fragments/` subdirectory. The standard
`html/template` directives (`{{define}}`, `{{block}}`, `{{template}}`)
wire the fragments into the page template.

```go
mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
    t, err := template.ParseFS(ufs, "**/*.html")
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    // Register funcs per-request by cloning:
    t = t.Funcs(template.FuncMap{
        "dict": func(kv ...any) map[string]any { /* ... */ },
    })
    _ = t.Execute(w, nil)
})
```

`components/card.html`:

```html
{{define "card"}}
<section class="card">
  <h2>{{.Title}}</h2>
  <p>{{.Description}}</p>
</section>
{{end}}
```

`public/index.html`:

```html
{{template "card" dict "Title" "Hello" "Description" "World"}}
```

If multiple sources both define a fragment with the same name, the
first source's `define` wins (the union FS dedupes by path).
Scope your glob to a specific path (e.g.
`template.ParseFS(ufs, "public/**/*.html")`) to keep each source's
fragments in their own namespace.

### Fragments in a reserved folder

If you want to keep fragments under each source's `basecoat/html/...`
tree (for organisation, so they never appear at a URL), they're
masked out of the union FS — the glob above won't find them. Use the
raw source `fs.FS` you passed to `Init` for a second `ParseFS` call,
then re-parse the fragment files into the page template so the page's
`{{template "name" .}}` lookups resolve:

```go
elementsFS := basecoat.Watch("./elements")
ufs, _ := basecoat.Init("./cache", elementsFS)

// Page from the union FS (basecoat/html/ is masked out).
pageTmpl, err := template.ParseFS(ufs, "**/*.html")
if err != nil { /* ... */ }

// Fragments from the raw source — another glob, another FS.
fragMatches, err := fs.Glob(elementsFS, "basecoat/html/*.html")
if err != nil { /* ... */ }
for _, match := range fragMatches {
    f, _ := elementsFS.Open(match)
    data, _ := io.ReadAll(f)
    f.Close()
    pageTmpl, err = pageTmpl.Parse(string(data))
    if err != nil { /* ... */ }
}
_ = pageTmpl.Execute(w, data)
```

The same pattern works per-source in a multi-source setup: each
source's raw `fs.FS` (saved before being passed to `Init`) is the
view onto that source's `basecoat/...` subtree. Compose as many
fragment globs as you need. The page and the fragments don't share a
namespace in the union FS, so two sources can each define a
fragment named `card.html` at the same path and each page picks up
only its own.

## Parent vs child (SPA pattern)

`Init` is **parent mode**: it downloads the basecoat shell (the
prebuilt `styles.css` and the latest runtime JS) and serves them
alongside your user content. The parent's HTML loads
`/basecoat.css` and `/basecoat.js` once, and that's the only place
the basecoat shell is requested.

`InitChild` is **child mode**: no network, no cache. It produces
just your user CSS (concatenated from every source's
`basecoat/css/`) and just your user JS (concatenated from every
source's `basecoat/js/`). No embedded runtime, because the parent's
`basecoat.js` is already loaded — the child's JS just calls
`basecoat.register(...)` to add its components to the global
registry on page load.

Typical SPA host:

```go
basecoat.Static = true

parent,  _ := basecoat.Init("./cache", basecoat.Watch("./public"))
team,    _ := basecoat.InitChild(basecoat.Watch("./team-svc"))
billing, _ := basecoat.InitChild(basecoat.Watch("./billing-svc"))

mux := http.NewServeMux()

// Parent: serves the SPA shell.
mux.Handle("/basecoat.css", serveFrom(parent, "basecoat.css"))
mux.Handle("/basecoat.js",  serveFrom(parent, "basecoat.js"))

// Each child: serves its own additive bundles.
mux.Handle("/team/basecoat.css",    serveFrom(team,    "basecoat.css"))
mux.Handle("/team/basecoat.js",     serveFrom(team,    "basecoat.js"))
mux.Handle("/billing/basecoat.css", serveFrom(billing, "basecoat.css"))
mux.Handle("/billing/basecoat.js",  serveFrom(billing, "basecoat.js"))
```

The SPA shell's `index.html` loads `/basecoat.css`, `/basecoat.js`,
then loads each child's bundles. Each child is independently
reloadable.

## Component JS

The embedded runtime provides a [basecoat](https://basecoat.dev) compatible API:

```js
window.basecoat.register(name, selector, initFn, destroyFn?)   // destroyFn optional — see "Component lifecycle (destroy)"
window.basecoat.init(name)
window.basecoat.initAll()
window.basecoat.start()
window.basecoat.stop()
```

User JS files (anywhere under `basecoat/js/**/*.js`) should call
`basecoat.register()` to define components:

```js
basecoat.register('chat', '#my-chat:not([data-chat-initialized])', function(el) {
  // el is the matching DOM node
  el.addEventListener('submit', function(e) { /* ... */ });
  el.dataset.chatInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
})

// later calls override earlier ones with the same name
basecoat.register('dropdown-menu', '.dropdown-menu:not([data-dropdown-menu-initialized])', function(el) {
  // override the built-in dropdown-menu
})
```

After an `innerHTML` swap (e.g. htmx fragment), re-initialise everything:

```js
basecoat.initAll()
```

In child mode, the `basecoat.js` you emit contains only your user JS —
the runtime is already loaded by the parent. The user JS still calls
`basecoat.register()` exactly the same way; it just relies on
`window.basecoat` being on the page by the time it runs.

## Component lifecycle (destroy)

The parent bundle appends a small **lifecycle shim** (`lifecycle.js`,
embedded via `lifecycle.go`) right after the upstream basecoat runtime.
It wraps `window.basecoat.register` to accept an optional fourth
argument — a `destroy(el)` teardown callback — and adds a few helpers
so SPA shells can clean up a page's components before swapping
`innerHTML` (stopping intervals, aborting fetches, dropping listeners
on detached DOM). Child bundles do not append the shim: the parent
page has already loaded it onto `window.basecoat`, and the child's
`register(...)` calls hit the wrapped function automatically.

The shim is idempotent (guarded by `window.basecoat.__lifecycle`), so
loading it twice — including a stray copy in a child bundle — is
harmless but wasteful.

```js
basecoat.register(name, selector, init, destroy?)   // 4th arg optional
basecoat.destroy(el)        // run destroy(el) for every initialised
                            //   component whose root is el or inside el,
                            //   then clear the data-<name>-initialized marker
basecoat.destroyAll(root)  // sugar for destroy(root || document.body)
basecoat.unregister(name)  // stop calling destroy for name (init still runs)
```

Omit `destroy` for components with no teardown needs — existing
`register(name, selector, init)` calls keep working unchanged. A
component that starts a poller, for example:

```js
basecoat.register(
  'logs-system-page',
  '[data-logs-system-page]:not([data-logs-system-page-initialized])',
  function (el) {
    el.setAttribute('data-logs-system-page-initialized', 'true');
    var timer = setInterval(refresh, 10000);   // capture in this closure
    // remember the handle so destroy can clear it
    el.__logsTimer = timer;
  },
  function (el) {                              // destroy
    if (el.__logsTimer) { clearInterval(el.__logsTimer); el.__logsTimer = null; }
  }
);
```

Then in the SPA shell, before an `innerHTML` swap:

```js
if (window.basecoat && window.basecoat.destroyAll) {
  try { window.basecoat.destroyAll(main); } catch (_e) {}
}
main.innerHTML = html;
basecoat.initAll();   // re-init the freshly inserted tree
```

`destroy(el)` walks every registered component, finds initialised
elements at or beneath `el`, runs the matching `destroy(el)` (soft-failed
per component), then removes the `data-<name>-initialized` marker so a
later `initAll()` re-runs `init` cleanly. SSR/MPA sites need none of
this — the browser tears everything down on navigation, and the
`destroy` arg is optional.

## Hot-swap sources

For setups where the source set isn't known at construction time
(parent processes that receive `fs.FS` content from child services
over a Unix socket, plugin systems, multi-tenant hosts, etc.) the
library exposes `AddSource`, `RemoveSource`, and `Reload` on the
returned `FS`:

```go
ufs, _ := basecoat.Init("./cache")
ufs.AddSource("child-1", childFS1)   // any fs.FS the parent built
ufs.AddSource("child-2", childFS2)   //    from incoming socket data
ufs.Reload()                          // explicit — caller batches
// ... later, when child-1 disconnects:
ufs.RemoveSource("child-1")
ufs.Reload()
```

Semantics:

- `AddSource` does **not** auto-reload. The caller batches multiple
  add/remove operations and calls `Reload` once. The right shape for
  a parent handling bursts of connections.
- `RemoveSource` returns `false` for unknown names and is otherwise a
  no-op on the rest of the list. Order of remaining sources is
  preserved — first-match-wins across `Open()` calls is unchanged.
- Sources added via `AddSource` are **not** watched by the poll
  watcher (the watcher was started with the initial sources only).
  The parent is responsible for triggering `Reload` on external
  changes for `AddSource`'d entries. In a `Static = true` deployment
  the parent owns all reload signals.
- `Reload` is concurrency-safe and re-entrant from the poll watcher
  callback. `Open()` always sees the previous or next version, never a
  half-built one.

## Download failures

Both CDN downloads (styles + JS) use a shared `http.Client` with a
**30-second timeout**, so a stalled or hung CDN connection surfaces as
an error instead of blocking `Init` forever. There are no retries and
no checksum verification.

`Init` handles failures as follows:

| Asset | CDN down, cache exists | CDN down, no cache |
|---|---|---|
| Styles (`basecoat.cdn.min.css`) | Cached copy reused, no error | **Hard error** — `ErrStylesDownload` |
| JS runtime (`all.min.js`) | Cached copy reused, no error | **Hard error** — `ErrJSDownload` |

Both sentinels are exported for `errors.Is`:

```go
import (
    "errors"

    basecoat "github.com/yay101/go-basecoatui"
)

ufs, err := basecoat.Init("./cache", basecoat.Watch("./public"))
switch {
case errors.Is(err, basecoat.ErrStylesDownload):
    // styles CDN unreachable and no cached styles.css
case errors.Is(err, basecoat.ErrJSDownload):
    // runtime CDN unreachable and no cached basecoat.js — you can
    // either retry, abort, or build a *UnionFS directly and pass
    // basecoat.EmbeddedBasecoatJS as the embedded runtime.
}
```

`Init` no longer silently falls back to the embedded runtime on a JS
download failure — it returns the error so the caller knows. If you
want the old "keep the server running on a stale embedded runtime"
behaviour, construct a `*UnionFS` directly and pass
`basecoat.EmbeddedBasecoatJS` as the `embeddedJS` argument.

## CLI

The module ships with a command-line tool that generates `basecoat.css` and `basecoat.js` without running a server — useful for build pipelines and CI.

```sh
# Parent: downloads styles + js, produces the full bundle
go run github.com/yay101/go-basecoatui/cmd/basecoat \
  --mode=parent \
  --source ./public \
  --source ./components \
  --cache ./.basecoat-cache \
  --output ./dist

# Child: no network, just user content
go run github.com/yay101/go-basecoatui/cmd/basecoat \
  --mode=child \
  --source ./team-svc \
  --output ./dist
```

| Flag | Default | Description |
|---|---|---|
| `--mode` | `parent` | `parent` (downloads styles + js) or `child` (no network) |
| `--source` | — | Source directory (repeatable) |
| `--cache` | `./.basecoat-cache` | Download cache directory (parent mode only) |
| `--output` | `./dist` | Output directory for generated files |
| `--static` | `true` | Disable file watching (default true for cli) |

Install globally:

```sh
go install github.com/yay101/go-basecoatui/cmd/basecoat@latest
```

## Package-level configuration

Set this before calling `Init`:

| Variable | Default | Description |
|---|---|---|
| `Static` | `false` | Disable the poll watcher. Generation runs once. |

## Updating the embedded basecoat.js runtime

The `//go:embed basecoatui/v0.3.11/basecoat.js` byte slice
(`basecoat.EmbeddedBasecoatJS`) is a last-resort fallback for callers
that construct a `*UnionFS` directly when the CDN is unreachable and
no cache exists. `Init` itself no longer uses it silently — it
returns `ErrJSDownload` instead — but it's still exported so a caller
can deliberately opt into a stale runtime to keep the server running
through a CDN outage.

The pinned version doesn't have to match the latest published runtime
— it's a safety net, not the source of truth. To update it, drop the
new file at `basecoatui/v<version>/basecoat.js` and change the embed
directive in `basecoat.go`. The downloaded version is always the
latest from jsdelivr; the embed is only consulted when a caller
passes it in explicitly.

## Dependencies

**Zero.** Only `net/http`, `os`, `io`, `io/fs`, `embed`, `html/template`, `sort`, `sync`, `time`, `strings`, `regexp`, `errors`, `fmt`, `path/filepath` — all from the Go standard library.
