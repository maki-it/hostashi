// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/cpuguy83/go-md2man/v2/md2man"
)

// The manual page hostashi(1) is made from the usage text, so the two
// cannot drift apart: "hostashi __man" prints it, the .deb ships the copy in
// packaging/man/hostashi.1 and TestManPage fails when that copy is stale
// (go test ./cmd/hostashi -run TestManPage -update writes it again).

// manIntro is the part of the page around the command list.
const manIntro = `HOSTASHI 1 "" "Hostashi" "Hostashi Manual"
=============================================

# NAME
hostashi \- hosting control panel for a single Linux server

# SYNOPSIS
**hostashi** *command* [*flags*] [*arguments*]

# DESCRIPTION
Hostashi runs websites with nginx, PHP-FPM, Node.js, Python and MariaDB or
PostgreSQL on one Debian or Ubuntu server. The same binary is the
unprivileged web process (**serve**), the root agent that performs typed
operations (**agent**), the helpers that systemd units start, and the
command line client described here.

Most commands talk to the panel's API and need **hostashi login** first,
also as root. Commands marked "root" in their description talk to the
agent directly and only work as root on the server. Run a command with
**-h** for its flags.

# COMMANDS
`

const manOutro = `
# ENVIRONMENT
HOSTASHI_URL
: Address of the panel's API; overrides the stored login.

HOSTASHI_TOKEN
: API token; overrides the stored login.

XDG_CONFIG_HOME
: Directory of the stored login, by default *~/.config*.

# FILES
*~/.config/hostashi/credentials.json*
: The panel address and API token that **hostashi login** stores (mode 0600).

*/usr/share/hostashi/install.sh*
: The installer that sets up the server and runs again on every package upgrade.

*/etc/hostashi/install.options*
: The options the installer ran with; upgrades reuse them.

# EXIT STATUS
**0** on success, **1** when the command failed, **2** for a usage error.
**hostashi doctor** exits with **3** when a check failed.

# SEE ALSO
**nginx**(8), **systemctl**(1), **restic**(1)

The documentation is at <https://github.com/maki-it/hostashi/wiki>.
`

// manCommand is one entry of the usage text: one or more lines of
// commands and their description.
type manCommand struct {
	forms []string
	help  []string
}

// parseUsage splits the command part of the usage text into entries. A
// line indented by two spaces names commands; text after two or more
// spaces on it, and every line indented deeper, is the description. A
// command line that ends in "|" continues on the next line.
func parseUsage(text string) []manCommand {
	_, list, _ := strings.Cut(text, "\ncommands:\n")

	var (
		entries []manCommand
		cur     *manCommand
	)

	for _, line := range strings.Split(list, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			continue
		case strings.HasPrefix(line, "   "):
			if cur != nil {
				cur.help = append(cur.help, strings.TrimSpace(line))
			}
		default:
			form, help, _ := strings.Cut(strings.TrimSpace(line), "  ")
			form, help = strings.TrimSpace(form), strings.TrimSpace(help)

			switch {
			case cur != nil && len(cur.help) == 0 && strings.HasSuffix(cur.forms[len(cur.forms)-1], "|"):
				cur.forms[len(cur.forms)-1] += " " + form
			case cur != nil && len(cur.help) == 0:
				cur.forms = append(cur.forms, form)
			default:
				entries = append(entries, manCommand{forms: []string{form}})
				cur = &entries[len(entries)-1]
			}

			if help != "" {
				cur.help = append(cur.help, help)
			}
		}
	}

	return entries
}

// mdEscape keeps Markdown from reading usage text as markup.
var mdEscape = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`,
	`<`, `\<`, `>`, `\>`, `#`, `\#`, `|`, `\|`, `…`, `...`,
)

// manPage renders hostashi(1) as roff.
func manPage() []byte {
	var b strings.Builder

	b.WriteString(manIntro)

	for _, e := range parseUsage(usage) {
		// A term of a definition list is one line: the forms of an entry
		// are alternatives, as in "db list | db create SITE NAME".
		fmt.Fprintf(&b, "**%s**\n: %s\n\n",
			mdEscape.Replace(strings.Join(e.forms, " | ")),
			mdEscape.Replace(strings.Join(e.help, " ")))
	}

	b.WriteString(manOutro)

	return append([]byte(manHeader), minusSigns(md2man.Render([]byte(b.String())))...)
}

// manHeader is a roff comment above the page.
const manHeader = `.\" SPDX-License-Identifier: AGPL-3.0-or-later
.\" Generated from the usage text by "hostashi __man" (cmd/hostashi/man.go).
.\" Do not edit; run: go test ./cmd/hostashi -run TestManPage -update
`

// minusSigns writes each "-" in the text as "\-", the roff minus sign:
// a plain "-" is a hyphen, which breaks copying options such as -json
// from the rendered page. Requests (lines starting with a dot) stay.
func minusSigns(roff []byte) []byte {
	lines := strings.Split(string(roff), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ".") || strings.HasPrefix(l, "'") {
			continue
		}

		var b strings.Builder

		for j := 0; j < len(l); j++ {
			if l[j] == '-' && (j == 0 || l[j-1] != '\\') {
				b.WriteByte('\\')
			}

			b.WriteByte(l[j])
		}

		lines[i] = b.String()
	}

	return []byte(strings.Join(lines, "\n"))
}

// manCmd prints the manual page; "hostashi __man | man -l -" shows it.
func manCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: hostashi __man")
		return 2
	}

	if _, err := stdout.Write(manPage()); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	return 0
}
