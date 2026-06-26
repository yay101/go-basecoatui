package basecoat

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

// newTestUnionFS returns a UnionFS with no sources, no embedded JS, and
// no poll watcher. The cache fields stay empty so Reload() produces
// only user content (no basecoat styles or runtime). Suitable for
// testing AddSource/RemoveSource/Reload and Open() in isolation.
func newTestUnionFS() *UnionFS {
	return &UnionFS{
		sources:   nil,
		sourceIdx: make(map[string]sourceRef),
	}
}

func TestUnionFS_AddSource_OpenFindsNewSource(t *testing.T) {
	u := newTestUnionFS()

	child := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<div>hello</div>")},
	}
	u.AddSource("child-1", child)
	u.Reload()

	f, err := u.Open("index.html")
	if err != nil {
		t.Fatalf("Open(index.html) after AddSource: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "<div>hello</div>" {
		t.Errorf("got %q, want %q", data, "<div>hello</div>")
	}
}

func TestUnionFS_RemoveSource_OpenReturnsNotExist(t *testing.T) {
	u := newTestUnionFS()

	onlyHere := fstest.MapFS{
		"only-here.html": &fstest.MapFile{Data: []byte("temporary")},
	}
	u.AddSource("temp", onlyHere)

	if _, err := u.Open("only-here.html"); err != nil {
		t.Fatalf("Open before RemoveSource: %v", err)
	}

	if !u.RemoveSource("temp") {
		t.Fatal("RemoveSource returned false for registered source")
	}

	if _, err := u.Open("only-here.html"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open after RemoveSource: got %v, want fs.ErrNotExist", err)
	}
}

func TestUnionFS_AddSource_ReplacesExisting(t *testing.T) {
	u := newTestUnionFS()

	first := fstest.MapFS{
		"x.txt": &fstest.MapFile{Data: []byte("first")},
	}
	second := fstest.MapFS{
		"x.txt": &fstest.MapFile{Data: []byte("second")},
	}

	u.AddSource("dup", first)
	u.AddSource("dup", second)

	f, err := u.Open("x.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "second" {
		t.Errorf("got %q, want %q (replacement should win)", data, "second")
	}
}

func TestUnionFS_FirstSourceWinsOnConflict(t *testing.T) {
	u := newTestUnionFS()

	a := fstest.MapFS{"shared.txt": &fstest.MapFile{Data: []byte("a")}}
	b := fstest.MapFS{"shared.txt": &fstest.MapFile{Data: []byte("b")}}

	u.AddSource("a", a)
	u.AddSource("b", b)

	f, err := u.Open("shared.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "a" {
		t.Errorf("got %q, want %q (first registered source should win)", data, "a")
	}
}

func TestUnionFS_RemoveSource_UnknownNameReturnsFalse(t *testing.T) {
	u := newTestUnionFS()
	if u.RemoveSource("nope") {
		t.Error("RemoveSource for unknown name should return false")
	}
}

func TestUnionFS_Reload_UpdatesVirtualCSS(t *testing.T) {
	u := newTestUnionFS()

	src := fstest.MapFS{
		"basecoat/css/app.css": &fstest.MapFile{Data: []byte(`.btn{padding:1rem;color:red;}`)},
		"basecoat/js/app.js":   &fstest.MapFile{Data: []byte(`basecoat.register('x','#x',function(el){});`)},
	}

	u.Reload()
	cssBefore, _ := readVirtual(t, u, "basecoat.css")
	jsBefore, _ := readVirtual(t, u, "basecoat.js")

	u.AddSource("html", src)
	u.Reload()

	cssAfter, _ := readVirtual(t, u, "basecoat.css")
	jsAfter, _ := readVirtual(t, u, "basecoat.js")

	if bytes.Equal(cssBefore, cssAfter) {
		t.Error("basecoat.css did not change after AddSource + Reload")
	}
	if bytes.Equal(jsBefore, jsAfter) {
		t.Error("basecoat.js did not change after AddSource + Reload")
	}

	// Remove the source and Reload again — the .btn rule should drop
	// out of the CSS and the app.js out of basecoat.js.
	u.RemoveSource("html")
	u.Reload()

	cssAfter2, _ := readVirtual(t, u, "basecoat.css")
	jsAfter2, _ := readVirtual(t, u, "basecoat.js")

	if bytes.Equal(cssAfter, cssAfter2) {
		t.Error("basecoat.css did not change after RemoveSource + Reload")
	}
	if bytes.Equal(jsAfter, jsAfter2) {
		t.Error("basecoat.js did not change after RemoveSource + Reload")
	}
}

