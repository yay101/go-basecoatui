# go-basecoatui

Zero-dependency Go module that provides a virtual filesystem combining downloaded [Basecoat](https://basecoatui.com) CSS with user-provided component directories. Produces a single minified, tree-shaken `basecoat.css` and `basecoat.js`, and automatically regenerates them when source files change.

The library ships the **basecoat component classes only** (no Tailwind utility classes). Your HTML also needs the Tailwind v4 browser script for utilities to work — see [How Tailwind is included](#how-tailwind-is-included) below.

## Features

- **UnionFS** — implements `io/fs.FS`, layers multiple source directories, injects virtual `basecoat.css` and `basecoat.js`
- **Tree-shaking** — scans `.html` files for used class names, drops unused CSS rules
- **Minification** — strips comments and whitespace from CSS and JS
- **Version pinning** — built-in version table maps basecoat releases to download URLs; semver constraints like `^0.3.11` resolve to a concrete CSS file
- **Auto-download** — fetches and caches `basecoat.cdn.min.css` (component classes only) on first init
- **Component JS** — embedded basecoat runtime (`window.basecoat.register(...)`) plus user-provided `/js/*.js` files; later `register()` calls override earlier ones
- **Live reload** — 2-second poll watcher regenerates on file changes (disable with `Static` mode for production)
- **Auto-update notification** — optional check for newer basecoat versions, returns a sentinel error you can catch and log

## Usage

The library exposes a virtual `fs.FS` that serves a single `basecoat.css`
(basecoat component classes, tree-shaken against your HTML) and a single
`basecoat.js` (embedded basecoat runtime + your `js/*.js` files, minified).
You still need to add the Tailwind v4 browser script to your HTML
yourself so utility classes work — see [How Tailwind is included](#how-tailwind-is-included) below.

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
    )
    if errors.Is(err, basecoat.ErrUpdateAvailable) {
        log.Println("update available:", err) // still usable
    } else if err != nil {
        log.Fatal(err)
    }
    defer ufs.Close()

    log.Fatal(http.ListenAndServe(":8080", http.FileServer(http.FS(ufs))))
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

## Directory layout

Place your component files in directories that you pass as sources:

```
my-project/
├── public/
│   └── index.html              <!-- scanned for class tree-shaking -->
└── components/
    ├── css/
    │   ├── button.css           <!-- merged into basecoat.css -->
    │   └── card.css
    └── js/
        ├── onClick.js           <!-- runs basecoat.register(...) -->
        └── todo.js              <!-- appended to basecoat.js -->
```

The generated `basecoat.css` is the concatenation of downloaded basecoat CSS and every `components/**/css/*.css` file — tree-shaken and minified. The generated `basecoat.js` is the embedded basecoat runtime plus every `components/**/js/*.js` file — minified.

## Component JS

The embedded runtime provides a [basecoat](https://basecoat.dev) compatible API:

```js
window.basecoat.register(name, selector, initFn)
window.basecoat.init(name)
window.basecoat.initAll()
window.basecoat.start()
window.basecoat.stop()
```

User JS files should call `basecoat.register()` to define components:

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

**Zero.** Only `net/http`, `os`, `io/fs`, `embed`, `sync`, `time`, `strings`, `regexp`, `errors`, `fmt`, `path/filepath`, `encoding/json`, `strconv` — all from the Go standard library.
