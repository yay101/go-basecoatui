package basecoat

import (
	"bytes"
	"html/template"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"
)

// Compile-time checks that UnionFS implements the standard fs interfaces
// the FS interface in basecoat.go promises.
var (
	_ fs.FS        = (*UnionFS)(nil)
	_ fs.ReadDirFS = (*UnionFS)(nil)
	_ fs.StatFS    = (*UnionFS)(nil)
)

// UnionFS implements fs.FS by layering multiple source filesystems and
// injecting two virtual files — basecoat.css and basecoat.js — that are
// regenerated whenever source content changes.
//
// Virtual files:
//   - basecoat.css  minified, tree-shaken combination of basecoat CSS
//     plus every basecoat/css/**/*.css across all sources
//   - basecoat.js   embedded basecoat runtime plus every
//     basecoat/js/**/*.js across all sources
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
	mu           sync.RWMutex
	sources      []sourceFS
	assetSources []sourceFS
	sourceIdx    map[string]sourceRef
	cssData      []byte
	jsData       []byte
	cachePath    string
	basecoatPath string
	resolvedVer  *resolvedVersion
	embeddedJS   []byte
	watcher      *pollWatcher
	static       bool

	// Template cache. Reload() invalidates by bumping templateGen;
	// templateWith reads it under the read lock to decide whether to
	// reuse a cached *template.Template. The cache is keyed by the
	// joined match list; each entry also remembers the funcs map
	// pointer (via reflect) so two callers with different FuncMaps
	// for the same match don't reuse each other's template.
	templateGen uint64
	tmplCache   map[string]*tmplCacheEntry
}

// tmplCacheEntry is one cached *template.Template plus the identity of
// the funcs map it was parsed with.
type tmplCacheEntry struct {
	gen      uint64
	funcsPtr uintptr
	funcsNil bool
	tmpl     *template.Template
	parseErr error
}

// sourceRef points at a sourceFS in either u.sources or u.assetSources.
// The asset flag tells RemoveSource which slice to splice.
type sourceRef struct {
	asset bool
	index int
}

// masked reports whether name falls inside the reserved basecoat/
// namespace and must not resolve to a user file.
func masked(name string) bool {
	return name == "basecoat" || strings.HasPrefix(name, "basecoat/")
}

// Open implements fs.FS. It handles the two virtual paths specially,
// rejects anything inside the reserved basecoat/ namespace, and
// delegates everything else to the underlying source filesystems.
func (u *UnionFS) Open(name string) (fs.File, error) {
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
	if masked(name) {
		return nil, fs.ErrNotExist
	}
	u.mu.RLock()
	sources := u.sources
	u.mu.RUnlock()
	for _, src := range sources {
		f, err := src.fs.Open(name)
		if err == nil {
			return f, nil
		}
	}
	if name == "." {
		return u.openRootDir()
	}
	return nil, fs.ErrNotExist
}

// openRootDir builds a merged directory listing from all sources plus
// the two virtual file entries, masking any source entry that falls
// inside the reserved basecoat/ namespace.
func (u *UnionFS) openRootDir() (fs.File, error) {
	u.mu.RLock()
	sources := u.sources
	u.mu.RUnlock()

	entries := mergeDirEntries(sources, ".", true)
	// Append the two virtual files. Use a synthetic dirEntry with
	// pre-built FileInfo so Stat-via-Info returns the right size.
	entries = append(entries,
		dirEntry{info: &virtualFileInfo{name: "basecoat.css", size: int64(len(u.cssData)), mod: time.Now()}},
		dirEntry{info: &virtualFileInfo{name: "basecoat.js", size: int64(len(u.jsData)), mod: time.Now()}},
	)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return &virtualDir{entries: entries}, nil
}

