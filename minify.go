package basecoat

import (
	"regexp"
	"strings"
)

// Regexps shared by the CSS and JS minifiers.
var (
	reCSSComment = regexp.MustCompile(`/\*[\s\S]*?\*/`) // /* ... */
	reJSSingle   = regexp.MustCompile(`//.*`)           // // ...
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

// minifyJS strips single-line comments, multi-line comments, newlines,
// tabs, and collapses runs of whitespace. Like minifyCSS, this is a
// simple textual pass that works for the generated output.
func minifyJS(s string) string {
	s = reJSSingle.ReplaceAllString(s, "")
	s = reCSSComment.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = reWhitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
