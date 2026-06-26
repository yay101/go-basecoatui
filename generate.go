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
//   - every basecoat/css/**/*.css file from every source.
//
// In child mode basecoatStylesPath is "" and only user CSS is included.
// The result is minified.
func generateCSS(sources []sourceFS, basecoatStylesPath string) (string, error) {
	var parts []string

	if basecoatStylesPath != "" {
		if data, err := os.ReadFile(basecoatStylesPath); err == nil {
			parts = append(parts, string(data))
		}
	}

	for _, src := range sources {
		for _, name := range walkExt(src.fs, "basecoat/css", ".css") {
			f, err := src.fs.Open(name)
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
	}

	return minifyCSS(strings.Join(parts, "\n")), nil
}

// generateJS builds the basecoat.js string by concatenating:
//   - runtimeJS if non-nil (parent mode, holds the downloaded or
//     embedded basecoat.js),
//   - every basecoat/js/**/*.js file from every source.
//
// In child mode runtimeJS is nil and only user JS is included. The
// result is minified.
func generateJS(sources []sourceFS, runtimeJS []byte) (string, error) {
	var parts []string

	if len(runtimeJS) > 0 {
		parts = append(parts, string(runtimeJS))
	}

	for _, src := range sources {
		for _, name := range walkExt(src.fs, "basecoat/js", ".js") {
			f, err := src.fs.Open(name)
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
	}

	return minifyJS(strings.Join(parts, "\n")), nil
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
