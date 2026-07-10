package basecoat

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Compile-time checks that UnionFS implements the standard fs interfaces
// the FS interface in basecoat.go promises. The unmasked view satisfies
// the same read-only interfaces.
var (
	_ fs.FS        = (*UnionFS)(nil)
	_ fs.ReadDirFS = (*UnionFS)(nil)
	_ fs.StatFS    = (*UnionFS)(nil)

	_ fs.FS        = (*unmaskedFS)(nil)
	_ fs.ReadDirFS = (*unmaskedFS)(nil)
	_ fs.StatFS    = (*unmaskedFS)(nil)
)

// UnionFS implements fs.FS by layering multiple source filesystems and
// injecting two virtual files — basecoat.css and basecoat.js — that are
// regenerated whenever source content changes.
//
// Virtual files:
//   - basecoat.css  minified combination of (optionally) the downloaded
//     basecoat styles.css plus every
//     basecoat/css/**/*.css across all sources
//   - basecoat.js   minified combination of (optionally) the
//     basecoat.js runtime plus every
//     basecoat/js/**/*.js across all sources
//
// In parent mode the optional basecoat assets are set (one or both):
// the styles.css is read from disk on every Reload (falling back to
// embeddedCSS when the path is empty or unreadable), the runtime is
// held in memory as embeddedJS (with its on-disk path remembered for
// logging). In child mode both are zero/nil and the output is just
// user content.
//
// Non-virtual paths are resolved by searching sources in order and
// returning the first match (classic overlay behaviour).
//
// Reserved namespace: any path that is "basecoat" or starts with
// "basecoat/" is masked. User files at those paths never resolve;
// only the two virtual files at the root (basecoat.css, basecoat.js)
// survive the mask. This makes the /basecoat* URL namespace exclusive
// to the library. Callers that mount the FS over HTTP should add a
// /basecoat/ -> 404 rule to make the reservation explicit at the
// routing layer.
//
// Sources can be added at construction time (via Init) or at runtime
// (via AddSource / RemoveSource). After mutating the source set the
// caller must invoke Reload to regenerate the virtual CSS/JS.
type UnionFS struct {
	mu        sync.RWMutex
	sources   []sourceFS
	sourceIdx map[string]sourceRef
	cssData   []byte
	jsData    []byte

	// Parent-mode only. In child mode both stay empty and
	// generateCSS/generateJS skip them, producing user-only output.
	basecoatStylesPath string // path to downloaded styles.css; "" in child mode or when download failed
	embeddedCSS        []byte // fallback styles bytes (used when stylesPath is "")
	basecoatJSPath     string // path to downloaded basecoat.js;  "" in child mode or when download failed
	embeddedJS         []byte // fallback runtime bytes (used when jsPath is "")

	// tailwindBrowserJS is the Tailwind v4 browser build, prepended to
	// basecoat.js in parent mode so a single <script> tag yields both
	// the runtime utilities (flex, grid, p-4, ...) and the basecoat
	// component lifecycle. nil in child mode or when the download
	// failed and no cache existed (graceful degradation: basecoat
	// components still render, but Tailwind utility classes do not).
	tailwindBrowserJS []byte

	cachePath string
	watcher   *pollWatcher
	static    bool
}

