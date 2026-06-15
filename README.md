# go-basecoatui

Zero-dependency Go module that provides a virtual filesystem combining downloaded [Basecoat](https://basecoatui.com) CSS with user-provided component directories. Produces a single minified, tree-shaken `basecoat.css` and `basecoat.js`, and automatically regenerates them when source files change.

The library ships the **basecoat component classes only** (downloaded from `basecoat.cdn.min.css`, which is built with `@source(none)` and so contains no utility classes). Add Tailwind v4 utility classes to your HTML via the documented browser build:

```html
<script src="https://unpkg.com/@tailwindcss/browser@4"></script>
```

This generates utilities from your HTML at runtime; the library's tree-shaker then drops any basecoat component classes your HTML does not reference.

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

```go
import (
    "errors"
    "log"
    "net/http"

    basecoat "github.com/yay101/go-basecoatui"
)

func main() {
    // Optional: pin a basecoat version to download CSS assets.
    // basecoat.BasecoatVersion = "^0.3.11"

    // Disable file watching in production.
    // basecoat.Static = true

    ufs, err := basecoat.Init("./cache",
        basecoat.Dir("./components"),
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
basecoat.register('todo', '#todo-app:not([data-todo-initialized])', function(el) {
  // el is the matching DOM node
})

basecoat.register('dropdown-menu', '.dropdown-menu:not([data-dropdown-menu-initialized])', function(el) {
  // override built-in component
})
```

After an `innerHTML` swap, re-initialise with:

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
