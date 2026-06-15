package basecoat

import (
	"bytes"
	"io"
	"io/fs"
	"sort"
	"sync"
	"time"
)

// Compile-time check that UnionFS implements fs.FS.
var _ fs.FS = (*UnionFS)(nil)

// UnionFS implements fs.FS by layering multiple source filesystems and
// injecting two virtual files — basecoat.css and basecoat.js — that are
// regenerated whenever source content changes.
//
// Virtual files:
//   - basecoat.css  minified, tree-shaken combination of Tailwind CSS,
//                    basecoat CSS, and all /css/*.css from sources
//   - basecoat.js   embedded basecoat runtime + all /js/*.js from sources
//
// Non-virtual paths are resolved by searching sources in order and
// returning the first match (classic overlay behaviour).
type UnionFS struct {
	mu           sync.RWMutex
	sources      []sourceFS
	cssData      []byte
	jsData       []byte
	cachePath    string
	basecoatPath string
	resolvedVer  *resolvedVersion
	embeddedJS   []byte
	watcher      *pollWatcher
	static       bool
}

// Open implements fs.FS. It handles the two virtual paths specially and
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
	for _, src := range u.sources {
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
// the two virtual file entries.
func (u *UnionFS) openRootDir() (fs.File, error) {
	var entries []string
	for _, src := range u.sources {
		f, err := src.fs.Open(".")
		if err != nil {
			continue
		}
		dir, ok := f.(fs.ReadDirFile)
		if ok {
			list, _ := dir.ReadDir(-1)
			for _, e := range list {
				entries = append(entries, e.Name())
			}
		}
		f.Close()
	}
	entries = append(entries, "basecoat.css", "basecoat.js")
	sort.Strings(entries)
	entries = unique(entries)

	var dirs []fs.DirEntry
	for _, name := range entries {
		dirs = append(dirs, dirEntry{name: name})
	}
	return &virtualDir{entries: dirs}, nil
}

// regenerate re-scans all sources and rebuilds basecoat.css and basecoat.js.
// It is called once during Init and then whenever the poll watcher detects
// a file change. The result is swapped atomically under a write lock.
func (u *UnionFS) regenerate() {
	used := extractUsedClasses(u.sources)
	css, err := generateCSS(u.sources, u.basecoatPath, used)
	if err != nil {
		return
	}
	js, err := generateJS(u.sources, u.embeddedJS)
	if err != nil {
		return
	}
	u.mu.Lock()
	u.cssData = []byte(css)
	u.jsData = []byte(js)
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

func (di *virtualDirInfo) Name() string        { return "." }
func (di *virtualDirInfo) Size() int64         { return 0 }
func (di *virtualDirInfo) Mode() fs.FileMode   { return 0555 | fs.ModeDir }
func (di *virtualDirInfo) ModTime() time.Time  { return time.Now() }
func (di *virtualDirInfo) IsDir() bool         { return true }
func (di *virtualDirInfo) Sys() interface{}    { return nil }

// dirEntry implements fs.DirEntry for the synthetic root directory listing.
type dirEntry struct {
	name string
}

func (e dirEntry) Name() string               { return e.name }
func (e dirEntry) IsDir() bool                { return false }
func (e dirEntry) Type() fs.FileMode          { return 0444 }
func (e dirEntry) Info() (fs.FileInfo, error) {
	return &virtualFileInfo{name: e.name, size: 0, mod: time.Now()}, nil
}

// unique deduplicates a string slice while preserving order.
func unique(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// sourceFS pairs an fs.FS with an optional filesystem root and poll watcher.
type sourceFS struct {
	fs   fs.FS
	root string
	ws   *watchSource
}
