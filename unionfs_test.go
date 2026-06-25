package basecoat

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// newTestUnionFS returns a UnionFS with no sources, no embedded JS, and
// no poll watcher. The cache fields stay empty so Reload() falls back
// to user CSS only. Suitable for testing AddSource/RemoveSource/Reload
// and Open() in isolation, without hitting the network for basecoat
// downloads.
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

	// A source with both user CSS (rules the tree-shaker can keep) and
	// matching HTML, plus a JS file. Reloading after AddSource should
	// pull the .btn rule into basecoat.css and the app.js into
	// basecoat.js.
	src := fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte(`<div class="btn">x</div>`)},
		"basecoat/css/app.css": &fstest.MapFile{Data: []byte(`.btn{padding:1rem;color:red;}.unused{padding:2rem;}`)},
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
	// out of the tree-shaken CSS and the app.js out of basecoat.js.
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
		"index.html":           &fstest.MapFile{Data: []byte(`<div class="btn">x</div>`)},
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

func TestUnionFS_Template_ParsesFragmentsAndPage(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<h1>{{template "greet" .Name}}</h1>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "greet"}}Hello, {{.}}!{{end}}`)},
	})

	tmpl, err := u.Template("index.html")
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ Name string }{"world"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>Hello, world!</h1>" {
		t.Errorf("got %q, want %q", got, "<h1>Hello, world!</h1>")
	}
}

func TestUnionFS_Template_FragmentsAreRecursive(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"index.html":                          &fstest.MapFile{Data: []byte(`<p>{{template "x" .}}</p>`)},
		"basecoat/html/nested/deep/frag.html": &fstest.MapFile{Data: []byte(`{{define "x"}}deep{{end}}`)},
	})

	tmpl, err := u.Template("index.html")
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<p>deep</p>" {
		t.Errorf("got %q, want %q", got, "<p>deep</p>")
	}
}

func TestUnionFS_Template_NoFragmentsStillWorks(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<p>plain {{.}}</p>`)},
	})

	tmpl, err := u.Template("index.html")
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, "ok"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<p>plain ok</p>" {
		t.Errorf("got %q, want %q", got, "<p>plain ok</p>")
	}
}

func TestUnionFS_Template_CrossSourceFragments(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("page", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<h1>{{template "greet" .Name}}</h1>`)},
	})
	u.AddSource("frags", fstest.MapFS{
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "greet"}}Hello, {{.}}!{{end}}`)},
	})

	tmpl, err := u.Template("index.html")
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ Name string }{"across"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>Hello, across!</h1>" {
		t.Errorf("got %q, want %q", got, "<h1>Hello, across!</h1>")
	}
}

// countFS wraps an fs.FS and counts Open calls. Used to verify the
// template cache short-circuits re-parsing.
type countFS struct {
	fs.FS
	opens int
}

func (c *countFS) Open(name string) (fs.File, error) {
	c.opens++
	return c.FS.Open(name)
}

func TestUnionFS_Template_CacheHitOnRepeatCall(t *testing.T) {
	u := newTestUnionFS()
	inner := fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<p>{{.}}</p>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "x"}}X{{end}}`)},
	}
	c := &countFS{FS: inner}
	u.AddSource("a", c)

	t1, err := u.Template("index.html")
	if err != nil {
		t.Fatalf("first Template: %v", err)
	}
	opensAfterFirst := c.opens
	if opensAfterFirst == 0 {
		t.Fatal("first Template did not open any files")
	}

	t2, err := u.Template("index.html")
	if err != nil {
		t.Fatalf("second Template: %v", err)
	}
	if c.opens != opensAfterFirst {
		t.Errorf("second Template re-read files: opens went from %d to %d (cache miss)", opensAfterFirst, c.opens)
	}
	if t1 != t2 {
		t.Error("second Template returned a different *Template; cache should return the same pointer")
	}
}

func TestUnionFS_Template_CacheInvalidatedByReload(t *testing.T) {
	u := newTestUnionFS()
	c := &countFS{FS: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<p>{{.}}</p>`)},
	}}
	u.AddSource("a", c)

	if _, err := u.Template("index.html"); err != nil {
		t.Fatalf("first Template: %v", err)
	}
	opensAfterFirst := c.opens

	u.Reload()

	if _, err := u.Template("index.html"); err != nil {
		t.Fatalf("post-Reload Template: %v", err)
	}
	if c.opens == opensAfterFirst {
		t.Error("Reload did not invalidate the template cache: no re-read happened")
	}
}

