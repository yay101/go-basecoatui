package basecoat

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
		sourceIdx: make(map[string]int),
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
		"index.html":  &fstest.MapFile{Data: []byte(`<div class="btn">x</div>`)},
		"css/app.css": &fstest.MapFile{Data: []byte(`.btn{padding:1rem;color:red;}.unused{padding:2rem;}`)},
		"js/app.js":   &fstest.MapFile{Data: []byte(`basecoat.register('x','#x',function(el){});`)},
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