func TestUnionFS_AddRemove_PreservesOrderOfRemaining(t *testing.T) {
	u := newTestUnionFS()

	mk := func(name string) fs.FS {
		return fstest.MapFS{
			"file.txt": &fstest.MapFile{Data: []byte(name)},
		}
	}

	u.AddSource("a", mk("a"))
	u.AddSource("b", mk("b"))
	u.AddSource("c", mk("c"))
	u.RemoveSource("b")

	// After removing "b", order should be [a, c]. Open shared file
	// should hit "a" (first wins).
	f, err := u.Open("file.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "a" {
		t.Errorf("got %q, want a (order should be [a, c] after removing b)", data)
	}

	// Now add "d" and confirm it lands at the end, not before a or c.
	u.AddSource("d", mk("d"))
	f, _ = u.Open("file.txt")
	data, _ = io.ReadAll(f)
	f.Close()
	if string(data) != "a" {
		t.Errorf("after AddSource d, got %q, want a (a should still be first)", data)
	}
}

func TestUnionFS_ConcurrentAddOpenReload(t *testing.T) {
	u := newTestUnionFS()

	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			fs := fstest.MapFS{
				"file.txt": &fstest.MapFile{Data: []byte(fmt.Sprintf("src-%d", n))},
			}
			u.AddSource(fmt.Sprintf("src-%d", n), fs)
		}(i)
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = u.Open("file.txt")
			_, _ = u.Open("basecoat.css")
			_, _ = u.Open(".")
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u.Reload()
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			u.RemoveSource(fmt.Sprintf("src-%d", n))
		}(i)
	}

	wg.Wait()
}

func readVirtual(t *testing.T, u *UnionFS, name string) ([]byte, error) {
	t.Helper()
	f, err := u.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func TestUnionFS_ReadDir_RootMergesAllSources(t *testing.T) {
	u := newTestUnionFS()

	u.AddSource("a", fstest.MapFS{
		"x.txt": &fstest.MapFile{Data: []byte("a")},
		"y.txt": &fstest.MapFile{Data: []byte("a")},
	})
	u.AddSource("b", fstest.MapFS{
		"x.txt":    &fstest.MapFile{Data: []byte("b")},
		"shared/":  nil,
		"z.txt":    &fstest.MapFile{Data: []byte("b")},
		"shared/q": &fstest.MapFile{Data: []byte("q")},
	})

	entries, err := u.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"basecoat.css", "basecoat.js", "x.txt", "y.txt", "z.txt", "shared"} {
		if !strings.Contains(got, want) {
			t.Errorf("ReadDir(.) missing %q; got %q", want, got)
		}
	}
}

func TestUnionFS_ReadDir_PreservesIsDirFromSources(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"file.txt": &fstest.MapFile{Data: []byte("a")},
		"sub/":     nil,
	})

	entries, err := u.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	want := map[string]bool{
		"file.txt":     false,
		"sub":          true,
		"basecoat.css": false,
		"basecoat.js":  false,
	}
	for _, e := range entries {
		wantIsDir, ok := want[e.Name()]
		if !ok {
			t.Errorf("unexpected entry %q", e.Name())
			continue
		}
		if e.IsDir() != wantIsDir {
			t.Errorf("IsDir(%q) = %v, want %v", e.Name(), e.IsDir(), wantIsDir)
		}
		info, err := e.Info()
		if err != nil {
			t.Errorf("Info(%q): %v", e.Name(), err)
			continue
		}
		if info.IsDir() != wantIsDir {
			t.Errorf("Info(%q).IsDir() = %v, want %v", e.Name(), info.IsDir(), wantIsDir)
		}
	}
}