// newUnionFS builds a UnionFS wired up with the given sources and
// parent-mode asset paths/bytes. sources are registered as
// "init-0", "init-1", ... in order. The caller is responsible for
// calling Reload (and startWatcher, if !Static) afterwards.
func newUnionFS(sources []fs.FS, basecoatStylesPath string, embeddedCSS []byte, basecoatJSPath string, embeddedJS []byte, twPath string, twBrowserJS []byte, cachePath string) *UnionFS {
	srcs := make([]sourceFS, 0, len(sources))
	srcIdx := make(map[string]sourceRef, len(sources))
	for i, s := range sources {
		name := "init-" + itoa(i)
		sf := sourceFS{name: name, fs: s}
		if root, ok := watchableRoot(s); ok {
			sf.root = root
			sf.ws = newWatchSource(sf.root)
		}
		srcs = append(srcs, sf)
		srcIdx[name] = sourceRef{index: len(srcs) - 1}
	}

	return &UnionFS{
		sources:            srcs,
		sourceIdx:          srcIdx,
		basecoatStylesPath: basecoatStylesPath,
		embeddedCSS:        embeddedCSS,
		basecoatJSPath:     basecoatJSPath,
		embeddedJS:         embeddedJS,
		tailwindBrowserJS:  twBrowserJS,
		cachePath:          cachePath,
		static:             Static,
	}
}

// itoa avoids importing strconv just for source names.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// sourceRef points at a sourceFS in u.sources.
type sourceRef struct {
	index int
}

// masked reports whether name falls inside the reserved basecoat/
// namespace and must not resolve to a user file.
func masked(name string) bool {
	return name == "basecoat" || strings.HasPrefix(name, "basecoat/")
}

// snapshotSources returns an independent copy of the current sources
// slice so callers can iterate it without holding the lock or racing
// against AddSource / RemoveSource. sourceFS is a value type whose
// fields are interface/pointer — copying the slice entries gives us
// each entry's view of the source at the time of the snapshot.
func (u *UnionFS) snapshotSources() []sourceFS {
	u.mu.RLock()
	defer u.mu.RUnlock()
	out := make([]sourceFS, len(u.sources))
	copy(out, u.sources)
	return out
}

// Open implements fs.FS. It handles the two virtual paths specially,
// rejects anything inside the reserved basecoat/ namespace, and
// delegates everything else to the underlying source filesystems.
func (u *UnionFS) Open(name string) (fs.File, error) {
	return u.openWith(name, true)
}

// openWith is the masked-aware core of Open. When mask is true the
// reserved basecoat/ namespace is hidden (the default, public view);
// when mask is false it resolves to real user content (the view
// returned by Unmasked). The two virtual files at the root
// (basecoat.css, basecoat.js) always resolve regardless of mask.
func (u *UnionFS) openWith(name string, mask bool) (fs.File, error) {
	if name == "basecoat.css" {
		u.mu.RLock()
		data := u.cssData
		u.mu.RUnlock()
		return newVirtualFile("basecoat.css", data), nil
	}
	if name == "basecoat.js" {
		u.mu.RLock()
		data := u.jsData
		u.mu.RUnlock()
		return newVirtualFile("basecoat.js", data), nil
	}
	if mask && masked(name) {
		return nil, fs.ErrNotExist
	}
	sources := u.snapshotSources()
	for _, src := range sources {
		f, err := src.fs.Open(name)
		if err == nil {
			return f, nil
		}
	}
	if name == "." {
		return u.openRootDirWith(sources, mask)
	}
	return nil, fs.ErrNotExist
}

// openRootDir builds a merged directory listing from all sources plus
// the two virtual file entries, masking any source entry that falls
// inside the reserved basecoat/ namespace.
func (u *UnionFS) openRootDir() (fs.File, error) {
	sources := u.snapshotSources()
	return u.openRootDirWith(sources, true)
}

// openRootDirWith is the masked-aware core of openRootDir. When mask is
// true the reserved basecoat/ namespace is dropped from the root
// listing; when mask is false it is included so an unmasked view can
// enumerate the user's basecoat/ subtree.
func (u *UnionFS) openRootDirWith(sources []sourceFS, mask bool) (fs.File, error) {
	u.mu.RLock()
	cssData := u.cssData
	jsData := u.jsData
	u.mu.RUnlock()

	entries := mergeDirEntries(sources, ".", mask)
	// Append the two virtual files. Use a synthetic dirEntry with
	// pre-built FileInfo so Stat-via-Info returns the right size.
	entries = append(entries,
		dirEntry{info: &virtualFileInfo{name: "basecoat.css", size: int64(len(cssData)), mod: time.Now()}},
		dirEntry{info: &virtualFileInfo{name: "basecoat.js", size: int64(len(jsData)), mod: time.Now()}},
	)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return &virtualDir{entries: entries}, nil
}

