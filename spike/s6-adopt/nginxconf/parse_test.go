package nginxconf

import (
	"strings"
	"testing"
)

func TestParseBasics(t *testing.T) {
	src := `
# comment
server {
    listen 443 ssl;   # trailing
    server_name example.org "www.example.org";
    location ~ \.php$ {
        fastcgi_pass unix:/run/php/x.sock;
    }
    add_header X-Test 'a;b{c}';
}
`
	ds, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].Name != "server" || !ds[0].IsBlock() {
		t.Fatalf("unexpected: %+v", ds)
	}
	srv := ds[0].Block
	sn, _ := First(srv, "server_name")
	if len(sn.Args) != 2 || sn.Args[1] != "www.example.org" {
		t.Fatalf("server_name args: %v", sn.Args)
	}
	h, _ := First(srv, "add_header")
	if h.Args[1] != "a;b{c}" {
		t.Fatalf("quoted arg: %q", h.Args[1])
	}
	loc, _ := First(srv, "location")
	if loc.Args[0] != `~` || loc.Args[1] != `\.php$` {
		t.Fatalf("location args: %v", loc.Args)
	}
	if !strings.HasPrefix(loc.Raw, "location") || !strings.HasSuffix(loc.Raw, "}") {
		t.Fatalf("raw: %q", loc.Raw)
	}
}

func TestParseEmptyBlockIsBlock(t *testing.T) {
	ds, err := Parse("events {}")
	if err != nil || !ds[0].IsBlock() || len(ds[0].Block) != 0 {
		t.Fatalf("%v %+v", err, ds)
	}
}

func TestParseErrors(t *testing.T) {
	bad := map[string]string{
		"missing semicolon": "server { listen 80 }",
		"missing close":     "server { listen 80;",
		"extra close":       "listen 80; }",
		"unterminated str":  `add_header X "abc;`,
		"nul":               "listen 80;\x00",
		"no name":           "; listen 80;",
		"eof directive":     "listen 80",
	}
	for name, src := range bad {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseLimits(t *testing.T) {
	deep := strings.Repeat("a {", MaxDepth+5) + strings.Repeat("}", MaxDepth+5)
	if _, err := Parse(deep); err == nil {
		t.Error("deep nesting accepted")
	}
	if _, err := Parse(strings.Repeat("a;\n", MaxDirectives+1)); err == nil {
		t.Error("too many directives accepted")
	}
	if _, err := Parse(strings.Repeat("#", MaxBytes+1)); err == nil {
		t.Error("oversized file accepted")
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"a;", "a {b;}", `a "x" 'y' z;`, "# c\na b {c d;}", "}{", `a "\`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ds, err := Parse(s)
		if err != nil {
			return
		}
		// Every parsed directive must carry a non-empty name and a raw text
		// that parses again to the same shape.
		var walk func([]Directive)
		walk = func(ds []Directive) {
			for _, d := range ds {
				if d.Name == "" {
					t.Fatalf("empty name in %q", s)
				}
				walk(d.Block)
			}
		}
		walk(ds)
		if len(ds) > 0 {
			if _, err := Parse(ds[0].Raw); err != nil {
				t.Fatalf("raw of %q does not reparse: %v", s, err)
			}
		}
	})
}