func TestUnionFS_ReadDir_EmptyDirIsNotErrNotExist(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"empty/": &fstest.MapFile{Mode: fs.ModeDir},
	})

	entries, err := u.ReadDir("empty")
	if err != nil {
		t.Fatalf("ReadDir(empty): got %v, want nil (empty dir is not missing)", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(empty): got %d entries, want 0", len(entries))
	}
}

func TestUnionFS_ReadDir_MasksBasecoatNamespace(t *testing.T) {
	u := newTestUnionFS()

	u.AddSource("a", fstest.MapFS{
		"basecoat/foo.html":       &fstest.MapFile{Data: []byte("user file in reserved namespace")},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte("fragment")},
		"index.html":              &fstest.MapFile{Data: []byte("<html></html>")},
	})
	u.Reload()

	// Open of the reserved directory and any path under it returns NotExist.
	if _, err := u.Open("basecoat"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(basecoat): got %v, want fs.ErrNotExist", err)
	}
	if _, err := u.Open("basecoat/foo.html"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(basecoat/foo.html): got %v, want fs.ErrNotExist", err)
	}
	if _, err := u.Open("basecoat/html/frag.html"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(basecoat/html/frag.html): got %v, want fs.ErrNotExist", err)
	}

	// ReadDir("basecoat") and ReadDir("basecoat/html") also NotExist.
	if _, err := u.ReadDir("basecoat"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadDir(basecoat): got %v, want fs.ErrNotExist", err)
	}
	if _, err := u.ReadDir("basecoat/html"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadDir(basecoat/html): got %v, want fs.ErrNotExist", err)
	}

	// Stat of the reserved path also NotExist.
	if _, err := u.Stat("basecoat"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(basecoat): got %v, want fs.ErrNotExist", err)
	}
	if _, err := u.Stat("basecoat/foo.html"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(basecoat/foo.html): got %v, want fs.ErrNotExist", err)
	}

	// But the virtual basecoat.css / basecoat.js still resolve, and the
	// merged root listing must not expose the masked "basecoat" entry.
	entries, err := u.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	for _, e := range entries {
		if e.Name() == "basecoat" {
			t.Errorf("ReadDir(.) leaked reserved entry 'basecoat'")
		}
	}
	if _, err := u.Open("basecoat.css"); err != nil {
		t.Errorf("Open(basecoat.css): %v", err)
	}
	if _, err := u.Open("basecoat.js"); err != nil {
		t.Errorf("Open(basecoat.js): %v", err)
	}
}

func TestUnionFS_Stat_VirtualFiles(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("html", fstest.MapFS{
		"basecoat/css/app.css": &fstest.MapFile{Data: []byte(`.btn{padding:1rem;color:red;}`)},
		"basecoat/js/app.js":   &fstest.MapFile{Data: []byte(`basecoat.register('x','#x',function(){});`)},
	})
	u.Reload()

	info, err := u.Stat("basecoat.css")
	if err != nil {
		t.Fatalf("Stat(basecoat.css): %v", err)
	}
	if info.IsDir() {
		t.Errorf("Stat(basecoat.css).IsDir() = true, want false")
	}
	if info.Name() != "basecoat.css" {
		t.Errorf("Stat(basecoat.css).Name() = %q, want basecoat.css", info.Name())
	}

	info, err = u.Stat("basecoat.js")
	if err != nil {
		t.Fatalf("Stat(basecoat.js): %v", err)
	}
	if info.IsDir() {
		t.Errorf("Stat(basecoat.js).IsDir() = true, want false")
	}
	if info.Name() != "basecoat.js" {
		t.Errorf("Stat(basecoat.js).Name() = %q, want basecoat.js", info.Name())
	}

	info, err = u.Stat(".")
	if err != nil {
		t.Fatalf("Stat(.): %v", err)
	}
	if !info.IsDir() {
		t.Errorf("Stat(.).IsDir() = false, want true")
	}
}

