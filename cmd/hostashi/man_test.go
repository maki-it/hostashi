// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

var updateMan = flag.Bool("update", false, "rewrite packaging/man/hostashi.1 from the usage text")

const manFile = "../../packaging/man/hostashi.1"

// TestManPage keeps the shipped page in step with the usage text.
func TestManPage(t *testing.T) {
	got := manPage()

	if *updateMan {
		if err := os.WriteFile(manFile, got, 0o644); err != nil { //nolint:gosec // a page in the source tree, readable by all
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(manFile)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run: go test ./cmd/hostashi -run TestManPage -update", manFile)
	}
}

// TestManPageNamesEveryCommand: every command of the usage text gets an
// entry, and the entries keep their description.
func TestManPageNamesEveryCommand(t *testing.T) {
	entries := parseUsage(usage)
	if len(entries) < 30 {
		t.Fatalf("only %d entries parsed from the usage text", len(entries))
	}

	page := string(manPage())

	for _, c := range completionTree.sub {
		if c.name == "help" { // completes, the usage text does not list it
			continue
		}

		if !strings.Contains(page, `\fB`+strings.ReplaceAll(c.name, "-", `\-`)) {
			t.Errorf("the page has no entry for %q", c.name)
		}
	}

	for _, e := range entries {
		if len(e.help) == 0 {
			t.Errorf("entry %q has no description", e.forms)
		}
	}
}

func TestParseUsage(t *testing.T) {
	text := `usage: x

commands:
  one       first
  two [-f]
            second, line one
            line two
  a list | a get NAME |
  a set NAME
            all of a
  b run   inline after
            more
`

	got := parseUsage(text)
	want := []manCommand{
		{forms: []string{"one"}, help: []string{"first"}},
		{forms: []string{"two [-f]"}, help: []string{"second, line one", "line two"}},
		{forms: []string{"a list | a get NAME | a set NAME"}, help: []string{"all of a"}},
		{forms: []string{"b run"}, help: []string{"inline after", "more"}},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d entries %q, want %d", len(got), got, len(want))
	}

	for i := range want {
		if strings.Join(got[i].forms, "\n") != strings.Join(want[i].forms, "\n") ||
			strings.Join(got[i].help, "\n") != strings.Join(want[i].help, "\n") {
			t.Errorf("entry %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestManPageEscapes: usage text that looks like Markdown or roff comes
// out as text, and options keep a minus sign that can be copied.
func TestManPageEscapes(t *testing.T) {
	page := string(manPage())

	for _, want := range []string{
		`hostashi \- hosting control panel`,
		`\fBdoctor [\-json]\fP`,
		`restore\-<time>`,
		`{"sites":[{"user":"blog"`,
		`REMOTE_USER`,
		`ARGS...`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	for i, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, ".") || strings.HasPrefix(line, "'") {
			continue
		}

		for j := 0; j < len(line); j++ {
			if line[j] == '-' && (j == 0 || line[j-1] != '\\') {
				t.Fatalf("line %d has a bare hyphen: %q", i+1, line)
			}
		}
	}
}

func TestManCmd(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(t.Context(), []string{"__man"}, 1000, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	if !strings.Contains(out.String(), ".TH HOSTASHI 1") {
		t.Errorf("no title header:\n%s", out.String())
	}

	if code := run(t.Context(), []string{"__man", "extra"}, 1000, &out, &errb); code != 2 {
		t.Errorf("extra argument: exit %d, want 2", code)
	}
}