// mergeDirEntries enumerates name across every source (first-match-wins
// on entry name) and returns the union. When mask is true, entries that
// fall inside the reserved basecoat/ namespace are dropped.
func mergeDirEntries(sources []sourceFS, name string, mask bool) []fs.DirEntry {
	seen := make(map[string]bool)
	var out []fs.DirEntry
	for _, src := range sources {
		f, err := src.fs.Open(name)
		if err != nil {
			continue
		}
		d, ok := f.(fs.ReadDirFile)
		if !ok {
			f.Close()
			continue
		}
		list, _ := d.ReadDir(-1)
		f.Close()
		for _, e := range list {
			n := e.Name()
			// Skip "." and ".." — some FS backends (notably
			// fstest.MapFS for an empty dir) include a self
			// entry; they're not meaningful at the union layer.
			if n == "." || n == ".." {
				continue
			}
			if mask && masked(n) {
				continue
			}
			if seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, dirEntry{entry: e})
		}
	}
	return out
}

// ReadDir implements fs.ReadDirFS. For "." it returns the merged root
// listing (with the basecoat/ namespace masked). For "basecoat" or any
// "basecoat/..." path it returns fs.ErrNotExist. For any other name
// it opens the directory on each source and merges the resulting
// listings, first-match-wins on name.
func (u *UnionFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return u.readDirWith(name, true)
}

