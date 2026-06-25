# AGENTS.md

Guidance for AI coding agents working on `github.com/yay101/go-basecoatui`.

## Project overview

Zero-dependency Go 1.22 module that produces a virtual `fs.FS` layering
downloaded Basecoat CSS with user-provided component directories. It
emits a single minified, tree-shaken `basecoat.css` and `basecoat.js`,
and auto-regenerates them on file changes. A CLI in `cmd/basecoat`
produces the same output for build pipelines.

The library ships the **basecoat component classes only** (downloaded
from `basecoat.cdn.min.css`, built with `@source(none)` so no utility
classes are included). Projects that want Tailwind v4 utility classes
load them separately — the recommended approach is the
`@tailwindcss/browser@4` script tag, which generates utilities from the
HTML at runtime. The tree-shaker then drops any basecoat component
classes the user's HTML does not reference.

The hard constraint: **only the Go standard library**. No new third-party
dependencies. If a problem seems to require one, prefer a simpler textual /
regex solution in line with the existing code.

## Build & verify

```sh
# Build the module and CLI
go build ./...
go build ./cmd/basecoat

# Static checks (always run before finishing)
go vet ./...

# Tests
go test ./...
```

There is **no lint config**. If you add a linter, prefer `gofmt` + `go vet`
rather than introducing a tool dependency. A test suite exists — keep new
tests stdlib-only (`testing` package, table-driven where possible) and
prefer `fstest.MapFS` fixtures over mocking `http.Get`.

### Running the example

```sh
cd example
go run .
# serves on :8080
```

### Running the CLI

```sh
go run ./cmd/basecoat \
  --source ./public \
  --source ./components \
  --version ^0.3.11 \
  --output ./dist
```

## Repository layout

| File | Responsibility |
|---|---|
| `basecoat.go` | Package entry. `FS` interface, `Init()`, `Dir()`, package config (`BasecoatVersion`, `Static`, `AutoUpdate`), `ErrUpdateAvailable`, embedded-JS registry map. |
| `version.go` | `basecoatVersions` table, `parseConstraint`, `resolveVersion`. The semver parsing is intentionally minimal — major.minor only. |
| `unionfs.go` | `UnionFS` (`fs.FS` + `fs.ReadDirFS` + `fs.StatFS` impl), virtual file/dir types, `Reload` (atomic swap under write lock), `ReadDir`/`Stat`, `basecoat/` namespace masking, `Close()`. |
| `template.go` | `Template` / `TemplateFuncs` — `html/template` parsing over the union FS with auto-loaded `basecoat/html/**/*.html` fragments. |
| `watcher.go` | `watchSource` (mod-time map), 2-second `pollWatcher` goroutine. |
| `download.go` | `ensureCached`, `downloadFile`, `checkLatest` (unpkg `package.json`), `isNewerVersion`, `parseVersion`. |
| `generate.go` | `generateCSS`/`generateJS`, `extractUsedClasses`, `treeShakeCSS`, `splitCSSRules`, `keepRule`, `extractClassesFromSelector`, `walkExt`. |
| `minify.go` | `minifyCSS`, `minifyJS` — simple textual passes. |
| `basecoatui/v0.X.Y/basecoat.js` | Embedded basecoat JS runtime, one directory per supported version. |
| `cmd/basecoat/main.go` | CLI with repeatable `--source` flag. |
| `example/` | Runnable demo server (uses `TemplateFuncs` for the index page) and pre-generated `dist/` output. |

## Public API surface

When you change behaviour, these are the symbols callers depend on:

- `basecoat.Init(cacheDir string, sources ...fs.FS) (FS, error)` — returns the `FS` interface
- `basecoat.FS` interface — embeds `fs.FS`, `fs.ReadDirFS`, `fs.StatFS` plus `Reload`, `AddSource`, `AddAssetSource`, `RemoveSource`, `Template`, `TemplateFuncs`, `Close`
- `basecoat.Dir(root string) fs.FS` — registers the path with the poll watcher
- `(FS).Open(name string) (fs.File, error)` — must keep satisfying `fs.FS`; masks `basecoat/...` paths
- `(FS).ReadDir(name string) ([]fs.DirEntry, error)` — merged listing; masks `basecoat` and `basecoat/...`
- `(FS).Stat(name string) (fs.FileInfo, error)` — masks `basecoat/...`
- `(FS).AddSource(name string, src fs.FS)` — hot-add a full source at runtime (served + contributes to generation)
- `(FS).AddAssetSource(name string, src fs.FS)` — hot-add an asset-only source (contributes to CSS/JS/class extraction/fragments but is NOT served through Open/ReadDir/Stat)
- `(FS).RemoveSource(name string) bool` — hot-remove a source of either kind; returns false if no such name
- `(FS).Reload()` — rebuild `basecoat.css` and `basecoat.js` from the current set of sources (full and asset)
- `(FS).Template(match ...string) (*html/template.Template, error)` — parse page (resolved against full sources) + auto-loaded `basecoat/html/**/*.html` fragments (collected from both full and asset sources)
- `(FS).TemplateFuncs(funcs template.FuncMap, match ...string) (*html/template.Template, error)` — like `Template` but registers funcs before parsing (fragments can call them)
- `(FS).Close() error`
- `*UnionFS` is still exported as the concrete implementation; tests and power users can type-assert/declare it directly
- Package vars: `BasecoatVersion`, `Static`, `AutoUpdate`
- Sentinel: `ErrUpdateAvailable` (use `errors.Is`)

