// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// own is a copy of WordPress with another name, as the panel's copy
// button makes it.
func own(t *testing.T) Profile {
	t.Helper()

	p, ok := Builtin(WordPress)
	if !ok {
		t.Fatal("no WordPress profile")
	}

	p.Name, p.Title = "agency-wp", "Agency WordPress"
	p.Cron = []Cron{{Schedule: "*/5 * * * *", Command: "php wp-cron.php", Note: "WordPress cron."}}

	return Normalize(p)
}

func TestCheckOwn(t *testing.T) {
	if err := Check(own(t)); err != nil {
		t.Fatal(err)
	}

	// Every built-in, renamed, passes as an own profile too.
	for _, b := range Builtins() {
		b.Name = "copy-" + b.Name
		if err := Check(Normalize(b)); err != nil {
			t.Errorf("%s: %v", b.Name, err)
		}
	}

	bad := map[string]func(p *Profile){
		"built-in name":   func(p *Profile) { p.Name = WordPress },
		"static name":     func(p *Profile) { p.Name = Static },
		"upper case":      func(p *Profile) { p.Name = "Agency" },
		"leading digit":   func(p *Profile) { p.Name = "1wp" },
		"long name":       func(p *Profile) { p.Name = strings.Repeat("a", MaxNameLen+1) },
		"name with slash": func(p *Profile) { p.Name = "a/b" },
		"empty title":     func(p *Profile) { p.Title = " " },
		"title newline":   func(p *Profile) { p.Title = "a\nb" },
		"long title":      func(p *Profile) { p.Title = strings.Repeat("x", MaxTitleLen+1) },
		"type":            func(p *Profile) { p.Type = "node" },
		"min php":         func(p *Profile) { p.MinPHP = "8" },
		"min php text":    func(p *Profile) { p.MinPHP = "8.2; root /" },
		"static min php": func(p *Profile) {
			p.Type, p.MinPHP, p.PageCache, p.Hardening.NoPHPDirs, p.Hardening.LimitFiles = "static", "8.2", "", nil, nil
		},
		"web dir up":        func(p *Profile) { p.WebDir = "../etc" },
		"web dir absolute":  func(p *Profile) { p.WebDir = "/etc" },
		"web dir nginx":     func(p *Profile) { p.WebDir = "public; include /etc/passwd" },
		"hardening path":    func(p *Profile) { p.Hardening.DenyFiles = []string{"a; }"} },
		"hardening limit":   func(p *Profile) { p.Hardening.LimitFiles = []string{"index.html"} },
		"hardening twice":   func(p *Profile) { p.Hardening.DenyDirs = []string{"a", "a"} },
		"static no php dir": func(p *Profile) { p.Type, p.MinPHP, p.PageCache = "static", "", "" },
		"page cache":        func(p *Profile) { p.PageCache = "everything" },
		"cron schedule":     func(p *Profile) { p.Cron[0].Schedule = "every minute" },
		"cron command":      func(p *Profile) { p.Cron[0].Command = "a\nb" },
		"cron count":        func(p *Profile) { p.Cron = make([]Cron, MaxCron+1) },
		"checklist long":    func(p *Profile) { p.Checklist = []string{strings.Repeat("x", MaxChecklistText+1)} },
		"checklist count":   func(p *Profile) { p.Checklist = make([]string, MaxChecklist+1) },
		"checklist control": func(p *Profile) { p.Checklist = []string{"a\x00b"} },
		"checklist empty":   func(p *Profile) { p.Checklist = []string{""} },
	}

	for name, change := range bad {
		p := own(t)
		change(&p)

		if err := Check(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestYAMLRoundTrip(t *testing.T) {
	want := own(t)

	out, err := ExportYAML(want)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(string(out), "version: 1\n") || !strings.Contains(string(out), "minPhp: \"7.4\"") {
		t.Errorf("export:\n%s", out)
	}

	got, err := ImportYAML(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
	}

	// A built-in exports too, and imports once it has its own name.
	b, _ := Builtin(Laravel)

	out, err = ExportYAML(b)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ImportYAML(out); !errors.Is(err, ErrInvalid) {
		t.Errorf("built-in name imported: %v", err)
	}

	p, err := ImportYAML([]byte(strings.Replace(string(out), "name: laravel", "name: my-laravel", 1)))
	if err != nil || p.WebDir != "public" || p.Builtin || len(p.Cron) != 1 {
		t.Errorf("renamed built-in: %+v %v", p, err)
	}
}

func TestImportYAMLRefusals(t *testing.T) {
	const base = "version: 1\nname: mine\ntitle: Mine\ntype: php\nhardening:\n  configFiles: true\n"

	if p, err := ImportYAML([]byte(base + "minPhp: 8.2\n")); err != nil || p.MinPHP != "8.2" || !p.Hardening.ConfigFiles {
		t.Fatalf("base: %+v %v", p, err)
	}

	bad := map[string]string{
		"no version":       strings.TrimPrefix(base, "version: 1\n"),
		"version 2":        strings.Replace(base, "version: 1", "version: 2", 1),
		"version text":     strings.Replace(base, "version: 1", "version: \"1\"", 1),
		"unknown field":    base + "nginx: |\n  location / { root /; }\n",
		"unknown nested":   base + "  include: /etc/nginx/x.conf\n",
		"builtin flag":     base + "builtin: true\n",
		"nginx in web dir": base + "webDir: \"public; root /etc\"\n",
		"nginx in path":    base + "  denyFiles: [\"x { deny all; }\"]\n",
		"two documents":    base + "---\n" + base,
		"duplicate key":    base + "name: other\n",
		"not a mapping":    "- version: 1\n",
		"syntax":           "version: 1\nname: [\n",
		"too large":        base + "checklist:\n  - " + strings.Repeat("x", MaxYAMLBytes) + "\n",
		"wrong type":       base + "checklist: yes\n",
	}

	for name, in := range bad {
		if p, err := ImportYAML([]byte(in)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %+v %v", name, p, err)
		}
	}
}