// readDirWith is the masked-aware core of ReadDir. When mask is true
// the reserved basecoat/ namespace is hidden; when mask is false it is
// enumerable, so an unmasked view can list the user's basecoat/ tree.
func (u *UnionFS) readDirWith(name string, mask bool) ([]fs.DirEntry, error) {
	if name == "." {
		f, err := u.openRootDirWith(u.snapshotSources(), mask)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		d, ok := f.(fs.ReadDirFile)
		if !ok {
			return nil, fs.ErrInvalid
		}
		return d.ReadDir(-1)
	}
	if mask && masked(name) {
		return nil, fs.ErrNotExist
	}
	sources := u.snapshotSources()

	entries := mergeDirEntries(sources, name, false)
	if len(entries) == 0 {
		// Distinguish "dir exists but is empty" from "no such dir".
		// If no source gave us a ReadDirFile but at least one source
		// has the path as a directory (via Stat), return an empty
		// slice rather than ErrNotExist so callers can enumerate an
		// empty unioned directory.
		if dirExistsAnywhere(sources, name) {
			return []fs.DirEntry{}, nil
		}
		return nil, fs.ErrNotExist
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// dirExistsAnywhere reports whether any source resolves name as a
// directory, via Open+Stat or via fs.StatFS. Used by ReadDir to keep
// empty-but-real directories from looking like missing paths.
func dirExistsAnywhere(sources []sourceFS, name string) bool {
	for _, src := range sources {
		if statFS, ok := src.fs.(fs.StatFS); ok {
			if info, err := statFS.Stat(name); err == nil && info.IsDir() {
				return true
			}
			continue
		}
		f, err := src.fs.Open(name)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		f.Close()
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// Stat implements fs.StatFS. It returns synthetic FileInfo values for
// the two virtual files and for ".", masks anything inside basecoat/,
// and otherwise delegates to the first source that has the file.
func (u *UnionFS) Stat(name string) (fs.FileInfo, error) {
	return u.statWith(name, true)
}

// statWith is the masked-aware core of Stat. When mask is true the
// reserved basecoat/ namespace is hidden; when mask is false the
// unmasked view can stat user files under basecoat/.
func (u *UnionFS) statWith(name string, mask bool) (fs.FileInfo, error) {
	switch name {
	case ".":
		return &virtualDirInfo{}, nil
	case "basecoat.css", "basecoat.js":
		u.mu.RLock()
		var size int64
		if name == "basecoat.css" {
			size = int64(len(u.cssData))
		} else {
			size = int64(len(u.jsData))
		}
		u.mu.RUnlock()
		return &virtualFileInfo{name: name, size: size, mod: time.Now()}, nil
	}
	if mask && masked(name) {
		return nil, fs.ErrNotExist
	}
	f, err := u.openWith(name, mask)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// Unmasked returns an fs.FS, fs.ReadDirFS, and fs.StatFS view of the
// same union that does NOT mask the reserved basecoat/ namespace. Use
// it for template.ParseFS when you want globs like
// "basecoat/html/*.html" to find fragments that the masked UnionFS
// hides from serving.
//
// The view shares the underlying sources and the regenerated
// basecoat.css / basecoat.js data with u: Reload, AddSource, and
// RemoveSource on u apply to the unmasked view too. The view is read-
// only — it has no Reload/AddSource/RemoveSource/Close of its own; call
// those on the parent UnionFS. The two virtual files at the root
// (basecoat.css, basecoat.js) still resolve on the unmasked view.
//
// Callers that mount the FS over HTTP should keep using the masked
// UnionFS for the file server and reserve /basecoat/ at the routing
// layer; the unmasked view is intended for in-process template parsing
// only, not for serving.
func (u *UnionFS) Unmasked() fs.FS {
	return &unmaskedFS{u: u}
}

// unmaskedFS is a read-only view over a *UnionFS that does not apply
// the reserved basecoat/ namespace mask. It satisfies fs.FS,
// fs.ReadDirFS, and fs.StatFS by forwarding to the masked-aware cores
// with mask=false.
type unmaskedFS struct {
	u *UnionFS
}

func (v *unmaskedFS) Open(name string) (fs.File, error) {
	return v.u.openWith(name, false)
}

func (v *unmaskedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return v.u.readDirWith(name, false)
}

func (v *unmaskedFS) Stat(name string) (fs.FileInfo, error) {
	return v.u.statWith(name, false)
}

// AddSource registers src under name as a full source: its files are
// served through Open/ReadDir/Stat AND contribute to generation (CSS
// and JS output). Replaces any existing source with the same name.
// Order of registration is preserved for first-match-wins semantics
// across Open() calls. Does not auto-reload — call Reload when the set
// of sources has settled.
//
// If src was returned by Watch() the underlying root path is tracked so
// the poll watcher can poll it, but the poll watcher (if any) is not
// retroactively rewired: the watcher was started with the initial
// sources only. The parent is responsible for triggering Reload on
// external changes for AddSource'd entries.
func (u *UnionFS) AddSource(name string, src fs.FS) {
	u.mu.Lock()
	defer u.mu.Unlock()

	sf := sourceFS{name: name, fs: src}
	if root, ok := watchableRoot(src); ok {
		sf.root = root
		sf.ws = newWatchSource(sf.root)
	}

	if ref, exists := u.sourceIdx[name]; exists {
		u.sources[ref.index] = sf
		return
	}

	u.sources = append(u.sources, sf)
	if u.sourceIdx == nil {
		u.sourceIdx = make(map[string]sourceRef)
	}
	u.sourceIdx[name] = sourceRef{index: len(u.sources) - 1}
}

// RemoveSource drops the source with the given name. Returns false if
// no such source was registered. Does not auto-reload — call Reload to
// regenerate basecoat.css and basecoat.js without the removed source.
// Order of the remaining sources is preserved.
func (u *UnionFS) RemoveSource(name string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	ref, ok := u.sourceIdx[name]
	if !ok {
		return false
	}

	u.sources = append(u.sources[:ref.index], u.sources[ref.index+1:]...)
	u.reindexFull(ref.index)
	delete(u.sourceIdx, name)
	return true
}

// reindexFull rebuilds the sourceIdx entries for u.sources starting at
// the given offset (used after a splice that shifts later entries).
func (u *UnionFS) reindexFull(from int) {
	for j := from; j < len(u.sources); j++ {
		u.sourceIdx[u.sources[j].name] = sourceRef{index: j}
	}
}

// Reload rebuilds basecoat.css and basecoat.js from the current set of
// sources. Atomic with respect to Open() — readers see the previous or
// next version, never a half-built one. Safe to call concurrently and
// safe to call from inside the poll watcher callback.
func (u *UnionFS) Reload() {
	u.mu.RLock()
	stylesPath := u.basecoatStylesPath
	embeddedCSS := u.embeddedCSS
	jsPath := u.basecoatJSPath
	embeddedJS := u.embeddedJS
	twBrowserJS := u.tailwindBrowserJS
	u.mu.RUnlock()

	// Walk the unmasked view of this UnionFS so basecoat/{css,js}/... is
	// enumerable while the mask still hides it from the public Open path.
	// This keeps generation on the same first-match-wins overlay
	// semantics as every other path instead of concatenating across
	// sources, which was a divergence from how Open resolves files.
	ufs := u.Unmasked()

	// Re-read the cached styles.css on every Reload so a refreshed
	// download between Inits is picked up without a server restart.
	// Falls back to embeddedCSS when the path is empty or the file is
	// unreadable (e.g. embedded-only construction). In child mode both
	// are nil and generateCSS produces user-only output.
	var stylesCSS []byte
	if stylesPath != "" {
		if b, err := os.ReadFile(stylesPath); err == nil {
			stylesCSS = b
		} else {
			stylesCSS = embeddedCSS
		}
	} else {
		stylesCSS = embeddedCSS
	}

	// Re-read the cached basecoat.js on every Reload so a refreshed
	// download between Inits is picked up without a server restart.
	// In child mode (jsPath == "" and embeddedJS == nil) this is skipped.
	var runtimeJS []byte
	if jsPath != "" {
		if b, err := os.ReadFile(jsPath); err == nil {
			runtimeJS = b
		} else {
			runtimeJS = embeddedJS
		}
	} else {
		runtimeJS = embeddedJS
	}

	css, err := generateCSS(ufs, stylesCSS)
	if err != nil {
		return
	}
	js, err := generateJS(ufs, runtimeJS, twBrowserJS)
	if err != nil {
		return
	}
	u.mu.Lock()
	u.cssData = []byte(css)
	u.jsData = []byte(js)
	u.mu.Unlock()
}

// startWatcher wires up the 2-second poll watcher for any sources
// passed via Watch(). AddSource'd sources are not retroactively watched
// — the caller is responsible for triggering Reload for them. Safe to
// call multiple times; later calls are no-ops.
func (u *UnionFS) startWatcher() {
	var watchSources []*watchSource
	for _, src := range u.sources {
		if src.ws != nil {
			watchSources = append(watchSources, src.ws)
		}
	}
	if len(watchSources) == 0 {
		return
	}
	u.watcher = startPollWatcher(watchSources, u.Reload)
}

// Close stops the poll watcher goroutine. Call when the UnionFS is no
// longer needed (e.g. during server shutdown).
func (u *UnionFS) Close() error {
	if u.watcher != nil {
		u.watcher.stop()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Virtual file types — implement fs.File for in-memory content.
// ---------------------------------------------------------------------------

type virtualFile struct {
	name   string
	data   *bytes.Reader
	mod    time.Time
	closed bool
}

func newVirtualFile(name string, data []byte) *virtualFile {
	return &virtualFile{
		name: name,
		data: bytes.NewReader(data),
		mod:  time.Now(),
	}
}

func (f *virtualFile) Stat() (fs.FileInfo, error) {
	if f.closed {
		return nil, fs.ErrClosed
	}
	return &virtualFileInfo{name: f.name, size: int64(f.data.Len()), mod: f.mod}, nil
}

func (f *virtualFile) Read(b []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	return f.data.Read(b)
}

func (f *virtualFile) Close() error {
	f.closed = true
	return nil
}

type virtualFileInfo struct {
	name string
	size int64
	mod  time.Time
}

func (fi *virtualFileInfo) Name() string       { return fi.name }
func (fi *virtualFileInfo) Size() int64        { return fi.size }
func (fi *virtualFileInfo) Mode() fs.FileMode  { return 0444 }
func (fi *virtualFileInfo) ModTime() time.Time { return fi.mod }
func (fi *virtualFileInfo) IsDir() bool        { return false }
func (fi *virtualFileInfo) Sys() interface{}   { return nil }

type virtualDir struct {
	entries []fs.DirEntry
	pos     int
}

func (d *virtualDir) Stat() (fs.FileInfo, error) {
	return &virtualDirInfo{}, nil
}

func (d *virtualDir) Read(b []byte) (int, error) {
	return 0, fs.ErrInvalid
}

func (d *virtualDir) Close() error {
	return nil
}

func (d *virtualDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.pos >= len(d.entries) {
		return nil, io.EOF
	}
	if n <= 0 {
		d.pos = len(d.entries)
		return d.entries, nil
	}
	remain := len(d.entries) - d.pos
	if n > remain {
		n = remain
	}
	slice := d.entries[d.pos : d.pos+n]
	d.pos += n
	return slice, nil
}

type virtualDirInfo struct{}

func (di *virtualDirInfo) Name() string       { return "." }
func (di *virtualDirInfo) Size() int64        { return 0 }
func (di *virtualDirInfo) Mode() fs.FileMode  { return 0555 | fs.ModeDir }
func (di *virtualDirInfo) ModTime() time.Time { return time.Now() }
func (di *virtualDirInfo) IsDir() bool        { return true }
func (di *virtualDirInfo) Sys() interface{}   { return nil }

// dirEntry implements fs.DirEntry for the synthetic directory listings.
// It prefers to wrap a real fs.DirEntry from a source (so IsDir, Type,
// and Info report the truth); when that's not available — only for the
// two virtual files basecoat.css and basecoat.js — it carries a
// pre-built fs.FileInfo instead.
type dirEntry struct {
	entry fs.DirEntry // optional; nil for synthetic entries
	info  fs.FileInfo // required when entry is nil
}

func (e dirEntry) Name() string {
	if e.entry != nil {
		return e.entry.Name()
	}
	return e.info.Name()
}

func (e dirEntry) IsDir() bool {
	if e.entry != nil {
		return e.entry.IsDir()
	}
	return e.info.IsDir()
}

func (e dirEntry) Type() fs.FileMode {
	if e.entry != nil {
		return e.entry.Type()
	}
	return e.info.Mode().Type()
}

func (e dirEntry) Info() (fs.FileInfo, error) {
	if e.entry != nil {
		return e.entry.Info()
	}
	return e.info, nil
}

// watchableRoot looks up the filesystem root path for src in the
// global watchable map (populated by Watch). Returns ("", false) if src
// was not registered via Watch or if src is a type that can't be used
// as a sync.Map key (e.g. fstest.MapFS, which is a Go map). The
// recover guards the latter: sync.Map.Load hashes the key, which
// panics on non-comparable types.
func watchableRoot(src fs.FS) (string, bool) {
	defer func() { _ = recover() }()
	root, ok := watchable.Load(src)
	if !ok {
		return "", false
	}
	return root.(string), true
}

// sourceFS pairs an fs.FS with a name, an optional filesystem root,
// and an optional poll watcher.
type sourceFS struct {
	name string
	fs   fs.FS
	root string
	ws   *watchSource
}