Internal but worth knowing: `sourceFS`, `sourceRef`, `virtualFile`, `virtualDir`,
`pollWatcher`, `watchSource`, `resolvedVersion`, `versionEntry`,
`watchableRoot`, `masked`, `walkExt`, `contentFS`.

## Conventions

- **stdlib only.** No external imports. Match the existing minimalism of
  the regex-based minifier and tree-shaker.
- **One concern per file.** The package is deliberately split by
  responsibility. New code should follow the same shape — a new file for a
  new concern, not a 500-line `basecoat.go`.
- **Godoc comments on exported symbols.** The codebase uses standard
  godoc-style comments above every exported func, type, and var. Internal
  helpers carry brief comments. Match that style on anything you add.
- **Error wrapping.** Wrap with `fmt.Errorf("%w: ...", sentinel, ...)` so
  callers can `errors.Is(err, ErrUpdateAvailable)`. Never swallow errors
  silently in `regenerate()` — the current behaviour is to drop the
  regenerated output and keep the previous good data, which is intentional
  for the live-reload path but should not be replicated elsewhere.
- **Atomic swaps.** `Reload()` rebuilds under a write lock; readers
  take the read lock. Preserve this pattern.
- **Tree-shake always includes** `*`, `html`, `body`, `:root`. Do not
  strip these even if no HTML file references them.
- **Reserved namespace.** Any path that is `basecoat` or starts with
  `basecoat/` is masked at the `Open`/`ReadDir`/`Stat` layer. User
  files at those paths never resolve through the union FS; only the
  two virtual files at the root (`basecoat.css`, `basecoat.js`)
  survive. Internal operations (`generateCSS`, `generateJS`,
  `Template`) walk the raw sources directly so they can read
  `basecoat/{css,js,html}/...` without being blocked by the mask.

## Source layout

Every source passed to `Init` (or `Dir`) follows the same convention.
The `basecoat/` subdirectory is reserved for library-managed assets;
everything else is served verbatim through the union FS.

```
<source>/
├── index.html                      # page templates, parseable via Template()
├── about.html
└── basecoat/                       # reserved namespace — masked at Open/ReadDir
    ├── css/**/*.css                # merged into basecoat.css (recursive)
    ├── js/**/*.js                  # appended to basecoat.js after the runtime
    └── html/**/*.html              # picked up by Template() as fragments (recursive)
```

| File pattern | What it does |
|---|---|
| `**/*.html` (anywhere, recursive) | Scanned for class names used in the tree-shaker |
| `basecoat/css/**/*.css` (recursive) | Concatenated into `basecoat.css` and tree-shaken |
| `basecoat/js/**/*.js` (recursive) | Appended to `basecoat.js` after the embedded runtime |
| `basecoat/html/**/*.html` (recursive) | Parsed as `html/template` fragments by `Template`/`TemplateFuncs` |

Anything else in a source is ignored at generation time but still
served as a regular file by `http.FileServer` because `UnionFS` is a
layered `fs.FS` — except paths under `basecoat/`, which 404.

## Common tasks

### Add a new basecoat version

1. Add an entry to `basecoatVersions` in `version.go` keyed by `major.minor`.
2. Drop the JS runtime at `basecoatui/v<exact-version>/basecoat.js` and
   add a `//go:embed` directive plus a map entry in `basecoatUIEmbeds` in
   `basecoat.go`.
3. Use the new version from a caller by setting
   `basecoat.BasecoatVersion = "^X.Y.Z"`.

The README has a worked example.

### Add a user component

Place files under any source directory passed to `Dir()`, inside the
reserved `basecoat/` subtree:

```
components/
  basecoat/
    css/button.css    # merged into basecoat.css
    js/onClick.js     # appended to basecoat.js after the runtime
    html/card.html    # {{define "card"}}...{{end}} — picked up by Template()
```

JS files should call `basecoat.register(name, selector, initFn)`. Later
`register()` calls override earlier ones — that is how users override
built-in components.

