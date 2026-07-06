package basecoat

import (
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// generateCSS builds the basecoat.css string by concatenating:
//   - the contents of basecoatStylesPath if non-empty (parent mode),
//   - every basecoat/css/**/*.css file reachable through ufs.
//
// ufs is the unmasked view of the UnionFS, so the reserved
// basecoat/ namespace is enumerable but paths under it still resolve
// with the same first-match-wins overlay semantics as every other
// path. In child mode basecoatStylesPath is "" and only user CSS is
// included. The result is minified.
func generateCSS(ufs fs.FS, basecoatStylesPath string) (string, error) {
	var parts []string

	if basecoatStylesPath != "" {
		if data, err := os.ReadFile(basecoatStylesPath); err == nil {
			parts = append(parts, string(data))
		}
	}

	for _, name := range walkExt(ufs, "basecoat/css", ".css") {
		f, err := ufs.Open(name)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			continue
		}
		parts = append(parts, string(data))
	}

	return minifyCSS(strings.Join(parts, "\n")), nil
}

// generateJS builds the basecoat.js string by concatenating:
//   - tailwindBrowserJS if non-nil (parent mode, holds the Tailwind v4
//     browser build which scans the DOM and generates utility classes
//     at runtime),
//   - runtimeJS if non-nil (parent mode, holds the downloaded or
//     embedded basecoat.js runtime),
//   - the lifecycle shim (parent mode only, after the runtime),
//   - every basecoat/js/**/*.js file reachable through ufs.
//
// ufs is the unmasked view of the UnionFS, so the reserved basecoat/
// namespace is enumerable but paths under it still resolve with the
// same first-match-wins overlay semantics as every other path.
//
// In child mode both tailwindBrowserJS and runtimeJS are nil and only
// user JS is included. The runtime + lifecycle + user JS are minified
// together; the Tailwind browser build is prepended verbatim — it is
// already minified upstream and re-minifying a ~270KB blob with the
// textual minifier is both wasteful and risky (its dense regexes can
// trip the regex/division heuristic in stripJSComments).
func generateJS(ufs fs.FS, runtimeJS, tailwindBrowserJS []byte) (string, error) {
	var minified []string

	if len(runtimeJS) > 0 {
		parts := []string{string(runtimeJS)}
		// Lifecycle shim: wraps basecoat.register with an optional
		// destroy(el) and adds destroy(el)/destroyAll(root). Parent
		// mode only — child bundles rely on the parent's shim being
		// already on the page.
		parts = append(parts, string(lifecycleShim()))
		minified = append(minified, parts...)
	}

	for _, name := range walkExt(ufs, "basecoat/js", ".js") {
		f, err := ufs.Open(name)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			continue
		}
		minified = append(minified, string(data))
	}

	body := minifyJS(strings.Join(minified, "\n"))

	// Parent mode: prepend the Tailwind browser build first so it
	// scans the DOM and generates utilities before the basecoat
	// runtime and user JS run. It is left un-minified (see comment
	// above). Child mode skips this entirely — the parent page has
	// already loaded it.
	if len(runtimeJS) > 0 && len(tailwindBrowserJS) > 0 {
		return string(tailwindBrowserJS) + "\n" + body, nil
	}
	return body, nil
}

// walkExt returns every path under root in fsys whose file name ends in
// suffix, recursively. It returns nil if root does not exist. Paths are
// returned in lexical order so concatenated output is deterministic
// across runs.
func walkExt(fsys fs.FS, root, suffix string) []string {
	var out []string
	fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, suffix) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
