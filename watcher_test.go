package basecoat

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mkdirp creates a directory tree under a temp root, returns the root.
func mkdirp(t *testing.T, layout map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range layout {
		p := filepath.Join(root, rel)
		if dir := filepath.Dir(p); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return root
}

// touch sets the file's mtime to now+1s so a subsequent change is
// detectable even on filesystems with coarse mtime granularity.
func touch(t *testing.T, p string) {
	t.Helper()
	ts := time.Now().Add(time.Second)
	if err := os.Chtimes(p, ts, ts); err != nil {
		t.Fatalf("chtimes %s: %v", p, err)
	}
}

// TestWatchSource_Changed_DetectsNestedFileModify pins the recursive
// walk fix: modifying a file two levels under root (basecoat/js/x.js)
// must be reported as a change. The pre-fix shallow ReadDir(root)
// missed this on Linux because the parent dir's mtime does not move
// when a contained file changes.
func TestWatchSource_Changed_DetectsNestedFileModify(t *testing.T) {
	root := mkdirp(t, map[string]string{
		"basecoat/js/app.js": "basecoat.register('initial','.i',function(){});",
	})
	w := newWatchSource(root)
	// First sweep reports the initial tree as changed (everything is
	// new to the empty map) — this is intentional, it primes the
	// watcher and triggers an initial Reload.
	if !w.changed() {
		t.Fatal("first sweep should report the initial tree as changed")
	}
	// Settle: the second sweep over the same tree is a no-op.
	if w.changed() {
		t.Fatal("second sweep over unchanged tree should not report a change")
	}
	touch(t, filepath.Join(root, "basecoat/js/app.js"))
	if !w.changed() {
		t.Error("changed() = false after modifying a nested file; want true")
	}
	if w.changed() {
		t.Error("changed() = true on idempotent re-sweep; want false")
	}
}

// TestWatchSource_Changed_DetectsNewNestedFile covers the add-file
// case: a brand new file under basecoat/css/ must trigger a change.
func TestWatchSource_Changed_DetectsNewNestedFile(t *testing.T) {
	root := mkdirp(t, map[string]string{
		"basecoat/css/a.css": ".a{padding:1rem;}",
	})
	w := newWatchSource(root)
	w.changed()
	p := filepath.Join(root, "basecoat/css/b.css")
	if err := os.WriteFile(p, []byte(".b{padding:2rem;}"), 0644); err != nil {
		t.Fatal(err)
	}
	touch(t, p)
	if !w.changed() {
		t.Error("changed() = false after adding a nested file; want true")
	}
}

// TestWatchSource_Changed_DetectsDeletedNestedFile covers the
// remove-file case: deleting a file under basecoat/js/ must trigger a
// change (and the deleted entry must be pruned from the map so it does
// not keep reporting as changed forever).
func TestWatchSource_Changed_DetectsDeletedNestedFile(t *testing.T) {
	root := mkdirp(t, map[string]string{
		"basecoat/js/a.js": "basecoat.register('a','.a',function(){});",
		"basecoat/js/b.js": "basecoat.register('b','.b',function(){});",
	})
	w := newWatchSource(root)
	w.changed()
	if err := os.Remove(filepath.Join(root, "basecoat/js/b.js")); err != nil {
		t.Fatal(err)
	}
	if !w.changed() {
		t.Error("changed() = false after deleting a nested file; want true")
	}
	if w.changed() {
		t.Error("changed() = true on idempotent re-sweep after delete; want false")
	}
}

// TestWatchSource_Changed_IgnoresUnchangedTree confirms a tree with no
// mtime movement reports no change after the initial sweep.
func TestWatchSource_Changed_IgnoresUnchangedTree(t *testing.T) {
	root := mkdirp(t, map[string]string{
		"index.html":               "<html></html>",
		"basecoat/css/a.css":       ".a{padding:1rem;}",
		"basecoat/js/a.js":         "basecoat.register('a','.a',function(){});",
		"basecoat/html/frag.html":  "<p>fragment</p>",
	})
	w := newWatchSource(root)
	w.changed() // settle
	if w.changed() {
		t.Error("changed() = true on an unchanged tree; want false")
	}
}

// TestWatchSource_Changed_NoFalsePositiveOnReaddedFile guards against
// a regression where a file deleted then re-created with the same
// mtime bucket could be missed. Re-adding the same path must report a
// change (the entry was pruned on delete).
func TestWatchSource_Changed_NoMissOnDeleteThenReadd(t *testing.T) {
	root := mkdirp(t, map[string]string{
		"basecoat/js/app.js": "v1",
	})
	w := newWatchSource(root)
	w.changed()
	p := filepath.Join(root, "basecoat/js/app.js")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if !w.changed() {
		t.Fatal("delete not detected")
	}
	if err := os.WriteFile(p, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	touch(t, p)
	if !w.changed() {
		t.Error("re-add of previously-deleted file not detected")
	}
}