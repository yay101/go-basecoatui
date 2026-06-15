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

There is currently **no test suite** and **no lint config**. If you add
tests, keep them stdlib-only (`testing` package). If you add a linter, prefer
`gofmt` + `go vet` rather than introducing a tool dependency. See the TODO at
the bottom of this file.

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
| `basecoat.go` | Package entry. `Init()`, `Dir()`, package config (`BasecoatVersion`, `Static`, `AutoUpdate`), `ErrUpdateAvailable`, embedded-JS registry map. |
| `version.go` | `basecoatVersions` table, `parseConstraint`, `resolveVersion`. The semver parsing is intentionally minimal — major.minor only. |
| `unionfs.go` | `UnionFS` (`fs.FS` impl), virtual file/dir types, `regenerate()` (atomic swap under write lock), `Close()`. |
| `watcher.go` | `watchSource` (mod-time map), 2-second `pollWatcher` goroutine. |
| `download.go` | `ensureCached`, `downloadFile`, `checkLatest` (unpkg `package.json`), `isNewerVersion`, `parseVersion`. |
| `generate.go` | `generateCSS`/`generateJS`, `extractUsedClasses`, `treeShakeCSS`, `splitCSSRules`, `keepRule`, `extractClassesFromSelector`. |
| `minify.go` | `minifyCSS`, `minifyJS` — simple textual passes. |
| `basecoatui/v0.X.Y/basecoat.js` | Embedded basecoat JS runtime, one directory per supported version. |
| `cmd/basecoat/main.go` | CLI with repeatable `--source` flag. |
| `example/` | Runnable demo server and pre-generated `dist/` output. |

## Public API surface

When you change behaviour, these are the symbols callers depend on:

- `basecoat.Init(cacheDir string, sources ...fs.FS) (*UnionFS, error)`
- `basecoat.Dir(root string) fs.FS` — registers the path with the poll watcher
- `(*UnionFS).Open(name string) (fs.File, error)` — must keep satisfying `fs.FS`
- `(*UnionFS).Close() error`
- Package vars: `BasecoatVersion`, `Static`, `AutoUpdate`
- Sentinel: `ErrUpdateAvailable` (use `errors.Is`)

Internal but worth knowing: `sourceFS`, `virtualFile`, `virtualDir`,
`pollWatcher`, `watchSource`, `resolvedVersion`, `versionEntry`.

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
- **Atomic swaps.** `regenerate()` rebuilds under a write lock; readers
  take the read lock. Preserve this pattern.
- **Tree-shake always includes** `*`, `html`, `body`, `:root`. Do not
  strip these even if no HTML file references them.

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

Place files under any source directory passed to `Dir()`:

```
components/
  css/button.css   # merged into basecoat.css
  js/onClick.js    # appended to basecoat.js after the runtime
```

JS files should call `basecoat.register(name, selector, initFn)`. Later
`register()` calls override earlier ones — that is how users override
built-in components.

### Regenerate the example dist

```sh
cd example
go run ../cmd/basecoat --source ./public --source ./elements --output ./dist
```

## Gotchas

- The CLI defaults `--static` to **true**; the `example/main.go` sets
  `basecoat.Static = false` to enable live reload. Do not "fix" this
  inconsistency — it matches each tool's use case.
- Use `basecoat.Dir(root)`, not bare `os.DirFS(root)`, for any source you
  want the watcher to poll. `Dir()` is the only thing that registers the
  root for change detection.
- The poll watcher reads `os.ReadDir` on the root only — it does not
  recurse. Changes in nested subdirectories (e.g. `components/css/`) will
  still trigger regeneration because the parent dir's mtime updates, but
  the watcher cannot tell you *which* file changed.
- The CSS tree-shaker keeps any rule with no class selector and all
  `@-rules` verbatim. It does not recurse into `@media` blocks.
- `checkLatest()` and `downloadFile()` perform plain `http.Get` with no
  timeout, no retries, and no checksum verification. Network failures
  surface as `Init` errors.
- Cache layout is `{cacheDir}/basecoat/v{version}/basecoat.css`. Changing
  this shape will invalidate every existing user's cache.

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

- No automated tests exist. When adding tests, prefer table-driven tests
  against small CSS/JS fixtures in `testdata/` rather than mocking
  `http.Get` (consider factoring `downloadFile` to take an `http.Client`
  first).
- No CI configuration. If added, run `go vet ./...`, `go test ./...`, and
  `gofmt -l .` as the minimum pipeline.
