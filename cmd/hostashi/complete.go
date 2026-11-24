// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/maki-it/hostashi/api"
	"github.com/maki-it/hostashi/internal/apicli"
)

// Shell completion: "hostashi completion SHELL" prints a small script that
// asks "hostashi __complete WORD…" for the candidates, so the command tree
// lives here once for bash, zsh and fish. __complete never calls the API or
// the agent: the API areas and commands come from the embedded OpenAPI
// document, so completing works without login and costs no network call.

var (
	//go:embed completion/hostashi.bash
	bashCompletion string
	//go:embed completion/_hostashi
	zshCompletion string
	//go:embed completion/hostashi.fish
	fishCompletion string
)

// completionScripts maps a shell to its script; the installer writes each
// one into the shell's completion directory below /usr/local/share.
var completionScripts = map[string]string{"bash": bashCompletion, "zsh": zshCompletion, "fish": fishCompletion}

// filesDirective as the only line of __complete's output asks the shell
// for file name completion instead.
const filesDirective = ":files"

// compNode is one command or subcommand.
type compNode struct {
	name, help string
	// flags as the flag package takes them; a trailing "=" marks a flag
	// with a value. fileFlags take a local path as their value.
	flags, fileFlags []string
	sub              []*compNode
	// args are offered once argsFrom positional arguments were given.
	args     []string
	argsFrom int
	// hidden commands run as services (systemd units start them); they
	// work but are not offered.
	hidden bool
	// dynamic replaces sub and args: it gets the positional arguments
	// after this node and the current word.
	dynamic func(pos []string, cur string) []completion
}

type completion struct{ value, help string }

func cmds(nodes ...*compNode) []*compNode { return nodes }

func leaf(name, help string) *compNode { return &compNode{name: name, help: help} }

var limitKeys = []string{
	"preset=", "memory=", "tasks=", "cpu-priority=", "cpu-quota=", "disk=",
	"inodes=", "php-workers=", "db-size=", "db-connections=",
}

