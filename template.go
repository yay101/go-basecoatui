package basecoat

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"reflect"
	"strings"
)

// Template parses the given patterns as html/template files out of the
// union filesystem, with every *.html file found anywhere under any
// source's "basecoat/html/" tree (recursive) parsed as fragments.
// Standard html/template directives (define, block, template) wire the
// fragments into the caller's page templates.
//
// The first entry of match becomes the main template. Callers can
// register template functions on the returned value with t.Funcs(...)
// before Execute; the fragments are parsed without those functions and
// so cannot reference them — keep fragment logic data-only, or call
// TemplateFuncs to register funcs before parsing.
//
// Results are cached and reused across calls until the next Reload
// (which the poll watcher triggers on file changes). Callers no longer
// need to cache *template.Template values themselves. A parse error is
// also cached: a broken template won't be re-parsed on every request
// until the underlying source changes and Reload runs.
//
// All sources share one fragment namespace, so duplicate {{define}}
// names across sources cause a parse error. Use SourceTemplate to
// scope resolution to a single source when you need per-source
// fragment isolation.
func (u *UnionFS) Template(match ...string) (*template.Template, error) {
	return u.templateWithScoped("", nil, match...)
}

// TemplateFuncs is like Template but registers funcs on the resulting
// template before parsing. Use this when fragment files (under
// basecoat/html/) need to call functions such as printf or a custom
// helper; Template alone cannot give fragments access to Funcs because
// fragment parsing happens before the caller can add Funcs.
//
// The cache is keyed by the joined match list plus the identity of
// the funcs map. Reuse the same FuncMap value across calls (define it
// once at startup) to get cache hits; a freshly-allocated FuncMap for
// every call is always a cache miss.
func (u *UnionFS) TemplateFuncs(funcs template.FuncMap, match ...string) (*template.Template, error) {
	return u.templateWithScoped("", funcs, match...)
}

// SourceTemplate is like Template but scopes resolution to the source
// registered as sourceName. The main template must live in that
// source, and only that source's "basecoat/html/**/*.html" fragments
// are collected. Each source has its own fragment namespace, so two
// sources can each define a fragment with the same name without
// colliding. Returns an error if sourceName is not a registered
// source. Use it when each source is a self-contained sub-app that
// ships its own page plus its own fragments and should be rendered
// independently of the rest of the union.
func (u *UnionFS) SourceTemplate(sourceName string, match ...string) (*template.Template, error) {
	return u.templateWithScoped(sourceName, nil, match...)
}

// SourceTemplateFuncs is like SourceTemplate but registers funcs on
// the resulting template before parsing.
func (u *UnionFS) SourceTemplateFuncs(sourceName string, funcs template.FuncMap, match ...string) (*template.Template, error) {
	return u.templateWithScoped(sourceName, funcs, match...)
}

func (u *UnionFS) templateWithScoped(sourceName string, funcs template.FuncMap, match ...string) (*template.Template, error) {
	// The cache key embeds sourceName so a scoped call and a global
	// call with the same match list don't share an entry — they
	// resolve from different source sets and so have different
	// fragments.
	key := sourceName + "\x00" + strings.Join(match, "\x00")
	funcsPtr, funcsNil := funcsIdentity(funcs)

	// Cache lookup under the read lock.
	u.mu.RLock()
	gen := u.templateGen
	if e, ok := u.lookupTemplateLocked(key, gen, funcsPtr, funcsNil); ok {
		u.mu.RUnlock()
		if e.parseErr != nil {
			return nil, e.parseErr
		}
		return e.tmpl, nil
	}

	var sources []sourceFS
	if sourceName != "" {
		ref, ok := u.sourceIdx[sourceName]
		if !ok {
			u.mu.RUnlock()
			return nil, fmt.Errorf("template: no source named %q", sourceName)
		}
		// Snapshot just the one source so parseTemplate sees a
		// single-element slice and walks only that tree.
		sources = []sourceFS{u.sources[ref.index]}
	} else {
		sources = make([]sourceFS, len(u.sources))
		copy(sources, u.sources)
	}
	u.mu.RUnlock()

	// Parse outside the lock — this does FS reads and can be slow.
	tmpl, err := parseTemplate(sources, funcs, match)

	u.mu.Lock()
	// Re-read gen under the write lock: a Reload may have bumped it
	// while we were parsing. Store the current gen so a stale-by-reload
	// entry is detectable on the next lookup.
	storeGen := u.templateGen
	if u.tmplCache == nil {
		u.tmplCache = make(map[string]*tmplCacheEntry)
	}
	u.tmplCache[key] = &tmplCacheEntry{
		gen:      storeGen,
		funcsPtr: funcsPtr,
		funcsNil: funcsNil,
		tmpl:     tmpl,
		parseErr: err,
	}
	u.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return tmpl, nil
}

