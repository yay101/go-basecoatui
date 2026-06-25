# go-basecoatui

Zero-dependency Go module that provides a virtual filesystem combining downloaded [Basecoat](https://basecoatui.com) CSS with user-provided component directories. Produces a single minified, tree-shaken `basecoat.css` and `basecoat.js`, and automatically regenerates them when source files change.

The library ships the **basecoat component classes only** (no Tailwind utility classes). Your HTML also needs the Tailwind v4 browser script for utilities to work — see [How Tailwind is included](#how-tailwind-is-included) below.

## Features

- **UnionFS** — implements `io/fs.FS`, `fs.ReadDirFS`, and `fs.StatFS`; layers multiple source directories; injects virtual `basecoat.css` and `basecoat.js`
- **Tree-shaking** — scans `.html` files for used class names, drops unused CSS rules
- **Minification** — strips comments and whitespace from CSS and JS
- **Version pinning** — built-in version table maps basecoat releases to download URLs; semver constraints like `^0.3.11` resolve to a concrete CSS file
- **Auto-download** — fetches and caches `basecoat.cdn.min.css` (component classes only) on first init
- **Component JS** — embedded basecoat runtime (`window.basecoat.register(...)`) plus user-provided `basecoat/js/**/*.js` files; later `register()` calls override earlier ones
- **html/template** — `Template()` / `TemplateFuncs()` parse page templates out of the union FS and auto-load every `basecoat/html/**/*.html` as a fragment (results cached, invalidated by `Reload`)
- **Asset-only sources** — `AddAssetSource()` registers a child service's CSS/JS/fragments without serving its pages (parent aggregates, child serves itself)
- **Live reload** — 2-second poll watcher regenerates on file changes (disable with `Static` mode for production)
- **Auto-update notification** — optional check for newer basecoat versions, returns a sentinel error you can catch and log
- **Reserved namespace** — `basecoat/...` is masked at the FS layer so user files never leak into the `/basecoat*` URL space

## Usage

The library exposes a virtual `fs.FS` that serves a single `basecoat.css`
(basecoat component classes, tree-shaken against your HTML) and a single
`basecoat.js` (embedded basecoat runtime + your `basecoat/js/**/*.js`
files, minified). You still need to add the Tailwind v4 browser script
to your HTML yourself so utility classes work — see
[How Tailwind is included](#how-tailwind-is-included) below.

`Init` returns a `basecoat.FS` interface that embeds `fs.FS`,
`fs.ReadDirFS`, and `fs.StatFS` plus the basecoat-specific operations
(`Reload`, `AddSource`, `RemoveSource`, `Template`, `TemplateFuncs`,
`Close`). It drops straight into `http.FileServer(http.FS(ufs))`.

```go
import (
    "errors"
    "log"
    "net/http"

    basecoat "github.com/yay101/go-basecoatui"
)

func main() {
    // Pin a basecoat version so Init downloads and caches basecoat.cdn.min.css
    // on first run. Leave empty to serve only your local assets (no component
    // classes, no Tailwind utilities).
    basecoat.BasecoatVersion = "^0.3.11"

    // Disable file watching in production.
    // basecoat.Static = true

    ufs, err := basecoat.Init("./cache",
        basecoat.Dir("./public"),
        basecoat.Dir("./components"),
    )
    if errors.Is(err, basecoat.ErrUpdateAvailable) {
        log.Println("update available:", err) // still usable
    } else if err != nil {
        log.Fatal(err)
    }
    defer ufs.Close()

    mux := http.NewServeMux()
    mux.Handle("/", http.FileServer(http.FS(ufs)))
    // /basecoat/ is reserved — anything that isn't the two virtual files
    // at the root (/basecoat.css, /basecoat.js) 404s explicitly.
    mux.Handle("/basecoat/", http.NotFoundHandler())
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

Your `public/index.html` then loads the two stylesheets/scripts side by side:

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>my app</title>
<link rel="stylesheet" href="/basecoat.css">                              <!-- basecoat component classes, tree-shaken -->
<script src="https://unpkg.com/@tailwindcss/browser@4"></script>          <!-- Tailwind v4 utilities, generated from your HTML at runtime -->
</head>
<body>
<!-- your markup here -->
</body>
</html>
```

## How Tailwind is included

The library downloads `basecoat.cdn.min.css` from unpkg, which is built
with `@source(none)` and so contains only the basecoat component classes
(`.btn`, `.card`, `.input`, `.select`, `.popover`, `.toast`, etc.) — no
generic utility classes. The pre-compiled Tailwind v4 build is not
published to any public CDN, and Tailwind v4's npm package only ships
source fragments that need `npx tailwindcss` to compile.

The supported path is the official Tailwind v4 browser build
(`@tailwindcss/browser@4`), which is a JS bundle that processes your
HTML at runtime and generates utility classes as it sees them. This
gives you the full Tailwind v4 utility set (`flex`, `gap-4`, `p-4`,
`text-muted-foreground`, etc.) without any build step or Go-side
compilation.

The trade-off: Tailwind processing happens in the browser (small first-paint
cost), and you depend on a CDN at runtime. If you want a fully
self-contained CSS with no CDN dependency, you'd need to run Tailwind
locally and commit the output — out of scope for this library.

## File inclusion rules

There is no required folder structure beyond the reserved `basecoat/`
subdirectory. The library picks files up by extension and by location
inside `basecoat/`. You can pass any number of source directories to
`Init` (or `--source` to the CLI) and arrange them however you like:

| File pattern | What it does |
|---|---|
| `**/*.html` (anywhere, recursive) | Scanned for class names used in the tree-shaker |
| `basecoat/css/**/*.css` (recursive) | Concatenated into `basecoat.css` and tree-shaken |
| `basecoat/js/**/*.js` (recursive) | Appended to `basecoat.js` after the embedded runtime |
| `basecoat/html/**/*.html` (recursive) | Parsed as `html/template` fragments by `Template`/`TemplateFuncs` |

Everything else in a source is ignored at generation time — it still
passes through as a regular file served by `http.FileServer` because
`UnionFS` is a layered `fs.FS`, **except** paths under `basecoat/`,
which 404 (the namespace is reserved).

A typical layout:

```
my-project/
├── public/
│   ├── index.html                # page template, parseable via Template()
│   └── about.html                # also scanned for class names
└── components/
    └── basecoat/
        ├── css/
        │   ├── button.css        # merged into basecoat.css
        │   └── card.css
        ├── js/
        │   ├── onClick.js        # appended to basecoat.js
        │   └── todo.js
        └── html/
            └── card.html         # {{define "card"}}...{{end}} — fragment
```

Both `public/` and `components/` are passed to `Init`; the library
doesn't care that one is HTML-only and the other is CSS+JS+fragments.
The generated `basecoat.css` is the concatenation of the downloaded
basecoat CSS plus every `basecoat/css/**/*.css` across all sources —
tree-shaken and minified. The generated `basecoat.js` is the embedded
basecoat runtime plus every `basecoat/js/**/*.js` across all sources —
minified.

## Templates

`Template()` and `TemplateFuncs()` parse page templates out of the
union FS and auto-load every `*.html` file under any source's
`basecoat/html/` tree (recursive) as `html/template` fragments.
Standard `{{define}}` / `{{block}}` / `{{template}}` directives wire
fragments into page templates.

```go
// Render the page, with a dict helper so we can build maps inline.
funcs := template.FuncMap{
    "dict": func(kv ...any) map[string]any {
        m := make(map[string]any, len(kv)/2)
        for i := 0; i < len(kv); i += 2 {
            m[kv[i].(string)] = kv[i+1]
        }
        return m
    },
}

mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
    t, err := ufs.TemplateFuncs(funcs, "index.html")
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    _ = t.Execute(w, nil)
})
```

`components/basecoat/html/card.html`:

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

`Template` caches its result and reuses it until the next `Reload`
(which the poll watcher triggers on file changes). Callers no longer
need to cache the `*template.Template` themselves. The cache is keyed
by the joined match list plus the identity of the funcs map — reuse
the same `template.FuncMap` value across calls (define it once at
startup) to get cache hits; a freshly-allocated FuncMap per call is
always a miss. A cached parse error is also reused until Reload, so a
broken template won't be re-parsed on every request.

`html/template` errors on duplicate `{{define}}` names across sources.
Keep fragment names unique across the union.

## Component JS

The embedded runtime provides a [basecoat](https://basecoat.dev) compatible API:

```js
window.basecoat.register(name, selector, initFn)
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

## Asset-only sources (child services)

`AddAssetSource(name, src fs.FS)` is for child services that ship
their `basecoat/css/`, `basecoat/js/`, `basecoat/html/`, and any
`*.html` files to a parent for inclusion in the parent's single
`basecoat.css` / `basecoat.js`, but **serve their own pages via their
own mux prefix**. The asset source is invisible to `Open` / `ReadDir`
/ `Stat` — its files never appear at any URL on the parent. The
parent's `Template()` resolves match targets against full sources
only, but collects fragments from both full and asset sources.

| Aspect | `AddSource` (full) | `AddAssetSource` (asset) |
|---|---|---|
| `Open` / `ReadDir` / `Stat` | yes | **no** |
| `basecoat/css/**/*.css` → `basecoat.css` | tree-shaken | tree-shaken |
| `basecoat/js/**/*.js` → `basecoat.js` | yes | yes |
| `**/*.html` scanned for used classes | yes | yes |
| `basecoat/html/**/*.html` as fragments | yes | yes |
| Poll watcher | yes (if via `Dir()`) | no |

```go
ufs, _ := basecoat.Init("./cache", basecoat.Dir("./public"))
// A child service sends its fs.FS over a socket; the parent adds it
// as an asset-only source. The child's CSS/JS/fragments merge into
// the parent's basecoat.css / basecoat.js; the child's pages are not
// served by the parent (the child has its own mux prefix).
ufs.AddAssetSource("team-svc", childFS)
ufs.Reload()
```

Re-registering a name with either method replaces the existing entry
and may switch its kind (full ↔ asset). `RemoveSource(name)` removes
an entry of either kind.

## CLI

The module ships with a command-line tool that generates `basecoat.css` and `basecoat.js` without running a server — useful for build pipelines and CI.

```sh
go run github.com/yay101/go-basecoatui/cmd/basecoat \
  --source ./public \
  --source ./components \
  --version ^0.3.11 \
  --output ./dist
```

| Flag | Default | Description |
|---|---|---|
| `--source` | — | Source directory (repeatable) |
| `--cache` | `./.basecoat-cache` | Download cache directory |
| `--output` | `./dist` | Output directory for generated files |
| `--version` | `""` | Basecoat version constraint |
| `--static` | `true` | Disable file watching |

Install globally:

```sh
go install github.com/yay101/go-basecoatui/cmd/basecoat@latest
```

## Package-level configuration

Set these before calling `Init`:

| Variable | Default | Description |
|---|---|---|
| `BasecoatVersion` | `""` | Semver constraint e.g. `"^0.3.11"`. Empty = skip downloads. |
| `Static` | `false` | Disable the poll watcher. Generation runs once. |
| `AutoUpdate` | `false` | Check unpkg for a newer basecoat version. Returns `ErrUpdateAvailable` if found. |

## Adding a version entry

Edit `version.go` and add a new entry to `basecoatVersions`. The URL must
point at a pre-compiled basecoat CSS — `basecoat.cdn.min.css` is the
canonical source on unpkg.

```go
"0.4": {
    BasecoatVersion: "0.4.0",
    BasecoatURL:     "https://unpkg.com/basecoat-css@0.4.0/dist/basecoat.cdn.min.css",
},
```

Embed the corresponding JS runtime file at `basecoatui/v0.4.0/basecoat.js` and register it in `basecoatUIEmbeds` in `basecoat.go`.

## Dependencies

**Zero.** Only `net/http`, `os`, `io`, `io/fs`, `embed`, `html/template`, `sort`, `sync`, `time`, `strings`, `regexp`, `errors`, `fmt`, `path/filepath`, `encoding/json`, `strconv` — all from the Go standard library.
