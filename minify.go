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

// States for the JS string/comment scanner used by minifyJS.
const (
	jsNormal = iota
	jsSQuote       // inside '...'
	jsDQuote       // inside "..."
	jsTemplate     // inside `...`
	jsLineComment  // inside //... up to newline
	jsBlockComment // inside /* ... */
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

// minifyJS strips // line comments and /* ... */ block comments
// (string-aware: it does not treat // or /* inside '...', "...", or
// `...` as comment starts), removes newlines/tabs/carriage-returns,
// and collapses runs of whitespace.
//
// The string-awareness is required because the embedded basecoat runtime
// ships SVG icons as single-quoted strings containing
// xmlns="http://www.w3.org/2000/svg" — a naive regex would treat the //
// as a line-comment opener and strip the rest of the bundle.
func minifyJS(s string) string {
	s = stripJSComments(s)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = reWhitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// stripJSComments removes // line comments and /* ... */ block comments
// from s, leaving '...', "...", and `...` string literals untouched.
// Backslash escapes inside strings are honoured so '\\' and '\'' do not
// terminate the string. Template-literal expressions (${...}) and regex
// literals (/.../) are not specially handled.
func stripJSComments(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	state := jsNormal
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case jsNormal:
			switch c {
			case '\'':
				state = jsSQuote
				out.WriteByte(c)
			case '"':
				state = jsDQuote
				out.WriteByte(c)
			case '`':
				state = jsTemplate
				out.WriteByte(c)
			case '/':
				if i+1 < len(s) {
					switch s[i+1] {
					case '/':
						state = jsLineComment
						i++
					case '*':
						state = jsBlockComment
						i++
					default:
						out.WriteByte(c)
					}
				} else {
					out.WriteByte(c)
				}
			default:
				out.WriteByte(c)
			}
		case jsSQuote:
			switch c {
			case '\\':
				out.WriteByte(c)
				if i+1 < len(s) {
					out.WriteByte(s[i+1])
					i++
				}
			case '\'':
				state = jsNormal
				out.WriteByte(c)
			default:
				out.WriteByte(c)
			}
		case jsDQuote:
			switch c {
			case '\\':
				out.WriteByte(c)
				if i+1 < len(s) {
					out.WriteByte(s[i+1])
					i++
				}
			case '"':
				state = jsNormal
				out.WriteByte(c)
			default:
				out.WriteByte(c)
			}
		case jsTemplate:
			switch c {
			case '\\':
				out.WriteByte(c)
				if i+1 < len(s) {
					out.WriteByte(s[i+1])
					i++
				}
			case '`':
				state = jsNormal
				out.WriteByte(c)
			default:
				out.WriteByte(c)
			}
		case jsLineComment:
			if c == '\n' {
				state = jsNormal
				out.WriteByte(c)
			}
		case jsBlockComment:
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				state = jsNormal
				i++
			}
		}
	}
	return out.String()
}
