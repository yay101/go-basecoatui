package basecoat

import "testing"

func TestStripJSComments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain code", "var x = 1;", "var x = 1;"},
		{"line comment", "var x = 1; // trailing\nvar y = 2;", "var x = 1; \nvar y = 2;"},
		{"line comment eof", "var x = 1; // trailing", "var x = 1; "},
		{"block comment", "var x = 1; /* mid */ var y = 2;", "var x = 1;  var y = 2;"},
		{"block comment multiline", "a; /* line1\nline2\nline3 */ b;", "a;  b;"},
		{"comment then newline", "// only\ncode", "\ncode"},
		{
			"single-quoted string with slashes",
			"var x = 'http://a.com/b';",
			"var x = 'http://a.com/b';",
		},
		{
			"double-quoted string with slashes",
			`var x = "http://a.com/b";`,
			`var x = "http://a.com/b";`,
		},
		{
			"svg attribute with // in url",
			`'<svg xmlns="http://www.w3.org/2000/svg"></svg>'`,
			`'<svg xmlns="http://www.w3.org/2000/svg"></svg>'`,
		},
		{
			"all four toast icons",
			`{success:'<svg xmlns="http://www.w3.org/2000/svg"></svg>',error:'<svg xmlns="http://www.w3.org/2000/svg"></svg>',info:'<svg xmlns="http://www.w3.org/2000/svg"></svg>',warning:'<svg xmlns="http://www.w3.org/2000/svg"></svg>'}`,
			`{success:'<svg xmlns="http://www.w3.org/2000/svg"></svg>',error:'<svg xmlns="http://www.w3.org/2000/svg"></svg>',info:'<svg xmlns="http://www.w3.org/2000/svg"></svg>',warning:'<svg xmlns="http://www.w3.org/2000/svg"></svg>'}`,
		},
		{
			"escaped quote in string",
			`var x = 'a\'b'; var y = 2;`,
			`var x = 'a\'b'; var y = 2;`,
		},
		{
			"escaped backslash then quote",
			`var x = 'a\\\\'; /* c */ var y = 2;`,
			`var x = 'a\\\\';  var y = 2;`,
		},
		{
			"real comment after string",
			"var x = 'http://a'; // real comment\nvar y = 2;",
			"var x = 'http://a'; \nvar y = 2;",
		},
		{
			"comment-like content inside string",
			"var x = '// not a comment'; var y = '/* also not */';",
			"var x = '// not a comment'; var y = '/* also not */';",
		},
		{
			"block comment inside string",
			"var x = '/* inside */'; /* outside */ var y = 2;",
			"var x = '/* inside */';  var y = 2;",
		},
		{
			"template literal with slashes",
			"var x = `http://a.com/b`;",
			"var x = `http://a.com/b`;",
		},
		{
			"template literal containing comment markers",
			"var x = `// foo /* bar */ baz`;",
			"var x = `// foo /* bar */ baz`;",
		},
		{
			"nested-looking comment in string",
			"var x = '/* /* /* */'; // real\n",
			"var x = '/* /* /* */'; \n",
		},
		{
			"slash not followed by slash or star",
			"var x = 1 / 2; var y = 3;",
			"var x = 1 / 2; var y = 3;",
		},
		{
			"empty line comment",
			"a;//\nb",
			"a;\nb",
		},
		{
			"empty block comment",
			"a;/**/b",
			"a;b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripJSComments(tt.in); got != tt.want {
				t.Errorf("stripJSComments(%q)\n  got:  %q\n  want: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMinifyJS_EndToEnd(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"line comment removed, whitespace collapsed",
			"var x = 1; // trailing\nvar y = 2;",
			"var x = 1; var y = 2;",
		},
		{
			"// in url preserved",
			`var x = "http://example.com/path";`,
			`var x = "http://example.com/path";`,
		},
		{
			"svg // in attribute preserved",
			`var s = '<svg xmlns="http://www.w3.org/2000/svg"></svg>';`,
			`var s = '<svg xmlns="http://www.w3.org/2000/svg"></svg>';`,
		},
		{
			"block comment removed",
			"a; /* x */ b;",
			"a; b;",
		},
		{
			"multiple line comments",
			"a; // one\nb; // two\nc;",
			"a; b; c;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := minifyJS(tt.in); got != tt.want {
				t.Errorf("minifyJS(%q)\n  got:  %q\n  want: %q", tt.in, got, tt.want)
			}
		})
	}
}
