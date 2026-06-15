package basecoat

import (
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// Regexps used for class extraction and CSS tree-shaking.
var (
	reClassAttr  = regexp.MustCompile(`class="([^"]*)"`)
	reCSSRule    = regexp.MustCompile(`^([^{]+)\{`)
	reClassInSel = regexp.MustCompile(`\.([a-zA-Z0-9_-]+)`)
)

// generateCSS builds the basecoat.css string by concatenating:
//   - downloaded basecoat CSS (tree-shaken) — already includes the Tailwind
//     v4 preflight and theme layer
//   - all /css/*.css files from every source (tree-shaken)
//
// Every chunk is tree-shaken against the used set and the result is minified.
func generateCSS(sources []sourceFS, basecoatPath string, used map[string]bool) (string, error) {
	var parts []string

	if basecoatPath != "" {
		data, err := os.ReadFile(basecoatPath)
		if err == nil {
			parts = append(parts, treeShakeCSS(string(data), used))
		}
	}
	for _, src := range sources {
		cssFiles, err := fs.Glob(src.fs, "css/*.css")
		if err != nil {
			continue
		}
		for _, name := range cssFiles {
			f, err := src.fs.Open(name)
			if err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				continue
			}
			parts = append(parts, treeShakeCSS(string(data), used))
		}
	}

	combined := strings.Join(parts, "\n")
	return minifyCSS(combined), nil
}

// extractUsedClasses walks every .html file in all source filesystems
// and collects every class name that appears in a class="..." attribute.
// The returned set always includes "*", "html", "body", and ":root" so
// that universal and type selectors are never stripped.
func extractUsedClasses(sources []sourceFS) map[string]bool {
	used := map[string]bool{"*": true, "html": true, "body": true, ":root": true}
	for _, src := range sources {
		fs.WalkDir(src.fs, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
				return err
			}
			f, err := src.fs.Open(p)
			if err != nil {
				return nil
			}
			data, _ := io.ReadAll(f)
			f.Close()
			matches := reClassAttr.FindAllStringSubmatch(string(data), -1)
			for _, m := range matches {
				for _, cls := range strings.Fields(m[1]) {
					used[cls] = true
				}
			}
			return nil
		})
	}
	return used
}

// treeShakeCSS removes CSS rules whose selectors only reference class
// names that are absent from the used set. @-rules (@media, @keyframes,
// @font-face, etc.) and rules without class selectors are always kept.
func treeShakeCSS(css string, used map[string]bool) string {
	var result strings.Builder
	rules := splitCSSRules(css)
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		if keepRule(rule, used) {
			result.WriteString(rule)
			result.WriteString("\n")
		}
	}
	return result.String()
}

// splitCSSRules splits CSS text into individual rule blocks by tracking
// brace depth, which correctly handles nested @-rules.
func splitCSSRules(css string) []string {
	var rules []string
	depth := 0
	start := 0
	for i, ch := range css {
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				rules = append(rules, css[start:i+1])
				start = i + 1
			}
		}
	}
	if start < len(css) {
		rem := strings.TrimSpace(css[start:])
		if rem != "" {
			rules = append(rules, rem)
		}
	}
	return rules
}

// keepRule decides whether a single CSS rule block should be included
// in the tree-shaken output.
func keepRule(rule string, used map[string]bool) bool {
	// Always keep at-rules — we don't attempt to tree-shake inside them.
	if strings.HasPrefix(rule, "@media") || strings.HasPrefix(rule, "@keyframes") ||
		strings.HasPrefix(rule, "@font-face") || strings.HasPrefix(rule, "@import") ||
		strings.HasPrefix(rule, "@charset") || strings.HasPrefix(rule, "@namespace") {
		return true
	}
	m := reCSSRule.FindStringSubmatch(rule)
	if m == nil {
		return true
	}
	classes := extractClassesFromSelector(m[1])
	if len(classes) == 0 {
		return true
	}
	for _, cls := range classes {
		if used[cls] {
			return true
		}
	}
	return false
}

// extractClassesFromSelector returns every .<class> token in a CSS selector.
func extractClassesFromSelector(sel string) []string {
	matches := reClassInSel.FindAllStringSubmatch(sel, -1)
	if matches == nil {
		return nil
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m[1]
	}
	return out
}

// generateJS builds the basecoat.js string by concatenating the embedded
// basecoat runtime (which registers the window.basecoat API and all built-in
// components) followed by every /js/*.js file from every source directory.
// User files can call basecoat.register(...) to add new components or
// override built-in ones — the registry uses assignment so later calls win.
func generateJS(sources []sourceFS, embeddedJS []byte) (string, error) {
	var parts []string

	if len(embeddedJS) > 0 {
		parts = append(parts, string(embeddedJS))
	}

	for _, src := range sources {
		jsFiles, err := fs.Glob(src.fs, "js/*.js")
		if err != nil {
			continue
		}
		for _, name := range jsFiles {
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