HTML fragments use standard `html/template` directives (`{{define}}`,
`{{block}}`) and are auto-loaded by `Template`/`TemplateFuncs` so page
templates can reference them via `{{template "name" .}}`.

### Regenerate the example dist

```sh
go run ./cmd/basecoat --source ./example/public --source ./example/elements --output ./example/dist
```

### Hot-swap sources at runtime

Designed for parent services that host child modules over a Unix
socket (or any setup where the set of sources is not known at `Init`
time). Sources are added/removed by name; `Reload` rebuilds the
virtual CSS/JS:

```go
ufs, _ := basecoat.Init("./cache")           // no sources yet, or pass some
ufs.AddSource("child-1", childFS1)           // childFS1 is any fs.FS the parent
                                              // built from incoming socket data
ufs.AddSource("child-2", childFS2)
ufs.Reload()                                 // explicit — caller batches
// ... later, when child-1 disconnects:
ufs.RemoveSource("child-1")
ufs.Reload()
```

Semantics worth knowing:

- `AddSource` does not auto-reload. The caller batches multiple
  add/remove operations and calls `Reload` once. This is the right
  shape for a parent that handles bursts of child connections.
- `RemoveSource` returns `false` for unknown names; the source list
  is otherwise unchanged. Order of remaining sources is preserved
  (first-match-wins across `Open` calls is unchanged).
- Sources added via `AddSource` are **not** watched by the poll
  watcher — the watcher was started with the initial sources only.
  The parent is responsible for triggering `Reload` on external
  changes for `AddSource`'d entries.
- `Reload` is concurrency-safe and re-entrant from the poll watcher
  callback. `Open` always sees the previous or next version, never a
  half-built one.
- `Reload` also invalidates the template cache. The poll watcher
  triggers Reload on file changes, so `Template` / `TemplateFuncs`
  stay fresh without callers doing their own invalidation. A cached
  parse error is also invalidated by Reload.

### Asset-only sources (child services)

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

## Gotchas

- The CLI defaults `--static` to **true**; the `example/main.go` sets
  `basecoat.Static = false` to enable live reload. Do not "fix" this
  inconsistency — it matches each tool's use case.
- Use `basecoat.Dir(root)`, not bare `os.DirFS(root)`, for any source you
  want the watcher to poll. `Dir()` is the only thing that registers the
  root for change detection.
- The poll watcher reads `os.ReadDir` on the root only — it does not
  recurse. Changes in nested subdirectories (e.g. `components/basecoat/css/`)
  will still trigger regeneration because the parent dir's mtime updates,
  but the watcher cannot tell you *which* file changed.
- The CSS tree-shaker keeps any rule with no class selector and all
  `@-rules` verbatim. It does not recurse into `@media` blocks.
- `checkLatest()` and `downloadFile()` perform plain `http.Get` with no
  timeout, no retries, and no checksum verification. Network failures
  surface as `Init` errors.
- Cache layout is `{cacheDir}/basecoat/v{version}/basecoat.css`. Changing
  this shape will invalidate every existing user's cache.
- `Template` / `TemplateFuncs` cache their result and reuse it until
  the next `Reload` (which the poll watcher triggers on file changes).
  Callers no longer need to cache the `*template.Template` themselves.
  The cache is keyed by the joined match list plus the identity of the
  funcs map — reuse the same `template.FuncMap` value across calls
  (define it once at startup) to get hits; a freshly-allocated FuncMap
  per call is always a miss. A cached parse error is also reused until
  Reload, so a broken template won't be re-parsed on every request.
- `html/template` errors on duplicate `{{define}}` names across sources.
  If two sources both define a fragment named `"card"`, `Template` will
  fail at parse time. Keep fragment names unique across the union.
- Callers that mount the FS over HTTP should add
  `mux.Handle("/basecoat/", http.NotFoundHandler())` so anything that
  isn't the two virtual files at the root 404s explicitly at the
  routing layer. The library masks `basecoat/...` inside the FS; the
  mux rule is defence in depth.

## What NOT to do

- Do not add a third-party dependency for any reason.
- Do not break the `fs.FS` contract on `UnionFS` (no path-cleaning changes,
  no `Open` returning directories that don't satisfy `fs.ReadDirFile`
  expectations of callers like `http.FileServer`).
- Do not add inline comments to code that doesn't already have a comment
  style — match the surrounding file. The project has none in `minify.go`
  except for the var-block header, but plenty in `generate.go`.
- Do not bump the minimum Go version without a clear reason. Current floor
  is 1.22.

## TODO

- Consider factoring `downloadFile` to take an `http.Client` so tests
  can inject a stub instead of mocking `http.Get`.
- No CI configuration. If added, run `go vet ./...`, `go test ./...`,
  and `gofmt -l .` as the minimum pipeline.
