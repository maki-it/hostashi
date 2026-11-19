// SPDX-License-Identifier: AGPL-3.0-or-later

// Package snippetdraft turns the custom directives that the CloudPanel
// scanner kept for a site into a draft for the site's two snippet slots
// (ADR-0017 Part 2, ADR-0029). A person reviews the draft once before it is
// saved; saving goes through the agent's check like every other snippet.
//
// The draft drops what Hostashi renders itself (ACME, listen, TLS) or no
// longer ships (pagespeed), moves the body of a custom "location /" into the
// location / slot, points paths from the old document root and home at the
// new ones and fastcgi_pass at the site's own socket. Every directive is then
// checked on its own, so one rejected line does not take the others with it.
// Every entry of the scan is accounted for in Items.
package snippetdraft

import (
	"path"
	"strings"

	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/nginxconf"
	"github.com/maki-it/hostashi/internal/snippet"
)

// Action says what happened to one scanned entry.
type Action string

const (
	Kept      Action = "kept"
	Rewritten Action = "rewritten" // kept, with a path or the PHP socket changed
	Dropped   Action = "dropped"
)

// Item is the outcome for one directive of the scan.
type Item struct {
	Context string       `json:"context"`
	Source  string       `json:"source"`
	Line    int          `json:"line"`
	Slot    snippet.Slot `json:"slot,omitempty"`
	Action  Action       `json:"action"`
	Reason  string       `json:"reason,omitempty"`
}

// Result is the draft for one site.
type Result struct {
	Server string `json:"server"`
	Root   string `json:"root"`
	Items  []Item `json:"items"`
}

// Target is where the site lives on the new server.
type Target struct {
	// Home is the site's new home directory.
	Home string
	// WebRoot is the site's new document root, below Home.
	WebRoot string
	// PHPSocket is the site's own PHP-FPM socket, empty for no PHP.
	PHPSocket string
}

// Draft builds the snippet draft of one scanned site.
func Draft(s scan.Site, t Target) Result {
	b := &builder{
		t:       t,
		oldRoot: s.DocRoot,
		oldHome: "/home/" + s.User,
		newRoot: t.WebRoot,
		seen:    map[string]bool{},
		res:     Result{Items: []Item{}},
	}

	for _, c := range s.Custom {
		b.entry(c)
	}

	b.res.Server = snippet.Render(b.server, "")
	b.res.Root = snippet.Render(b.root, "")

	return b.res
}

type builder struct {
	t                         Target
	oldRoot, oldHome, newRoot string
	seen                      map[string]bool
	server, root              []nginxconf.Directive
	res                       Result
}

func (b *builder) item(c scan.Custom, slot snippet.Slot, a Action, reason string) {
	b.res.Items = append(b.res.Items, Item{Context: c.Context, Source: c.Source, Line: c.Line, Slot: slot, Action: a, Reason: reason})
}

func (b *builder) entry(c scan.Custom) {
	if c.Context == "server (redirect)" {
		b.item(c, "", Dropped, "redirects become typed redirects")

		return
	}

	// Front and backend blocks of CloudPanel often carry the same line.
	if b.seen[c.Raw] {
		b.item(c, "", Dropped, "same directive already in the draft")

		return
	}

	b.seen[c.Raw] = true

	ds, err := nginxconf.Parse(c.Raw)
	if err != nil {
		b.item(c, "", Dropped, "cannot be parsed: "+err.Error())

		return
	}

	if len(ds) != 1 {
		b.item(c, "", Dropped, "not a single directive")

		return
	}

	d := ds[0]

	if reason := dropReason(d); reason != "" {
		b.item(c, "", Dropped, reason)

		return
	}

	slot, body := snippet.SlotServer, ds

	if d.Name == "location" && len(d.Args) == 1 && d.Args[0] == "/" {
		// Hostashi owns location /; its body goes into the location /
		// slot, without the try_files Hostashi renders itself.
		slot, body = snippet.SlotRoot, nil

		for _, x := range d.Block {
			if x.Name != "try_files" {
				body = append(body, x)
			}
		}

		if len(body) == 0 {
			b.item(c, slot, Dropped, "only what Hostashi's location / already does")

			return
		}
	}

	changed := false
	for i := range body {
		changed = b.rewrite(&body[i]) || changed
	}

	text := snippet.Render(body, "")

	checked, err := snippet.Check(text, slot, snippet.Site{Home: b.t.Home, PHPSocket: b.t.PHPSocket})
	if err != nil {
		b.item(c, slot, Dropped, strings.TrimPrefix(err.Error(), "snippet: "))

		return
	}

	if slot == snippet.SlotRoot {
		b.root = append(b.root, checked...)
	} else {
		b.server = append(b.server, checked...)
	}

	if changed {
		b.item(c, slot, Rewritten, "paths or the PHP socket point at the new server")
	} else {
		b.item(c, slot, Kept, "")
	}
}

// dropReason names directives that are left out on purpose.
func dropReason(d nginxconf.Directive) string {
	switch {
	case strings.HasPrefix(d.Name, "pagespeed"):
		return "the PageSpeed module is not shipped (ADR-0017)"
	case d.Name == "listen" || d.Name == "server_name" || strings.HasPrefix(d.Name, "ssl_") || d.Name == "http2" || d.Name == "http3":
		return "Hostashi renders addresses, names and TLS itself"
	case d.Name == "try_files" || d.Name == "index" || d.Name == "root":
		return "Hostashi renders the document root and the site type's defaults"
	case d.Name == "location" && len(d.Args) > 0 && strings.Contains(d.Args[len(d.Args)-1], "/.well-known/acme-challenge"):
		return "Hostashi answers ACME challenges itself"
	}

	return ""
}

// rewrite moves paths of the old server to the new one and returns whether
// anything changed. A path that stays outside the new home is left alone,
// so the check rejects it.
func (b *builder) rewrite(d *nginxconf.Directive) bool {
	changed := false

	switch d.Name {
	case "root", "alias", "auth_basic_user_file":
		if len(d.Args) == 1 {
			if p, ok := b.movePath(d.Args[0]); ok {
				d.Args = []string{p}
				changed = true
			}
		}
	case "fastcgi_pass":
		if b.t.PHPSocket != "" && len(d.Args) == 1 && d.Args[0] != "unix:"+b.t.PHPSocket {
			d.Args = []string{"unix:" + b.t.PHPSocket}
			changed = true
		}
	}

	for i := range d.Block {
		changed = b.rewrite(&d.Block[i]) || changed
	}

	return changed
}

func (b *builder) movePath(p string) (string, bool) {
	slash := strings.HasSuffix(p, "/")
	clean := path.Clean(p)

	var out string

	switch {
	case b.oldRoot != "" && (clean == b.oldRoot || strings.HasPrefix(clean, b.oldRoot+"/")):
		out = b.newRoot + strings.TrimPrefix(clean, b.oldRoot)
	case clean == b.oldHome || strings.HasPrefix(clean, b.oldHome+"/"):
		out = b.t.Home + strings.TrimPrefix(clean, b.oldHome)
	default:
		return "", false
	}

	if slash && !strings.HasSuffix(out, "/") {
		out += "/"
	}

	return out, out != p
}
