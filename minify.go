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

// States for the JS string/comment/regex scanner used by minifyJS.
const (
	jsNormal       = iota
	jsSQuote       // inside '...'
	jsDQuote       // inside "..."
	jsTemplate     // inside `...`
	jsLineComment  // inside //... up to newline
	jsBlockComment // inside /* ... */
	jsRegex        // inside /.../
	jsRegexClass   // inside the [...] part of a regex literal
)

// isIdentChar reports whether c may continue a JS identifier
// (ASCII letters, digits, '_' or '$').
func isIdentChar(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

func isJSSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// jsRegexContext reports whether a lone '/' at the current position
// should open a regex literal rather than be read as division. It
// inspects the last meaningful byte written so far: if an operand
// (identifier, number, ')', ']', '.', or a closing quote) just ended,
// '/' is division; otherwise (start of input, after an operator or an
// expression-expecting keyword like return/if/new, after a '{' block,
// etc.) '/' starts a regex.
//
// This is a heuristic, not a full lexer — it cannot disambiguate every
// grammatical corner (e.g. `function(){} /g/` is treated as regex),
// but it correctly handles the regular literals that ship in the
// basecoat runtime and user component bundles, including regexes that
// contain '"' (the case that broke the previous scanner).
func jsRegexContext(b string) bool {
	i := len(b)
	for i > 0 && isJSSpace(b[i-1]) {
		i--
	}
	if i == 0 {
		return true
	}
	c := b[i-1]
	switch {
	case isIdentChar(c):
		j := i
		for j > 0 && isIdentChar(b[j-1]) {
			j--
		}
		switch b[j:i] {
		case "return", "typeof", "void", "delete", "new", "in", "of",
			"instanceof", "do", "else", "throw", "yield", "await", "case",
			"if", "for", "while", "switch", "catch", "with":
			return true
		}
		return false
	case c == ')':
		return jsParenFollowsKeyword(b[:i])
	case c == ']', c == '.', c == '"', c == '\'', c == '`':
		return false
	case c == '}':
		return true
	default:
		// operators and structural punctuation → operand position
		return true
	}
}

// jsParenFollowsKeyword reports whether the ')' at the end of b closes
// a group whose preceding token is a control-flow keyword (if, for,
// while, switch, catch, with, do) — in which case a following '/' is
// a regex literal, not division.
func jsParenFollowsKeyword(b string) bool {
	depth := 0
	for i := len(b); i > 0; {
		i--
		switch b[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				j := i
				for j > 0 && isIdentChar(b[j-1]) {
					j--
				}
				switch b[j:i] {
				case "if", "for", "while", "switch", "catch", "with", "do":
					return true
				}
				return false
			}
		case '{', '}', '[', ']':
			return false
		}
	}
	return false
}

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
// from s, leaving '...', "...", and `...` string literals and regex
// literals (/.../, including the /.../flags form and escapes inside
// character classes) untouched. Backslash escapes inside strings and
// regexes are honoured so '\\' and '\” do not terminate the literal.
// Template-literal expressions (${...}) are not specially handled.
//
// Regex literals are recognised by context: a '/' in operand position
// (start of input, after an operator or a control-flow keyword) opens
// a regex; a '/' after an operand (identifier, number, ')', ']', '.'
// or a closing quote) is division. This matches the basecoat runtime
// and user component bundles without pulling in a full JS grammar.
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
						if jsRegexContext(out.String()) {
							state = jsRegex
						}
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
		case jsRegex:
			switch c {
			case '\\':
				out.WriteByte(c)
				if i+1 < len(s) {
					out.WriteByte(s[i+1])
					i++
				}
			case '[':
				state = jsRegexClass
				out.WriteByte(c)
			case '/':
				state = jsNormal
				out.WriteByte(c)
			case '\n':
				// Unterminated regex — restore normal mode so a
				// malformed literal doesn't eat the rest of the
				// bundle. (Should not happen in valid JS.)
				state = jsNormal
				out.WriteByte(c)
			default:
				out.WriteByte(c)
			}
		case jsRegexClass:
			switch c {
			case '\\':
				out.WriteByte(c)
				if i+1 < len(s) {
					out.WriteByte(s[i+1])
					i++
				}
			case ']':
				state = jsRegex
				out.WriteByte(c)
			case '\n':
				state = jsNormal
				out.WriteByte(c)
			default:
				out.WriteByte(c)
			}
		}
	}
	return out.String()
}