// completionTree follows the usage text; TestCompletionTreeMatchesUsage
// keeps the two in step.
var completionTree = &compNode{sub: cmds(
	&compNode{name: "login", help: "log in to the panel's API", flags: []string{"-token", "-name="}},
	leaf("logout", "forget the stored token"),
	leaf("whoami", "show the account and panel the CLI talks to"),
	&compNode{name: "api", help: "call any operation of the panel's API", dynamic: completeAPI},
	&compNode{name: "serve", hidden: true},
	&compNode{name: "agent", hidden: true},
	leaf("ping", "check that the agent answers"),
	&compNode{name: "doctor", help: "check the server and every site", flags: []string{"-json"}},
	&compNode{
		name: "service", help: "list, start, stop, restart or reload services",
		sub:  cmds(&compNode{name: "list", help: "show the managed services", flags: []string{"-json"}}),
		args: []string{"start", "stop", "restart", "reload"}, argsFrom: 1,
	},
	&compNode{name: "system", help: "time zone, hostname and reboot", sub: cmds(
		leaf("info", "show time zone, hostname and uptime"),
		leaf("timezones", "list the time zones"),
		leaf("timezone", "set the time zone"),
		leaf("hostname", "set the hostname"),
		leaf("reboot", "reboot after typing the hostname"),
	)},
	&compNode{name: "site", help: "create, delete and list sites", sub: cmds(
		leaf("create", "create a static site"),
		leaf("delete", "delete a site with its data"),
		leaf("list", "list the sites"),
		leaf("files", "start the file manager's socket of a site"),
		&compNode{name: "limits", help: "show or change a site's limits", args: limitKeys, argsFrom: 1},
	)},
	&compNode{name: "pool", help: "storage pools for site homes", sub: cmds(
		&compNode{name: "add", help: "add a mounted volume as a pool", flags: []string{"-default"}},
		leaf("remove", "remove a pool"),
		leaf("list", "list the pools"),
		leaf("rename", "give a pool a new name"),
		leaf("default", "make a pool the default for new sites"),
	)},
	&compNode{name: "web", help: "make the web state live", sub: cmds(leaf("apply", "read the web state as JSON from stdin"))},
	&compNode{name: "db", help: "databases and database users", sub: cmds(
		leaf("list", "list the databases"),
		leaf("create", "create a database for a site"),
		leaf("delete", "delete a database"),
		&compNode{name: "user", help: "database users", sub: cmds(
			leaf("create", "create a user for a database"),
			leaf("password", "set a user's password"),
			leaf("delete", "delete a user"),
		)},
		leaf("import", "import a .sql or .sql.gz file"),
		leaf("export", "export into a .sql or .sql.gz file"),
	)},
	&compNode{name: "backup", help: "backup settings, backups and restores", sub: cmds(
		&compNode{name: "run", hidden: true},
		leaf("get", "show the backup settings"),
		leaf("set", "set the backup settings from JSON on stdin"),
		leaf("hostkey", "set the storage's SSH host key from stdin"),
		leaf("start", "run a backup now"),
		leaf("status", "show the last backup"),
		leaf("restore", "restore a snapshot into a site's home"),
		leaf("restore-status", "show a restore"),
		&compNode{name: "restore-run", hidden: true},
		&compNode{name: "key", help: "the keys to keep outside the server", sub: cmds(leaf("show", "print the repository password and panel key"))},
	)},
	&compNode{name: "cloudpanel", help: "move sites from a CloudPanel server", sub: cmds(
		&compNode{
			name: "scan", help: "show which sites of a CloudPanel server can be imported",
			flags: []string{"-root=", "-json", "-full", "-strict=", "-no-usage"}, fileFlags: []string{"-root"},
		},
		leaf("database", "make a password for the read-only database user"),
		leaf("import", "copy a site's files and databases"),
		leaf("import-status", "show an import"),
		&compNode{name: "import-run", hidden: true},
	)},
	&compNode{name: "dbjob", hidden: true},
	&compNode{name: "node", help: "Node.js lines", sub: cmds(
		leaf("status", "the installed lines and the last installation"),
		leaf("install", "install an LTS line"),
	)},
	&compNode{name: "node-install", hidden: true},
	&compNode{name: "python", help: "Python lines", sub: cmds(
		leaf("status", "the installed lines and the last installation"),
		leaf("install", "install a line"),
		leaf("remove", "remove a line no site uses"),
	)},
	&compNode{name: "python-install", hidden: true},
	&compNode{name: "git-sync", hidden: true},
	&compNode{name: "user", help: "recover a locked-out account", sub: cmds(
		&compNode{name: "recover", help: "reset a user's password and second factors", flags: []string{"-public-url=", "-db="}},
	)},
	&compNode{name: "files-helper", hidden: true},
	leaf("setup-token", "print the one-time token for the first login"),
	&compNode{name: "version", help: "print the version", flags: []string{"-schema"}},
	&compNode{name: "completion", help: "print the shell completion script", sub: cmds(
		leaf("bash", "completion for bash"),
		leaf("zsh", "completion for zsh"),
		leaf("fish", "completion for fish"),
	)},
	leaf("help", "show the usage"),
)}

// completionCmd runs "hostashi completion SHELL".
func completionCmd(args []string, stdout, stderr io.Writer) int {
	if s, ok := completionScripts[strings.Join(args, " ")]; ok {
		fmt.Fprint(stdout, s)

		return 0
	}

	fmt.Fprint(stderr, "usage: hostashi completion bash|zsh|fish\n")

	return 2
}

// completeCmd runs "hostashi __complete WORD… CURRENT": one candidate per
// line, a tab and a short help after it for the shells that show one.
func completeCmd(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		args = []string{""}
	}

	for _, c := range complete(args[:len(args)-1], args[len(args)-1]) {
		if c.help == "" {
			fmt.Fprintln(stdout, c.value)
		} else {
			fmt.Fprintf(stdout, "%s\t%s\n", c.value, c.help)
		}
	}

	return 0
}

