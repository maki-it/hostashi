// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func values(cs []completion) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.value)
	}

	return out
}

func TestComplete(t *testing.T) {
	cases := []struct {
		name    string
		words   []string
		want    []string // exactly these, in order
		has     []string // at least these
		hasNot  []string
		wantNil bool
	}{
		{name: "top level by prefix", words: []string{"si"}, want: []string{"site"}},
		{name: "top level hides service commands", words: []string{""}, has: []string{"login", "api", "site", "completion"}, hasNot: []string{"serve", "agent", "files-helper", "dbjob", "node-install", "python-install", "__complete"}},
		{name: "subcommands", words: []string{"db", ""}, want: []string{"list", "create", "delete", "user", "import", "export"}},
		{name: "nested subcommands", words: []string{"db", "user", "p"}, want: []string{"password"}},
		{name: "hidden subcommands", words: []string{"backup", "r"}, want: []string{"restore", "restore-status"}},
		{name: "nothing after a leaf", words: []string{"site", "list", ""}, wantNil: true},
		{name: "no subcommand after an argument", words: []string{"site", "limits", ""}, wantNil: true},
		{name: "limit keys after the site", words: []string{"site", "limits", "blog", "c"}, want: []string{"cpu-priority=", "cpu-quota="}},
		{name: "limit keys after other keys", words: []string{"site", "limits", "blog", "memory=1G", "d"}, want: []string{"disk=", "db-size=", "db-connections="}},
		{name: "service actions after the id", words: []string{"service", "nginx", "re"}, want: []string{"restart", "reload"}},
		{name: "service actions after id and site", words: []string{"service", "php", "blog", "st"}, want: []string{"start", "stop"}},
		{name: "service list flag", words: []string{"service", "list", "-"}, want: []string{"-json"}},
		{name: "flags", words: []string{"cloudpanel", "scan", "-"}, want: []string{"-root=", "-json", "-full", "-strict=", "-no-usage"}},
		{name: "flags by prefix with two dashes", words: []string{"doctor", "--j"}, wantNil: true},
		{name: "file flag value", words: []string{"cloudpanel", "scan", "-root", ""}, want: []string{filesDirective}},
		{name: "file flag value after equals", words: []string{"cloudpanel", "scan", "-root=/mn"}, want: []string{filesDirective}},
		{name: "other flag value", words: []string{"login", "-name", ""}, wantNil: true},
		{name: "flag value is not an argument", words: []string{"login", "-name", "laptop", "-"}, want: []string{"-token", "-name="}},
		{name: "completion shells", words: []string{"completion", ""}, want: []string{"bash", "zsh", "fish"}},
		{name: "unknown command", words: []string{"exec", ""}, wantNil: true},
		{name: "api areas", words: []string{"api", "si"}, want: []string{"sites"}},
		{name: "api commands", words: []string{"api", "sites", "get"}, has: []string{"get", "get-app"}, hasNot: []string{"list"}},
		{name: "api unknown area", words: []string{"api", "nope", ""}, wantNil: true},
		{name: "api unknown command", words: []string{"api", "sites", "nope", ""}, wantNil: true},
		{name: "api path parameter first", words: []string{"api", "sites", "get", ""}, wantNil: true},
		{name: "api fields", words: []string{"api", "sites", "create", "na"}, want: []string{"name="}},
		{name: "api fields after the path parameter", words: []string{"api", "sites", "delete", "blog", "-"}, has: []string{"-h"}},
		{name: "api value", words: []string{"api", "sites", "create", "name=bl"}, wantNil: true},
		{name: "api body file", words: []string{"api", "backups", "set-settings", "-d", ""}, want: []string{filesDirective}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := values(complete(tc.words[:len(tc.words)-1], tc.words[len(tc.words)-1]))

			switch {
			case tc.wantNil:
				if len(got) != 0 {
					t.Fatalf("got %q, want nothing", got)
				}
			case tc.want != nil:
				if !slices.Equal(got, tc.want) {
					t.Fatalf("got %q, want %q", got, tc.want)
				}
			}

			for _, h := range tc.has {
				if !slices.Contains(got, h) {
					t.Errorf("%q missing in %q", h, got)
				}
			}

			for _, h := range tc.hasNot {
				if slices.Contains(got, h) {
					t.Errorf("%q offered in %q", h, got)
				}
			}
		})
	}
}

// Every command and subcommand of the usage text can be completed, and the
// completion offers nothing the usage does not name.
func TestCompletionTreeMatchesUsage(t *testing.T) {
	word := regexp.MustCompile(`^[a-z][a-z-]*$`)
	named := map[string]bool{"help": true}

	for _, line := range strings.Split(usage, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
			continue
		}

		for _, alt := range strings.Split(strings.TrimSpace(line), " | ") {
			n := completionTree

			for _, w := range strings.Fields(alt) {
				if !word.MatchString(w) || n.hidden || (n != completionTree && len(n.sub) == 0) {
					break
				}

				c := child(n, w)
				if c == nil {
					t.Errorf("usage names %q, the completion has no %q below %q", alt, w, n.name)

					break
				}

				if n == completionTree {
					named[w] = true
				}

				n = c
			}
		}
	}

	for _, c := range completionTree.sub {
		if !named[c.name] {
			t.Errorf("completion offers %q, the usage does not name it", c.name)
		}
	}
}

func TestCompletionCommands(t *testing.T) {
	for shell, marker := range map[string]string{"bash": "complete -F _hostashi hostashi", "zsh": "#compdef hostashi", "fish": "complete -c hostashi"} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), []string{"completion", shell}, 1000, &out, &errOut); code != 0 {
			t.Fatalf("completion %s: exit %d: %s", shell, code, &errOut)
		}

		if !strings.Contains(out.String(), marker) || !strings.Contains(out.String(), "__complete") {
			t.Errorf("completion %s does not look like the %s script:\n%s", shell, shell, &out)
		}
	}

	for _, args := range [][]string{{"completion"}, {"completion", "tcsh"}, {"completion", "bash", "zsh"}} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), args, 1000, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage") {
			t.Errorf("%q: exit %d, stderr %q, want 2 with usage", args, code, &errOut)
		}
	}

	var out bytes.Buffer
	if code := run(context.Background(), []string{"__complete", "api", "sites", "create", "na"}, 1000, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("__complete: exit %d", code)
	}

	if out.String() != "name=\tstring\n" {
		t.Errorf("__complete printed %q", &out)
	}

	out.Reset()

	if run(context.Background(), []string{"__complete"}, 1000, &out, &bytes.Buffer{}) != 0 || !strings.Contains(out.String(), "login\t") {
		t.Errorf("__complete without words printed %q, want the commands", &out)
	}
}
