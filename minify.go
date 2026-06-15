package basecoat

import (
	"regexp"
	"strings"
)

// Regexps shared by the CSS and JS minifiers.
var (
	reCSSComment = regexp.MustCompile(`/\*[\s\S]*?\*/`) // /* ... */
	reWhitespace = regexp.MustCompile(`\s+`)            // one-or-more whitespace chars
)

// minifyCSS strips comments, newlines, tabs, and trims whitespace around
// structural characters ({, }, ;, :, ,). This is intentionally simple —
// it does not parse selectors or values.
func minifyCSS(s string) string {
	s = reCSSComment.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = reWhitespace.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, " {", "{")
	s = strings.ReplaceAll(s, "{ ", "{")
	s = strings.ReplaceAll(s, " }", "}")
	s = strings.ReplaceAll(s, "} ", "}")
	s = strings.ReplaceAll(s, "; ", ";")
	s = strings.ReplaceAll(s, " :", ":")
	s = strings.ReplaceAll(s, ": ", ":")
	s = strings.ReplaceAll(s, " ,", ",")
	s = strings.ReplaceAll(s, ", ", ",")
	return strings.TrimSpace(s)
}

// minifyJS strips multi-line comments, newlines, tabs, and collapses
// runs of whitespace. It deliberately does not strip single-line //
// comments: a naive regex would also match the // inside string
// literals like "http://" in the embedded basecoat runtime's toast SVG
// icons, eating the rest of the line and corrupting the output. The
// embedded basecoat.js is already minified, and user-supplied JS that
// wants // comments removed can be pre-processed before being placed
// under js/.
func minifyJS(s string) string {
	s = reCSSComment.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = reWhitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
