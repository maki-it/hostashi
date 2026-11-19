// SPDX-License-Identifier: AGPL-3.0-or-later

package snippet

import (
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/internal/nginxconf"
)

// bareRe are arguments that nginx reads the same with or without quotes.
var bareRe = regexp.MustCompile(`^[A-Za-z0-9_./:$~^*=@%+,!?()\[\]|&<>-]+$`)

// Render writes checked directives as nginx text, indented by indent. Each
// argument that is not plainly safe is double-quoted, with backslash and
// double quote escaped, so that nginx reads back exactly the checked value.
// Comments of the source are not kept.
func Render(ds []nginxconf.Directive, indent string) string {
	var b strings.Builder

	render(&b, ds, indent)

	return b.String()
}

func render(b *strings.Builder, ds []nginxconf.Directive, indent string) {
	for _, d := range ds {
		b.WriteString(indent)
		b.WriteString(d.Name)

		for _, a := range d.Args {
			_ = b.WriteByte(' ')
			b.WriteString(quote(a))
		}

		if !d.IsBlock() {
			b.WriteString(";\n")

			continue
		}

		b.WriteString(" {\n")
		render(b, d.Block, indent+"    ")
		b.WriteString(indent)
		b.WriteString("}\n")
	}
}

func quote(a string) string {
	if bareRe.MatchString(a) {
		return a
	}

	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)

	return `"` + r.Replace(a) + `"`
}