func TestUnionFS_Stat_DelegatesToSources(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("hello")},
	})

	info, err := u.Stat("hello.txt")
	if err != nil {
		t.Fatalf("Stat(hello.txt): %v", err)
	}
	if info.Name() != "hello.txt" {
		t.Errorf("Stat(hello.txt).Name() = %q, want hello.txt", info.Name())
	}
	if info.IsDir() {
		t.Errorf("Stat(hello.txt).IsDir() = true, want false")
	}

	if _, err := u.Stat("nope.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(nope.txt): got %v, want fs.ErrNotExist", err)
	}
}

// ---------------------------------------------------------------------------
// Generation tests: parent mode (styles + runtime prefix), child mode
// (user content only).
// ---------------------------------------------------------------------------

// newTestParentFS builds a UnionFS in parent mode wired up with a
// fake styles.css path and a fake runtime path. Avoids the network.
func newTestParentFS(sources []fs.FS, stylesCSS, runtimeJS string) *UnionFS {
	u := newUnionFS(sources, stylesCSS, runtimeJS, nil, "")
	u.Reload()
	return u
}

// newTestChildFS builds a UnionFS in child mode (no parent assets).
func newTestChildFS(sources []fs.FS) *UnionFS {
	u := newUnionFS(sources, "", "", nil, "")
	u.Reload()
	return u
}

// writeTempFile writes content to a temp file and returns the path.
// Used to fake a downloaded styles.css / basecoat.js for the
// generation tests.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "basecoat-test-*.css")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestGenerateCSS_Parent_IncludesStylesAndUserCSS(t *testing.T) {
	styles := writeTempFile(t, ".from-styles{padding:1rem;}")
	js := writeTempFile(t, "// runtime")

	u := newTestParentFS([]fs.FS{
		fstest.MapFS{
			"basecoat/css/a.css": &fstest.MapFile{Data: []byte(".a{padding:2rem;}")},
			"basecoat/css/b.css": &fstest.MapFile{Data: []byte(".b{padding:3rem;}")},
		},
	}, styles, js)

	css, err := readVirtual(t, u, "basecoat.css")
	if err != nil {
		t.Fatalf("readVirtual: %v", err)
	}
	got := string(css)
	for _, want := range []string{".from-styles", ".a", ".b"} {
		if !strings.Contains(got, want) {
			t.Errorf("parent basecoat.css missing %q; got: %s", want, got)
		}
	}
}

func TestGenerateCSS_Child_OnlyUserCSS(t *testing.T) {
	u := newTestChildFS([]fs.FS{
		fstest.MapFS{
			"basecoat/css/a.css": &fstest.MapFile{Data: []byte(".a{padding:2rem;}")},
			"basecoat/css/b.css": &fstest.MapFile{Data: []byte(".b{padding:3rem;}")},
		},
	})

	css, err := readVirtual(t, u, "basecoat.css")
	if err != nil {
		t.Fatalf("readVirtual: %v", err)
	}
	got := string(css)
	for _, want := range []string{".a", ".b"} {
		if !strings.Contains(got, want) {
			t.Errorf("child basecoat.css missing %q; got: %s", want, got)
		}
	}
}