// complete returns the candidates for cur after the words before it.
func complete(before []string, cur string) []completion {
	n := completionTree

	var pos []string

	for i := 0; i < len(before); i++ {
		w := before[i]

		switch {
		case w == "--":
			pos = append(pos, before[i+1:]...)
			i = len(before)
		case strings.HasPrefix(w, "-") && n.dynamic == nil:
			// "-name value": the value is not a positional argument.
			if !strings.Contains(w, "=") && hasFlag(n.flags, flagName(w)+"=") {
				i++
			}
		case len(pos) == 0 && child(n, w) != nil:
			n = child(n, w)
		default:
			pos = append(pos, w)
		}
	}

	if n.dynamic != nil {
		return n.dynamic(pos, cur)
	}

	if len(before) > 0 {
		if prev := before[len(before)-1]; strings.HasPrefix(prev, "-") && !strings.Contains(prev, "=") && hasFlag(n.flags, flagName(prev)+"=") {
			return flagValue(n, flagName(prev))
		}
	}

	if name, _, ok := strings.Cut(cur, "="); ok && strings.HasPrefix(cur, "-") {
		return flagValue(n, flagName(name))
	}

	var out []completion

	if strings.HasPrefix(cur, "-") {
		for _, f := range n.flags {
			out = appendPrefixed(out, cur, f, "")
		}

		return out
	}

	if len(pos) == 0 {
		for _, s := range n.sub {
			if !s.hidden {
				out = appendPrefixed(out, cur, s.name, s.help)
			}
		}
	}

	if len(pos) >= n.argsFrom {
		for _, a := range n.args {
			out = appendPrefixed(out, cur, a, "")
		}
	}

	return out
}

func child(n *compNode, name string) *compNode {
	for _, s := range n.sub {
		if s.name == name {
			return s
		}
	}

	return nil
}

func flagName(w string) string {
	name, _, _ := strings.Cut(strings.TrimLeft(w, "-"), "=")

	return "-" + name
}

func hasFlag(flags []string, f string) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}

	return false
}

// flagValue completes the value of a flag: a local path for fileFlags,
// nothing otherwise.
func flagValue(n *compNode, flag string) []completion {
	if hasFlag(n.fileFlags, flag) {
		return []completion{{value: filesDirective}}
	}

	return nil
}

func appendPrefixed(out []completion, cur, value, help string) []completion {
	if strings.HasPrefix(value, cur) {
		out = append(out, completion{value: value, help: help})
	}

	return out
}

// completeAPI completes "hostashi api AREA COMMAND ARGS…" from the embedded
// OpenAPI document.
func completeAPI(pos []string, cur string) []completion {
	sp, err := apicli.Load(api.Spec)
	if err != nil {
		return nil
	}

	var out []completion

	switch len(pos) {
	case 0:
		for _, t := range sp.Tags() {
			out = appendPrefixed(out, cur, t, fmt.Sprintf("%d commands", len(sp.InTag(t))))
		}

		return out
	case 1:
		for _, op := range sp.InTag(pos[0]) {
			out = appendPrefixed(out, cur, op.Verb, op.Summary)
		}

		return out
	}

	op := sp.Find(pos[0], pos[1])
	if op == nil {
		return nil
	}

	args := pos[2:]
	if len(args) > 0 && args[len(args)-1] == "-d" {
		return []completion{{value: filesDirective}}
	}

	// Path parameters come first, in order; nothing to offer for them.
	given := 0

	for _, a := range args {
		if !strings.Contains(a, "=") && !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "@") {
			given++
		}
	}

	if given < len(op.PathParams) && !strings.HasPrefix(cur, "-") {
		return nil
	}

	if strings.Contains(cur, "=") {
		return nil
	}

	if op.BodyType != "" && (len(op.Fields) == 0 || op.BodyType == "application/octet-stream") {
		out = appendPrefixed(out, cur, "-d", "request body: JSON, @FILE or - for stdin")
	}

	out = appendPrefixed(out, cur, "-h", "explain this command")

	for _, q := range op.Query {
		out = appendPrefixed(out, cur, q.Name+"=", q.Description)
	}

	for _, f := range op.Fields {
		t := f.Type
		if t == "" {
			t = "JSON"
		}

		out = appendPrefixed(out, cur, f.Name+"=", t)
	}

	return out
}
