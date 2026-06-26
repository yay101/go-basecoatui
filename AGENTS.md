# AGENTS.md

Guidance for AI coding agents working on `github.com/yay101/go-basecoatui`.

## Project overview

Zero-dependency Go 1.22 module that produces a virtual `fs.FS` layering
downloaded basecoat + tailwind CSS with user-provided component
directories. It emits a single minified `basecoat.css` and `basecoat.js`,
and auto-regenerates them on file changes. A CLI in `cmd/basecoat`
produces the same output for build pipelines.

The library ships two init entry points, each producing a `UnionFS`
with the same two virtual files at the root (`basecoat.css`,
`basecoat.js`):

- **`Init(cacheDir, sources...)`** is **parent mode**. It downloads
  basecoat's prebuilt styles from `https://basecoatui.com/assets/styles.css`
  into `{cacheDir}/basecoat/styles.css` (download-once, never refreshed)
  and fetches the latest basecoat.js runtime from jsdelivr on every
  `Init`. `basecoat.css` = styles + user CSS. `basecoat.js` = runtime +
  user JS.
- **`InitChild(sources...)`** is **child mode**. No network, no cache.
  `basecoat.css` = user CSS only. `basecoat.js` = user JS only — no
  embedded runtime, because the parent has already loaded it. The
  child's JS uses `basecoat.register()` on page load to add its
  components to the global registry.

`basecoat.css` is the prebuilt bundle from basecoatui.com, which
already includes the Tailwind v4 preflight and theme layer. No
`@tailwindcss/browser` script tag is required.

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

# Tests — run with -race to catch the Open/RemoveSource snapshot races
go test -race ./...
```

There is **no lint config**. If you add a linter, prefer `gofmt` + `go vet`
rather than introducing a tool dependency. A test suite exists — keep new
tests stdlib-only (`testing` package, table-driven where possible) and
prefer `fstest.MapFS` fixtures over mocking `http.Get`. Network-dependent
code paths (the JS download) are tested via `httptest.NewServer` and
override the package-level `basecoatJSURL` var.

### Running the example

```sh
cd example
go run .
# serves on :8080
```

### Running the CLI

```sh
# Parent: downloads styles + js, produces the full bundle
go run ./cmd/basecoat --mode=parent --cache ./cache \
  --source ./public --source ./components --output ./dist

# Child: no network, just user content
go run ./cmd/basecoat --mode=child \
  --source ./team-svc --output ./dist
```

## Repository layout

| File | Responsibility |
|---|---|
| `basecoat.go` | Package entry. `FS` interface, `Init` (parent), `InitChild`, `Watch()`, `Mask()`, `Static` config, `//go:embed` basecoat runtime fallback. |
| `unionfs.go` | `UnionFS` (`fs.FS` + `fs.ReadDirFS` + `fs.StatFS` impl), virtual file/dir types, `Reload` (atomic swap under write lock), `ReadDir`/`Stat`, `basecoat/` namespace masking, `snapshotSources` helper, `Close()`. |
| `watcher.go` | `watchSource` (mod-time map), 2-second `pollWatcher` goroutine. |
| `download.go` | `downloadFile`, `ensureBasecoatStyles` (download-once), `ensureBasecoatJS` (always-download with embedded fallback). |
| `generate.go` | `generateCSS`, `generateJS`, `walkExt`. |
| `minify.go` | `minifyCSS`, `minifyJS` — simple textual passes. |
| `basecoatui/v0.X.Y/basecoat.js` | Embedded basecoat JS runtime, used as a network-failure fallback in parent mode. |
| `cmd/basecoat/main.go` | CLI with `--mode=parent|child`, `--cache`, repeated `--source`. |
| `example/` | Runnable demo server that uses `template.ParseFS(ufs, "**/*.html")` in its index handler. |

## Public API surface

When you change behaviour, these are the symbols callers depend on:

- `basecoat.Init(cacheDir string, sources ...fs.FS) (FS, error)` — parent mode: downloads styles.css + jsdelivr basecoat.js, embeds the runtime into basecoat.js
- `basecoat.InitChild(sources ...fs.FS) (FS, error)` — child mode: no network, no embedded runtime, just user content
- `basecoat.FS` interface — embeds `fs.FS`, `fs.ReadDirFS`, `fs.StatFS` plus `Reload`, `AddSource`, `RemoveSource`, `Close`
- `basecoat.Watch(root string) fs.FS` — registers the path with the poll watcher
- `(FS).Open(name string) (fs.File, error)` — must keep satisfying `fs.FS`; masks `basecoat/...` paths
- `(FS).ReadDir(name string) ([]fs.DirEntry, error)` — merged listing; masks `basecoat` and `basecoat/...`
- `(FS).Stat(name string) (fs.FileInfo, error)` — masks `basecoat/...`
- `(FS).AddSource(name string, src fs.FS)` — hot-add a full source at runtime (served + contributes to generation)
- `(FS).RemoveSource(name string) bool` — hot-remove a source; returns false if no such name
- `(FS).Reload()` — rebuild `basecoat.css` and `basecoat.js` from the current set of sources
- `(FS).Close() error`
- `*UnionFS` is still exported as the concrete implementation; tests and power users can type-assert/declare it directly
- Package vars: `Static`

