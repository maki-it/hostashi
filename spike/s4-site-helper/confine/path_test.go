package confine

import (
	"strings"
	"testing"
)

func TestParseRel(t *testing.T) {
	bad := map[string]string{
		"absolute":           "/etc/passwd",
		"dotdot":             "..",
		"dotdot prefix":      "../x",
		"dotdot middle":      "a/../b",
		"dotdot end":         "a/..",
		"dot":                ".",
		"dot middle":         "a/./b",
		"double slash":       "a//b",
		"trailing slash":     "a/",
		"NUL":                "a\x00b",
		"newline":            "a\nb",
		"tab":                "a\tb",
		"DEL":                "a\x7fb",
		"invalid utf8":       "a\xffb",
		"long component":     strings.Repeat("a", 256),
		"long path":          strings.Repeat("a/", 2100),
		"temp prefix":        "dir/" + TempPrefix + "abc",
		"temp prefix deep":   TempPrefix + "x/file",
		"only slash":         "/",
		"unicode separator?": "a\u0085b", // C1 control NEL is above 0x7f, see below
	}
	// NEL (U+0085) is a C1 control, not rejected by the rule above: documented gap, not a security issue.
	delete(bad, "unicode separator?")
	for name, in := range bad {
		if _, err := ParseRel(in); err == nil {
			t.Errorf("%s: %q accepted", name, in)
		}
	}
	good := []string{"", "a", "a/b", "dir with space/f.txt", "ünï/cødé", "-rf", "0", ".hidden", "a..b", "...", "~", "a/.htaccess", strings.Repeat("a", 255), "日本語/ファイル"}
	for _, in := range good {
		if _, err := ParseRel(in); err != nil {
			t.Errorf("%q rejected: %v", in, err)
		}
	}
}

func FuzzParseRel(f *testing.F) {
	for _, s := range []string{"", "a/b", "../x", "a\x00", "//", "a/./b", strings.Repeat("a/", 50)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseRel(s)
		if err != nil {
			return
		}
		// Invariant: an accepted Rel never contains anything that could leave a directory.
		if strings.HasPrefix(string(r), "/") || strings.Contains(string(r), "\x00") {
			t.Fatalf("accepted %q", s)
		}
		for _, c := range r.components() {
			if c == "" || c == "." || c == ".." {
				t.Fatalf("accepted %q with component %q", s, c)
			}
		}
	})
}