func TestUnionFS_TemplateFuncs_CacheKeyedByFuncsIdentity(t *testing.T) {
	u := newTestUnionFS()
	c := &countFS{FS: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<p>{{upper .}}</p>`)},
	}}
	u.AddSource("a", c)

	funcsA := template.FuncMap{"upper": strings.ToUpper}

	tA, err := u.TemplateFuncs(funcsA, "index.html")
	if err != nil {
		t.Fatalf("TemplateFuncs A: %v", err)
	}
	opensAfterA := c.opens

	// Same funcs pointer → cache hit.
	tA2, err := u.TemplateFuncs(funcsA, "index.html")
	if err != nil {
		t.Fatalf("TemplateFuncs A repeat: %v", err)
	}
	if c.opens != opensAfterA {
		t.Errorf("same FuncMap re-read files: %d -> %d (expected cache hit)", opensAfterA, c.opens)
	}
	if tA != tA2 {
		t.Error("same FuncMap returned a different *Template")
	}

	// Different funcs pointer → cache miss.
	funcsB := template.FuncMap{"upper": strings.ToUpper}
	if _, err := u.TemplateFuncs(funcsB, "index.html"); err != nil {
		t.Fatalf("TemplateFuncs B: %v", err)
	}
	if c.opens == opensAfterA {
		t.Error("different FuncMap did not cause a cache miss")
	}
}

func TestUnionFS_Template_CachesParseErrorUntilReload(t *testing.T) {
	u := newTestUnionFS()
	c := &countFS{FS: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`{{define "x"}}`)}, // broken: unclosed define
	}}
	u.AddSource("a", c)

	if _, err := u.Template("index.html"); err == nil {
		t.Fatal("expected parse error on broken template")
	}
	opensAfterFirst := c.opens

	// Second call should hit the cached error, not re-parse.
	if _, err := u.Template("index.html"); err == nil {
		t.Fatal("expected cached parse error on second call")
	}
	if c.opens != opensAfterFirst {
		t.Errorf("broken template was re-parsed: %d -> %d (expected cached error)", opensAfterFirst, c.opens)
	}
}

// ---------------------------------------------------------------------------
// AddAssetSource — asset-only sources contribute to generation but
// are invisible to Open / ReadDir / Stat.
// ---------------------------------------------------------------------------

func TestUnionFS_AddAssetSource_InvisibleToOpenReadDirStat(t *testing.T) {
	u := newTestUnionFS()
	u.AddAssetSource("child", fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<div class="btn">x</div>`)},
		"basecoat/css/app.css":    &fstest.MapFile{Data: []byte(`.btn{color:red}`)},
		"basecoat/js/app.js":      &fstest.MapFile{Data: []byte(`basecoat.register('x','#x',function(){});`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "x"}}X{{end}}`)},
	})
	u.Reload()

	// The asset source's files are not reachable through the FS API.
	if _, err := u.Open("index.html"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(index.html): got %v, want fs.ErrNotExist (asset source not served)", err)
	}
	if _, err := u.Stat("index.html"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(index.html): got %v, want fs.ErrNotExist", err)
	}
	entries, err := u.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	for _, e := range entries {
		if e.Name() == "index.html" {
			t.Error("ReadDir(.) leaked asset source file 'index.html'")
		}
	}

	// The two virtual files still resolve.
	if _, err := u.Open("basecoat.css"); err != nil {
		t.Errorf("Open(basecoat.css): %v", err)
	}
	if _, err := u.Open("basecoat.js"); err != nil {
		t.Errorf("Open(basecoat.js): %v", err)
	}
}

func TestUnionFS_AddAssetSource_CSSAndJSIncluded(t *testing.T) {
	u := newTestUnionFS()
	u.AddAssetSource("child", fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte(`<div class="btn">x</div>`)},
		"basecoat/css/app.css": &fstest.MapFile{Data: []byte(`.btn{padding:1rem}`)},
		"basecoat/js/app.js":   &fstest.MapFile{Data: []byte(`basecoat.register('x','#x',function(){});`)},
	})
	u.Reload()

	css, _ := readVirtual(t, u, "basecoat.css")
	cssStr := string(css)
	if !strings.Contains(cssStr, ".btn") {
		t.Errorf("basecoat.css missing .btn rule from asset source; got %q", cssStr)
	}
	js, _ := readVirtual(t, u, "basecoat.js")
	jsStr := string(js)
	if !strings.Contains(jsStr, "basecoat.register('x'") {
		t.Errorf("basecoat.js missing user register call from asset source; got %q", jsStr)
	}
}

func TestUnionFS_AddAssetSource_CSSTreeShakenAgainstUnion(t *testing.T) {
	u := newTestUnionFS()
	// The asset source contributes CSS for .btn and .unused, but no
	// HTML anywhere in the union references .unused. The tree-shaker
	// should drop .unused.
	u.AddAssetSource("child", fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte(`<div class="btn">x</div>`)},
		"basecoat/css/app.css": &fstest.MapFile{Data: []byte(`.btn{padding:1rem}.unused{padding:2rem}`)},
	})
	u.Reload()

	css, _ := readVirtual(t, u, "basecoat.css")
	cssStr := string(css)
	if !strings.Contains(cssStr, ".btn") {
		t.Errorf("basecoat.css missing kept .btn rule; got %q", cssStr)
	}
	if strings.Contains(cssStr, ".unused") {
		t.Errorf("basecoat.css kept unused .unused rule (tree-shake should drop it); got %q", cssStr)
	}
}

func TestUnionFS_AddAssetSource_HTMLScannedForClasses(t *testing.T) {
	u := newTestUnionFS()
	// The asset source's HTML uses .btn. The downloaded basecoat CSS
	// isn't loaded here (no BasecoatVersion), so we use a second asset
	// source to contribute the rule and confirm the class scan from
	// the first asset source keeps it.
	u.AddAssetSource("html", fstest.MapFS{
		"page.html": &fstest.MapFile{Data: []byte(`<div class="btn">x</div>`)},
	})
	u.AddAssetSource("css", fstest.MapFS{
		"basecoat/css/b.css": &fstest.MapFile{Data: []byte(`.btn{color:red}.other{color:blue}`)},
	})
	u.Reload()

	css, _ := readVirtual(t, u, "basecoat.css")
	cssStr := string(css)
	if !strings.Contains(cssStr, ".btn") {
		t.Errorf("basecoat.css missing .btn (HTML class scan from asset source failed); got %q", cssStr)
	}
	if strings.Contains(cssStr, ".other") {
		t.Errorf("basecoat.css kept .other (no HTML references it); got %q", cssStr)
	}
}

func TestUnionFS_AddAssetSource_FragmentsWiredIntoTemplate(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("page", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<h1>{{template "greet" .}}</h1>`)},
	})
	u.AddAssetSource("frags", fstest.MapFS{
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "greet"}}Hello, {{.}}!{{end}}`)},
	})
	u.Reload()

	tmpl, err := u.Template("index.html")
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, "world"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>Hello, world!</h1>" {
		t.Errorf("got %q, want %q", got, "<h1>Hello, world!</h1>")
	}
}

func TestUnionFS_AddAssetSource_TemplateMatchDoesNotSeeAssetPages(t *testing.T) {
	u := newTestUnionFS()
	u.AddAssetSource("child", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<p>page in asset source</p>`)},
	})
	u.Reload()

	// index.html exists only in the asset source, so Template should
	// fail to resolve it as a page (asset sources are not
	// page-renderable).
	_, err := u.Template("index.html")
	if err == nil {
		t.Error("Template(index.html) succeeded; asset source pages should not be match targets")
	}
}