// mergeDirEntries enumerates name across every source (first-match-wins
// on entry name) and returns the union. When mask is true, entries that
// fall inside the reserved basecoat/ namespace are dropped. The boolean
// reports whether any source confirmed the path is a directory (used by
// ReadDir to distinguish "dir exists but is empty" from "no such dir").
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
	if name == "." {
		f, err := u.openRootDir()
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
	if masked(name) {
		return nil, fs.ErrNotExist
	}
	u.mu.RLock()
	sources := u.sources
	u.mu.RUnlock()

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
	if masked(name) {
		return nil, fs.ErrNotExist
	}
	f, err := u.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// AddSource registers src under name as a full source: its files are
// served through Open/ReadDir/Stat AND contribute to generation
// (CSS/JS output, class extraction, template fragments). Replaces any
// existing source (full or asset) with the same name. Order of
// registration is preserved for first-match-wins semantics across
// Open() calls. Does not auto-reload — call Reload when the set of
// sources has settled.
//
// If src was returned by Dir() the underlying root path is tracked so
// the poll watcher can poll it, but the poll watcher (if any) is not
// retroactively rewired: the watcher was started with the initial
// sources only. The parent is responsible for triggering Reload on
// external changes for AddSource'd entries.
func (u *UnionFS) AddSource(name string, src fs.FS) {
	u.mu.Lock()
	defer u.mu.Unlock()

	sf := sourceFS{name: name, fs: src, asset: false}
	if root, ok := watchableRoot(src); ok {
		sf.root = root
		sf.ws = newWatchSource(sf.root)
	}

	if ref, exists := u.sourceIdx[name]; exists {
		// Replace in whichever slice the name currently lives in.
		// If the kind is changing (asset -> full), we need to move
		// the entry between slices.
		if ref.asset {
			// Was an asset source; promote to full source.
			u.assetSources = append(u.assetSources[:ref.index], u.assetSources[ref.index+1:]...)
			u.reindexAsset(ref.index)
			u.sources = append(u.sources, sf)
			u.sourceIdx[name] = sourceRef{asset: false, index: len(u.sources) - 1}
			return
		}
		u.sources[ref.index] = sf
		return
	}

	u.sources = append(u.sources, sf)
	if u.sourceIdx == nil {
		u.sourceIdx = make(map[string]sourceRef)
	}
	u.sourceIdx[name] = sourceRef{asset: false, index: len(u.sources) - 1}
}

// AddAssetSource registers src under name as an asset-only source: its
// basecoat/css/**, basecoat/js/**, basecoat/html/**, and any *.html
// files contribute to generation (CSS/JS output, class extraction,
// template fragments) exactly like a full source, but the source's
// files are NOT served through Open/ReadDir/Stat. Use this for child
// services that ship their CSS/JS/fragments to a parent but serve their
// own pages via their own mux prefix.
//
// Replaces any existing source (full or asset) with the same name.
// Not poll-watched — the caller must call Reload after changes.
func (u *UnionFS) AddAssetSource(name string, src fs.FS) {
	u.mu.Lock()
	defer u.mu.Unlock()

	sf := sourceFS{name: name, fs: src, asset: true}

	if ref, exists := u.sourceIdx[name]; exists {
		if !ref.asset {
			// Was a full source; demote to asset source.
			u.sources = append(u.sources[:ref.index], u.sources[ref.index+1:]...)
			u.reindexFull(ref.index)
			u.assetSources = append(u.assetSources, sf)
			u.sourceIdx[name] = sourceRef{asset: true, index: len(u.assetSources) - 1}
			return
		}
		u.assetSources[ref.index] = sf
		return
	}

	u.assetSources = append(u.assetSources, sf)
	if u.sourceIdx == nil {
		u.sourceIdx = make(map[string]sourceRef)
	}
	u.sourceIdx[name] = sourceRef{asset: true, index: len(u.assetSources) - 1}
}

// RemoveSource drops the source (full or asset) with the given name.
// Returns false if no such source was registered. Does not auto-reload
// — call Reload to regenerate basecoat.css and basecoat.js without the
// removed source. Order of the remaining sources in either slice is
// preserved.
func (u *UnionFS) RemoveSource(name string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	ref, ok := u.sourceIdx[name]
	if !ok {
		return false
	}

	if ref.asset {
		u.assetSources = append(u.assetSources[:ref.index], u.assetSources[ref.index+1:]...)
		u.reindexAsset(ref.index)
	} else {
		u.sources = append(u.sources[:ref.index], u.sources[ref.index+1:]...)
		u.reindexFull(ref.index)
	}
	delete(u.sourceIdx, name)
	return true
}

// reindexFull rebuilds the sourceIdx entries for u.sources starting at
// the given offset (used after a splice that shifts later entries).
func (u *UnionFS) reindexFull(from int) {
	for j := from; j < len(u.sources); j++ {
		u.sourceIdx[u.sources[j].name] = sourceRef{asset: false, index: j}
	}
}

// reindexAsset rebuilds the sourceIdx entries for u.assetSources
// starting at the given offset.
func (u *UnionFS) reindexAsset(from int) {
	for j := from; j < len(u.assetSources); j++ {
		u.sourceIdx[u.assetSources[j].name] = sourceRef{asset: true, index: j}
	}
}

// Reload rebuilds basecoat.css and basecoat.js from the current set of
// sources. Atomic with respect to Open() — readers see the previous or
// next version, never a half-built one. Safe to call concurrently and
// safe to call from inside the poll watcher callback.
//
// Reload also invalidates the template cache: the next Template /
// TemplateFuncs call re-parses. The poll watcher calls Reload on file
// changes, so templates stay fresh without callers doing their own
// invalidation.
func (u *UnionFS) Reload() {
	u.mu.RLock()
	sources := make([]sourceFS, len(u.sources))
	copy(sources, u.sources)
	assetSources := make([]sourceFS, len(u.assetSources))
	copy(assetSources, u.assetSources)
	u.mu.RUnlock()

	used := extractUsedClasses(sources, assetSources)
	css, err := generateCSS(sources, assetSources, u.basecoatPath, used)
	if err != nil {
		return
	}
	js, err := generateJS(sources, assetSources, u.embeddedJS)
	if err != nil {
		return
	}
	u.mu.Lock()
	u.cssData = []byte(css)
	u.jsData = []byte(js)
	u.templateGen++
	u.tmplCache = nil
	u.mu.Unlock()
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
// global watchable map (populated by Dir). Returns ("", false) if src
// was not registered via Dir or if src is a type that can't be used
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
// and an optional poll watcher. The asset flag records whether this
// entry lives in assetSources (contributes to generation only) or in
// sources (also served through Open/ReadDir/Stat).
type sourceFS struct {
	name  string
	fs    fs.FS
	root  string
	ws    *watchSource
	asset bool
}