Internal but worth knowing: `sourceFS`, `sourceRef`, `virtualFile`,
`virtualDir`, `pollWatcher`, `watchSource`, `watchableRoot`, `masked`,
`walkExt`, `snapshotSources`, `newUnionFS`, `embeddedBasecoatJS`,
`basecoatJSURL`.

## Conventions

- **stdlib only.** No external imports. Match the existing minimalism
  of the regex-based minifier.
- **One concern per file.** The package is deliberately split by
  responsibility. New code should follow the same shape — a new file
  for a new concern, not a 500-line `basecoat.go`.
- **Godoc comments on exported symbols.** The codebase uses standard
  godoc-style comments above every exported func, type, and var.
  Internal helpers carry brief comments. Match that style on anything
  you add.
- **Error wrapping.** Wrap with `fmt.Errorf("%w: ...", sentinel, ...)`
  so callers can `errors.Is(err, ...)`. Never swallow errors silently
  in `Reload()` — the current behaviour is to drop the regenerated
  output and keep the previous good data, which is intentional for the
  live-reload path but should not be replicated elsewhere.
- **Atomic swaps.** `Reload()` rebuilds under a write lock; readers
  take the read lock. Preserve this pattern. `snapshotSources()` makes
  a deep copy of the sources slice so `Open`/`ReadDir` can iterate
  without racing `AddSource`/`RemoveSource`.
- **Reserved namespace.** Any path that is `basecoat` or starts with
  `basecoat/` is masked at the `Open`/`ReadDir`/`Stat` layer. User
  files at those paths never resolve through the union FS; only the
  two virtual files at the root (`basecoat.css`, `basecoat.js`)
  survive. Internal operations (`generateCSS`, `generateJS`) walk the
  raw sources directly so they can read `basecoat/{css,js}/...`
  without being blocked by the mask.

## Source layout

Every source passed to `Init` / `InitChild` (or `Watch`) follows the
same convention. The `basecoat/` subdirectory is reserved for
library-managed assets; everything else is served verbatim through the
union FS.

```
<source>/
├── index.html                      # any HTML — glob it with template.ParseFS
├── about.html
└── basecoat/                       # reserved namespace — masked at Open/ReadDir
    ├── css/**/*.css                # merged into basecoat.css (recursive)
    ├── js/**/*.js                  # appended to basecoat.js (parent: after runtime; child: alone)
    └── html/**/*.html              # NOT served; access via the raw source fs.FS for fragments
```

| File pattern | What it does |
|---|---|
| `basecoat/css/**/*.css` (recursive) | Concatenated into `basecoat.css` and minified (parent: after the downloaded styles; child: alone) |
| `basecoat/js/**/*.js` (recursive) | Appended to `basecoat.js` (parent: after the embedded runtime; child: alone) |
| `basecoat/html/**/*.html` (recursive) | Masked at the union FS. Use the raw source `fs.FS` (saved before being passed to `Init`) to glob fragments with a second `template.ParseFS` call. |
| anything else | Served verbatim through the union FS. Use this for HTML, images, etc. |

Anything under `basecoat/...` is masked at the `Open` / `ReadDir` /
`Stat` layer. If you want `template.ParseFS(ufs, "**/*.html")` to find
your HTML files (including fragments), put them outside `basecoat/`,
or use the raw source `fs.FS` you passed to `Init` for a second
`template.ParseFS` call (see "Fragments in a reserved folder" above).
The library walks `basecoat/css/` and `basecoat/js/` directly via the
raw source FS during generation, so the mask doesn't apply there.

## Common tasks

### Add a user component

Place files under any source directory passed to `Watch()`, inside the
reserved `basecoat/` subtree:

```
components/
  basecoat/
    css/button.css    # merged into basecoat.css
    js/onClick.js     # appended to basecoat.js
```

JS files should call `basecoat.register(name, selector, initFn)`. Later
`register()` calls override earlier ones — that is how users override
built-in components.

### Render HTML

The library no longer owns `html/template`. Callers parse their HTML
themselves with `template.ParseFS(ufs, globs...)` against the union FS:

```go
ufs, _ := basecoat.Init("./cache", basecoat.Watch("./public"))

t, err := template.ParseFS(ufs, "**/*.html")
if err != nil { /* ... */ }
_ = t.Execute(w, data)
```

