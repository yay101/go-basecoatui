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

func TestUnionFS_SourceTemplate_FragmentsScopedPerSource(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<h1>{{template "card" .}}</h1>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "card"}}A-card{{end}}`)},
	})
	u.AddSource("b", fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<h1>{{template "card" .}}</h1>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "card"}}B-card{{end}}`)},
	})

	ta, err := u.SourceTemplate("a", "index.html")
	if err != nil {
		t.Fatalf("SourceTemplate(a): %v", err)
	}
	var buf bytes.Buffer
	if err := ta.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute a: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>A-card</h1>" {
		t.Errorf("a: got %q, want %q", got, "<h1>A-card</h1>")
	}

	tb, err := u.SourceTemplate("b", "index.html")
	if err != nil {
		t.Fatalf("SourceTemplate(b): %v", err)
	}
	buf.Reset()
	if err := tb.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute b: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>B-card</h1>" {
		t.Errorf("b: got %q, want %q", got, "<h1>B-card</h1>")
	}
}

func TestUnionFS_SourceTemplate_DoesNotSeeOtherSourcesFragments(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("page", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<h1>{{template "frag" .}}</h1>`)},
	})
	u.AddSource("frags", fstest.MapFS{
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "frag"}}from-frags{{end}}`)},
	})

	// Scoping to "page" must not see fragments defined in "frags" —
	// the page has no "frag" define of its own, so Execute should fail.
	tmpl, err := u.SourceTemplate("page", "index.html")
	if err != nil {
		t.Fatalf("SourceTemplate: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err == nil {
		t.Errorf("expected execute to fail when scoped to a source that lacks the fragment, got output %q", buf.String())
	}
}

func TestUnionFS_SourceTemplate_GlobalFirstWinsWhenPathsCollide(t *testing.T) {
	// When two sources ship a fragment at the same path (e.g. both
	// put a "card" at basecoat/html/frag.html), the global Template
	// method dedupes by path and uses the first source. The user
	// can't pick — and that's exactly why SourceTemplate exists:
	// scope to a specific source and you get a clean, isolated
	// fragment namespace per source.
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<h1>{{template "card" .}}</h1>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "card"}}A{{end}}`)},
	})
	u.AddSource("b", fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<h1>{{template "card" .}}</h1>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "card"}}B{{end}}`)},
	})

	tmpl, err := u.Template("index.html")
	if err != nil {
		t.Fatalf("global Template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>A</h1>" {
		t.Errorf("global Template first-wins: got %q, want %q", got, "<h1>A</h1>")
	}

	tb, err := u.SourceTemplate("b", "index.html")
	if err != nil {
		t.Fatalf("SourceTemplate(b): %v", err)
	}
	buf.Reset()
	if err := tb.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute b: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>B</h1>" {
		t.Errorf("SourceTemplate(b) isolated: got %q, want %q (b's own fragment should win)", got, "<h1>B</h1>")
	}
}

func TestUnionFS_SourceTemplate_UnknownSourceErrors(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<p>{{.}}</p>`)},
	})

	if _, err := u.SourceTemplate("nope", "index.html"); err == nil {
		t.Error("SourceTemplate with unknown source should return an error")
	}
}

func TestUnionFS_SourceTemplateFuncs_FragmentsScopedPerSource(t *testing.T) {
	u := newTestUnionFS()
	u.AddSource("a", fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<h1>{{shout "hi"}}</h1>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "extra"}}A-extra{{end}}`)},
	})
	u.AddSource("b", fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<h1>{{whisper "HI"}}</h1>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "extra"}}B-extra{{end}}`)},
	})

	funcs := template.FuncMap{
		"shout":   strings.ToUpper,
		"whisper": strings.ToLower,
	}

	ta, err := u.SourceTemplateFuncs("a", funcs, "index.html")
	if err != nil {
		t.Fatalf("SourceTemplateFuncs(a): %v", err)
	}
	var buf bytes.Buffer
	if err := ta.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute a: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>HI</h1>" {
		t.Errorf("a: got %q, want %q", got, "<h1>HI</h1>")
	}

	tb, err := u.SourceTemplateFuncs("b", funcs, "index.html")
	if err != nil {
		t.Fatalf("SourceTemplateFuncs(b): %v", err)
	}
	buf.Reset()
	if err := tb.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute b: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "<h1>hi</h1>" {
		t.Errorf("b: got %q, want %q", got, "<h1>hi</h1>")
	}
}

func TestUnionFS_SourceTemplate_CacheKeyIncludesSourceName(t *testing.T) {
	u := newTestUnionFS()
	c := &countFS{FS: fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte(`<p>{{.}}</p>`)},
		"basecoat/html/frag.html": &fstest.MapFile{Data: []byte(`{{define "card"}}x{{end}}`)},
	}}
	u.AddSource("a", c)

	if _, err := u.SourceTemplate("a", "index.html"); err != nil {
		t.Fatalf("SourceTemplate: %v", err)
	}
	opensAfterFirst := c.opens

	if _, err := u.SourceTemplate("a", "index.html"); err != nil {
		t.Fatalf("SourceTemplate repeat: %v", err)
	}
	if c.opens != opensAfterFirst {
		t.Errorf("repeat SourceTemplate re-read files: %d -> %d (expected cache hit)", opensAfterFirst, c.opens)
	}
}