func TestGenerateJS_Parent_IncludesRuntimeAndUserJS(t *testing.T) {
	styles := writeTempFile(t, ".x{}")
	// Marker has to survive the JS minifier, so we use a real
	// identifier and a string literal — the minifier only strips
	// comments and whitespace.
	js := writeTempFile(t, `var RUNTIME_MARKER = 1;`)

	u := newTestParentFS([]fs.FS{
		fstest.MapFS{
			"basecoat/js/a.js": &fstest.MapFile{Data: []byte("basecoat.register('a','.a',function(){});")},
		},
	}, styles, js)

	body, err := readVirtual(t, u, "basecoat.js")
	if err != nil {
		t.Fatalf("readVirtual: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "RUNTIME_MARKER") {
		t.Errorf("parent basecoat.js missing runtime prefix; got: %s", got)
	}
	if !strings.Contains(got, "basecoat.register('a'") {
		t.Errorf("parent basecoat.js missing user JS; got: %s", got)
	}
}

func TestGenerateJS_Child_OnlyUserJS(t *testing.T) {
	u := newTestChildFS([]fs.FS{
		fstest.MapFS{
			"basecoat/js/a.js": &fstest.MapFile{Data: []byte("basecoat.register('a','.a',function(){});")},
		},
	})

	body, err := readVirtual(t, u, "basecoat.js")
	if err != nil {
		t.Fatalf("readVirtual: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "basecoat.register('a'") {
		t.Errorf("child basecoat.js missing user JS; got: %s", got)
	}
}

func TestGenerateJS_EmbeddedFallbackWhenJSDownloadFails(t *testing.T) {
	// Parent mode with a non-existent runtime path: should fall back
	// to the embedded //go:embed bytes.
	u := newTestParentFS([]fs.FS{
		fstest.MapFS{
			"basecoat/js/a.js": &fstest.MapFile{Data: []byte("basecoat.register('a','.a',function(){});")},
		},
	}, writeTempFile(t, ".x{}"), "/nonexistent/runtime.js")
	u.embeddedJS = embeddedBasecoatJS
	u.basecoatJSPath = ""
	u.Reload()

	body, err := readVirtual(t, u, "basecoat.js")
	if err != nil {
		t.Fatalf("readVirtual: %v", err)
	}
	if !strings.Contains(string(body), "basecoat.register('a'") {
		t.Errorf("fallback basecoat.js missing user JS; got: %s", body)
	}
	if len(body) <= len("basecoat.register('a','.a',function(){});") {
		t.Errorf("fallback basecoat.js too short — embedded fallback may be missing; got %d bytes", len(body))
	}
}

// ---------------------------------------------------------------------------
// ensureBasecoatJS: download round-trip with a fake CDN.
// ---------------------------------------------------------------------------

func TestEnsureBasecoatJS_DownloadsAndCaches(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte("/* runtime v1 */"))
	}))
	defer srv.Close()

	// Temporarily redirect the package-level URL to our test server.
	orig := basecoatJSURL
	basecoatJSURL = srv.URL
	t.Cleanup(func() { basecoatJSURL = orig })

	cacheDir := t.TempDir()
	path, data, err := ensureBasecoatJS(cacheDir)
	if err != nil {
		t.Fatalf("ensureBasecoatJS: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path on successful download")
	}
	if string(data) != "/* runtime v1 */" {
		t.Errorf("got %q, want %q", data, "/* runtime v1 */")
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("server hits: got %d, want 1", hits)
	}

	// Cache file should exist on disk.
	cached, err := os.ReadFile(filepath.Join(cacheDir, "basecoat", "basecoat.js"))
	if err != nil {
		t.Fatalf("read cached: %v", err)
	}
	if string(cached) != "/* runtime v1 */" {
		t.Errorf("cached: got %q, want %q", cached, "/* runtime v1 */")
	}
}

func TestEnsureBasecoatJS_ServesCacheWhenCDNDown(t *testing.T) {
	// Pre-populate the cache.
	cacheDir := t.TempDir()
	cached := []byte("/* cached runtime */")
	if err := os.MkdirAll(filepath.Join(cacheDir, "basecoat"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "basecoat", "basecoat.js"), cached, 0644); err != nil {
		t.Fatal(err)
	}

	// Point the URL at a server that always 500s.
	orig := basecoatJSURL
	basecoatJSURL = "http://127.0.0.1:1/always-down" // closed port
	t.Cleanup(func() { basecoatJSURL = orig })

	path, data, err := ensureBasecoatJS(cacheDir)
	if err != nil {
		t.Fatalf("expected cache fallback to succeed; got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path when cache exists")
	}
	if string(data) != string(cached) {
		t.Errorf("got %q, want %q (cached bytes)", data, cached)
	}
}

func TestEnsureBasecoatJS_ErrorsWhenNoCacheAndCDNDown(t *testing.T) {
	orig := basecoatJSURL
	basecoatJSURL = "http://127.0.0.1:1/always-down"
	t.Cleanup(func() { basecoatJSURL = orig })

	_, _, err := ensureBasecoatJS(t.TempDir())
	if err == nil {
		t.Error("expected error when network is down and no cache exists")
	}
}