// lookupTemplateLocked returns the cache entry for key if it matches the
// current generation and funcs identity. Caller must hold u.mu (read or
// write).
func (u *UnionFS) lookupTemplateLocked(key string, gen uint64, funcsPtr uintptr, funcsNil bool) (*tmplCacheEntry, bool) {
	if u.tmplCache == nil {
		return nil, false
	}
	e, ok := u.tmplCache[key]
	if !ok {
		return nil, false
	}
	if e.gen != gen {
		return nil, false
	}
	if e.funcsNil != funcsNil || e.funcsPtr != funcsPtr {
		return nil, false
	}
	return e, true
}

// funcsIdentity returns a pointer-stable identity for a FuncMap so the
// template cache can tell two callers apart. Reusing the same FuncMap
// value across calls (the recommended pattern) yields cache hits; a
// fresh FuncMap per call is always a miss.
func funcsIdentity(funcs template.FuncMap) (ptr uintptr, isNil bool) {
	if funcs == nil {
		return 0, true
	}
	return reflect.ValueOf(funcs).Pointer(), false
}

// parseTemplate does the actual FS reads and html/template parsing.
// Extracted from templateWith so the cache path can run it outside the
// UnionFS lock.
func parseTemplate(sources []sourceFS, funcs template.FuncMap, match []string) (*template.Template, error) {
	pageNames, err := resolvePageNames(sources, match)
	if err != nil {
		return nil, err
	}
	fragments := collectFragments(sources)

	names := make([]string, 0, len(pageNames)+len(fragments))
	names = append(names, pageNames...)
	names = append(names, fragments...)

	if len(names) == 0 {
		t := template.New("")
		if funcs != nil {
			t = t.Funcs(funcs)
		}
		return t, nil
	}

	contents, err := readContents(sources, names)
	if err != nil {
		return nil, err
	}

	// Replicate parseFiles' layout: the first name becomes the main
	// template (with funcs), subsequent files are parsed into named
	// sub-templates so their {{define}} blocks are addressable from
	// the main by name.
	t := template.New(names[0])
	if funcs != nil {
		t = t.Funcs(funcs)
	}
	if _, err := t.Parse(contents[names[0]]); err != nil {
		return nil, err
	}
	for _, name := range names[1:] {
		sub := t.New(name)
		if _, err := sub.Parse(contents[name]); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// resolvePageNames returns the input match list, validating that every
// requested file exists in at least one source. The order is preserved
// so the first entry remains the main template.
func resolvePageNames(sources []sourceFS, match []string) ([]string, error) {
	out := make([]string, 0, len(match))
	for _, name := range match {
		if hasFile(sources, name) {
			out = append(out, name)
			continue
		}
		return nil, fmt.Errorf("template: pattern matches no files: %q", name)
	}
	return out, nil
}

// collectFragments walks every source's basecoat/html/ subtree and
// returns the set of *.html files, deduped. First occurrence wins.
func collectFragments(sources []sourceFS) []string {
	seen := make(map[string]bool)
	var out []string
	for _, src := range sources {
		fs.WalkDir(src.fs, "basecoat/html", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(p, ".html") {
				return nil
			}
			if seen[p] {
				return nil
			}
			seen[p] = true
			out = append(out, p)
			return nil
		})
	}
	return out
}

// readContents reads every name from the first source that contains it.
// Missing files are an error.
func readContents(sources []sourceFS, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		for _, src := range sources {
			f, err := src.fs.Open(name)
			if err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				return nil, err
			}
			out[name] = string(data)
			break
		}
		if _, ok := out[name]; !ok {
			return nil, fmt.Errorf("template: %w: %q", fs.ErrNotExist, name)
		}
	}
	return out, nil
}

// hasFile reports whether any source contains the given file.
func hasFile(sources []sourceFS, name string) bool {
	for _, src := range sources {
		if _, err := src.fs.Open(name); err == nil {
			return true
		}
	}
	return false
}