The glob walks the union FS, picking up every `.html` file across all
sources. Put your fragments anywhere outside `basecoat/` so the mask
doesn't hide them.

### Fragments in a reserved folder

If you want to keep fragments under each source's `basecoat/html/...`
tree (for organisation, so they never appear at a URL), they're
masked out of the union FS — the glob above won't find them. Use the
raw source `fs.FS` you passed to `Init` for a second `ParseFS` call,
then re-parse the fragment files into the page template so the page's
`{{template "name" .}}` lookups resolve:

```go
elementsFS := basecoat.Dir("./elements")
ufs, _ := basecoat.Init("./cache", elementsFS)

// Page from the union FS (basecoat/html/ is masked out).
pageTmpl, _ := template.ParseFS(ufs, "**/*.html")

// Fragments from the raw source — another glob, another FS.
fragMatches, _ := fs.Glob(elementsFS, "basecoat/html/*.html")
for _, match := range fragMatches {
    f, _ := elementsFS.Open(match)
    data, _ := io.ReadAll(f)
    f.Close()
    pageTmpl, _ = pageTmpl.Parse(string(data))
}
_ = pageTmpl.Execute(w, data)
```

The same pattern works per-source in a multi-source setup: each
source's raw `fs.FS` (saved before being passed to `Init`) is the
view onto that source's `basecoat/...` subtree. Compose as many
fragment globs as you need. The page and the fragments don't share a
namespace in the union FS, so two sources can each define a fragment
named `card.html` at the same path and each page picks up only its
own.

### Render one source in isolation (parent mode)

If a source ships its own page and its own fragments, scope the parse
to that source's subtree via the `*html/template` glob. There is no
special API for this — glob against a more specific path:

```go
teamFS, _ := basecoat.InitChild(basecoat.Dir("./team-svc"))
t, err := template.ParseFS(teamFS, "**/*.html")
```

### Host a parent + several children (SPA pattern)

```go
basecoat.Static = true

parent,   _ := basecoat.Init("./cache", basecoat.Dir("./public"))
team,     _ := basecoat.InitChild(basecoat.Dir("./team-svc"))
billing,  _ := basecoat.InitChild(basecoat.Dir("./billing-svc"))

mux := http.NewServeMux()
mux.Handle("/basecoat.css",  serveFrom(parent, "basecoat.css"))
mux.Handle("/basecoat.js",   serveFrom(parent, "basecoat.js"))
mux.Handle("/team/basecoat.css",     serveFrom(team, "basecoat.css"))
mux.Handle("/team/basecoat.js",      serveFrom(team, "basecoat.js"))
mux.Handle("/billing/basecoat.css",  serveFrom(billing, "basecoat.css"))
mux.Handle("/billing/basecoat.js",   serveFrom(billing, "basecoat.js"))
```

The parent's HTML loads its own `basecoat.css` + `basecoat.js` (which
includes the basecoat runtime), then loads each child's bundles. The
child's `basecoat.js` calls `basecoat.register(...)` to wire its
components into the global registry — no duplicate runtime in the
child bundle.

### Regenerate the example dist

```sh
go run ./cmd/basecoat --mode=parent --cache ./example/cache \
  --source ./example/public --source ./example/elements \
  --output ./example/dist
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
- `ensureBasecoatStyles` downloads `https://basecoatui.com/assets/styles.css`
  on first Init and never refreshes. If you need a fresh copy, delete
  `{cacheDir}/basecoat/styles.css` and restart.
- `ensureBasecoatJS` re-downloads `https://cdn.jsdelivr.net/npm/basecoat-css/dist/js/all.min.js`
  on every Init (the URL is unpinned so jsdelivr serves the latest).
  If the download fails it falls back to the `//go:embed`d bytes in
  `basecoatui/v0.3.11/basecoat.js`, then to the cache copy. The
  fallback chain: fresh download → cached copy → embedded bytes.
  Update the embedded file when the project cuts a new runtime.
- `ensureBasecoatJS` and `ensureBasecoatStyles` use plain `http.Get` with
  no timeout, no retries, and no checksum verification. `Init` surfaces
  styles.css failures as a hard error; JS failures fall back to the
  embedded bytes (or the cache) so the server can still start.
- Cache layout is `{cacheDir}/basecoat/{styles.css,basecoat.js}`. Changing
  this shape will invalidate every existing user's cache.
- HTML files inside `basecoat/...` are masked from `Open` / `ReadDir` /
  `Stat` (and from `template.ParseFS(ufs, "**/*.html")` globs). If you
  want the glob to find them, put them outside `basecoat/`.
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
  can inject a stub instead of overriding the package-level `basecoatJSURL`
  var.
- No CI configuration. If added, run `go vet ./...`, `go test -race ./...`,
  and `gofmt -l .` as the minimum pipeline.