func TestUnionFS_AddAssetSource_RemoveSourceWorksForAsset(t *testing.T) {
	u := newTestUnionFS()
	u.AddAssetSource("child", fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte(`<div class="btn">x</div>`)},
		"basecoat/css/app.css": &fstest.MapFile{Data: []byte(`.btn{color:red}`)},
	})
	u.Reload()

	cssBefore, _ := readVirtual(t, u, "basecoat.css")
	if !strings.Contains(string(cssBefore), ".btn") {
		t.Fatal("setup: .btn rule not present before remove")
	}

	if !u.RemoveSource("child") {
		t.Fatal("RemoveSource(child) returned false")
	}
	u.Reload()

	cssAfter, _ := readVirtual(t, u, "basecoat.css")
	if strings.Contains(string(cssAfter), ".btn") {
		t.Errorf(".btn rule still in basecoat.css after RemoveSource + Reload; got %q", string(cssAfter))
	}
}

func TestUnionFS_AddAssetSource_ReplaceAcrossKinds(t *testing.T) {
	u := newTestUnionFS()

	// Register as a full source first — index.html is served.
	u.AddSource("svc", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<p>full</p>`)},
	})
	if _, err := u.Open("index.html"); err != nil {
		t.Fatalf("full source: Open(index.html): %v", err)
	}

	// Re-register the same name as an asset source — index.html should
	// no longer be served.
	u.AddAssetSource("svc", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<p>asset</p>`)},
	})
	if _, err := u.Open("index.html"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after AddAssetSource with same name: Open(index.html): got %v, want fs.ErrNotExist", err)
	}

	// And back to full source — index.html served again.
	u.AddSource("svc", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<p>full again</p>`)},
	})
	f, err := u.Open("index.html")
	if err != nil {
		t.Fatalf("after AddSource with same name: Open(index.html): %v", err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "<p>full again</p>" {
		t.Errorf("got %q, want %q", string(data), "<p>full again</p>")
	}
}

func TestUnionFS_AddAssetSource_ConcurrentWithOpenReload(t *testing.T) {
	u := newTestUnionFS()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			fs := fstest.MapFS{
				"index.html":           &fstest.MapFile{Data: []byte(fmt.Sprintf("src-%d", n))},
				"basecoat/css/app.css": &fstest.MapFile{Data: []byte(`.btn{color:red}`)},
			}
			u.AddAssetSource(fmt.Sprintf("src-%d", n), fs)
		}(i)
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = u.Open("basecoat.css")
			_, _ = u.Open("index.html")
			_, _ = u.ReadDir(".")
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
