// SPDX-License-Identifier: AGPL-3.0-or-later

package statuspage

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type entry struct {
	name string
	body string
	mode fs.FileMode
}

func zipOf(t testing.TB, entries ...entry) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}

		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestParseSingleHTML(t *testing.T) {
	files, err := Parse("Wartung.HTML", []byte("<h1>x</h1>"))
	if err != nil || len(files) != 1 || string(files["index.html"]) != "<h1>x</h1>" {
		t.Fatalf("%v %v", files, err)
	}

	for name, data := range map[string]string{"x.txt": "a", "x.html": "", "x.php": "<?php"} {
		if _, err := Parse(name, []byte(data)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseZip(t *testing.T) {
	files, err := Parse("page.zip", zipOf(t,
		entry{name: "css/", mode: fs.ModeDir | 0o755},
		entry{name: "index.html", body: "<h1>x</h1>"},
		entry{name: "css/site.css", body: "h1{}"},
		entry{name: "img/a/logo.png", body: "png"},
	))
	if err != nil || len(files) != 3 || string(files["css/site.css"]) != "h1{}" {
		t.Fatalf("%v %v", files, err)
	}

	// A zipped folder works too.
	files, err = Parse("page.zip", zipOf(t,
		entry{name: "mypage/index.html", body: "x"},
		entry{name: "mypage/css/site.css", body: "y"},
	))
	if err != nil || string(files["index.html"]) != "x" || string(files["css/site.css"]) != "y" {
		t.Fatalf("folder: %v %v", files, err)
	}
}

func TestParseZipRefuses(t *testing.T) {
	many := []entry{{name: "index.html", body: "x"}}
	for i := range agentapi.MaxPageFiles {
		many = append(many, entry{name: "f" + strings.Repeat("a", i%50) + string(rune('a'+i/50)) + ".css", body: "x"})
	}

	bomb := zipOf(t, entry{name: "index.html", body: "x"}, entry{name: "big.css", body: strings.Repeat("a", agentapi.MaxPageBytes)})

	for name, data := range map[string][]byte{
		"traversal":      zipOf(t, entry{name: "index.html", body: "x"}, entry{name: "../evil.html", body: "x"}),
		"absolute":       zipOf(t, entry{name: "index.html", body: "x"}, entry{name: "/etc/x.html", body: "x"}),
		"hidden":         zipOf(t, entry{name: "index.html", body: "x"}, entry{name: ".htaccess.css", body: "x"}),
		"backslash":      zipOf(t, entry{name: "index.html", body: "x"}, entry{name: "a\\b.css", body: "x"}),
		"too deep":       zipOf(t, entry{name: "index.html", body: "x"}, entry{name: "a/b/c/d.css", body: "x"}),
		"wrong type":     zipOf(t, entry{name: "index.html", body: "x"}, entry{name: "shell.php", body: "x"}),
		"symlink":        zipOf(t, entry{name: "index.html", body: "x"}, entry{name: "link.css", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}),
		"no index":       zipOf(t, entry{name: "start.html", body: "x"}),
		"index too deep": zipOf(t, entry{name: "a/b/index.html", body: "x"}),
		"duplicate":      zipOf(t, entry{name: "index.html", body: "x"}, entry{name: "index.html", body: "y"}),
		"too many":       zipOf(t, many...),
		"bomb":           bomb,
		"broken":         []byte("PK\x03\x04garbage"),
	} {
		if _, err := Parse("page.zip", data); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if len(bomb) > 64<<10 {
		t.Fatalf("bomb is %d bytes, the test wants a small upload", len(bomb))
	}
}

func TestBuiltin(t *testing.T) {
	for id, kind := range map[string]string{BuiltinMaintenance: KindMaintenance, BuiltinParking: KindParking} {
		files, k, ok := Builtin(id)
		if !ok || k != kind || !bytes.Contains(files["index.html"], []byte("<!doctype html>")) || bytes.Contains(files["index.html"], []byte("<script")) {
			t.Errorf("%s: %v %s", id, ok, k)
		}

		if err := agentapi.ValidatePageID(id); err != nil {
			t.Error(err)
		}
	}

	if _, _, ok := Builtin("p1"); ok {
		t.Error("p1 is not built in")
	}
}

func FuzzParse(f *testing.F) {
	f.Add("x.zip", zipOf(f, entry{name: "index.html", body: "x"}, entry{name: "css/a.css", body: "y"}))
	f.Add("x.zip", zipOf(f, entry{name: "../index.html", body: "x"}))
	f.Add("x.html", []byte("<h1>x</h1>"))

	f.Fuzz(func(t *testing.T, name string, data []byte) {
		files, err := Parse(name, data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error without ErrInvalid: %v", err)
			}

			return
		}

		total := 0

		for p, b := range files {
			if err := agentapi.ValidatePagePath(p); err != nil {
				t.Fatalf("accepted %q: %v", p, err)
			}

			total += len(b)
		}

		if _, ok := files[agentapi.PageIndex]; !ok || len(files) > agentapi.MaxPageFiles || total > agentapi.MaxPageBytes {
			t.Fatalf("accepted %d files, %d bytes", len(files), total)
		}
	})
}
