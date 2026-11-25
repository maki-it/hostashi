// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// PreviewDir is the folder of every placeholder path in a preview: the
// site's home, its PHP socket, certificate, caches, logs and password
// file. The real paths are known to the agent only.
const PreviewDir = "/placeholder"

// ErrPreview means the site cannot be previewed: the preview is for
// static and PHP sites.
var ErrPreview = errors.New("web: the server block preview is for static and PHP sites")

// previewCtl refuses everything: a preview never runs nginx.
type previewCtl struct{}

var errPreviewCtl = errors.New("web: a preview does not run nginx")

func (previewCtl) Test(context.Context, string) error { return errPreviewCtl }
func (previewCtl) Reload(context.Context) error       { return errPreviewCtl }
func (previewCtl) Restart(context.Context) error      { return errPreviewCtl }

// previewNginx is the driver of a preview: the default listeners and
// placeholder paths. HTTP/3 and Brotli are left out, they do not change
// the site's own rules.
var previewNginx = Nginx{
	Ctl:           previewCtl{},
	Listen:        []string{"80", "[::]:80"},
	ListenTLS:     []string{"443", "[::]:443"},
	ControlSocket: PreviewDir + "/run/control.sock",
	PIDFile:       PreviewDir + "/run/nginx.pid",
	ErrorLog:      PreviewDir + "/log/error.log",
	SiteLogDir:    PreviewDir + "/log",
	MimeTypes:     PreviewDir + "/mime.types",
	ACMERoot:      PreviewDir + "/acme",
	AuthDir:       PreviewDir + "/auth",
}

// Preview renders the server block of one static or PHP site with the
// same templates and checks as the agent (ADR-0069), from the state the
// panel sends, with placeholder paths below PreviewDir. The site is shown
// as it is served: maintenance and the disabled state are left out.
// Password hashes never reach the output: the password file is named
// after an empty user list. Custom snippets that point at the real home
// do not fit the placeholder home; they are left out with a note.
func Preview(ws agentapi.WebSite) (string, error) {
	if ws.Redirect != nil || ws.Proxy != nil || ws.App != nil {
		return "", ErrPreview
	}

	home := PreviewDir + "/home/" + ws.User
	root := home + "/htdocs"

	if ws.WebDir != "" {
		if err := agentapi.ValidateWebDir(ws.WebDir); err != nil {
			return "", fmt.Errorf("%w: %w", ErrInvalidDesired, err)
		}

		root += "/" + ws.WebDir
	}

	s := Site{
		User: ws.User, Domains: ws.Domains, Root: root, WWWRedirect: ws.WWWRedirect, RedirectDomains: ws.RedirectDomains,
		SPA: ws.SPA, AssetCache: ws.AssetCache, AssetCacheSeconds: ws.AssetCacheSeconds, AssetCacheExts: ws.AssetCacheExts,
	}

	if ws.TLS {
		s.Cert, s.Key = PreviewDir+"/certs/"+ws.User+"/fullchain.pem", PreviewDir+"/certs/"+ws.User+"/privkey.pem"
		s.HSTS = ws.HSTS
	}

	if ws.PHP != "" {
		s.PHPSocket = PreviewDir + "/php/" + ws.User + ".sock"

		var ps agentapi.PHPSettings
		if ws.PHPSettings != nil {
			ps = *ws.PHPSettings
		}

		s.BodyMax, s.FastCGIReadTimeout = ps.ClientMaxBodySize(), ps.FastCGIReadTimeout()

		if c := ws.PageCache; c != nil {
			s.PageCache = &PageCache{Dir: PreviewDir + "/cache/" + ws.User, Preset: c.Preset, Minutes: c.Minutes, SizeMB: c.SizeMB, Exclude: c.Exclude}
		}
	}

	if ws.Headers != nil {
		s.Headers = *ws.Headers
	}

	if ws.Hardening != nil {
		s.Hardening = *ws.Hardening
	}

	if ws.RealIP != nil {
		s.RealIP = *ws.RealIP
	}

	if ws.Access != nil {
		s.Access = *ws.Access
	}

	var notes []string

	if sn := ws.Snippets; sn != nil {
		s.Home = home
		s.Snippets = Snippets{Server: sn.Server, Root: sn.Root}

		if _, _, err := s.checkSnippets(); err != nil {
			s.Home, s.Snippets = "", Snippets{}

			notes = append(notes, "The custom snippets are left out: they do not fit the placeholder paths.")
		}
	}

	d := Desired{Sites: []Site{s}}
	if err := d.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidDesired, err)
	}

	// Only now: the check above needs the users.
	if a := s.Access.Auth; a != nil {
		auth := *a
		auth.Users = nil
		d.Sites[0].Access.Auth = &auth
	}

	n := previewNginx

	f, err := n.Render(d, 0)
	if err != nil {
		return "", err
	}

	var b strings.Builder

	b.WriteString("# Preview of the server block of site " + s.User + " (read only).\n")
	b.WriteString("# Paths below " + PreviewDir + " are placeholders; the server uses its own.\n")

	for _, note := range notes {
		b.WriteString("# " + note + "\n")
	}

	b.WriteString("\n")
	b.WriteString(string(f.Content["sites/"+s.User+".conf"]))

	names := []string{}

	for name := range f.Content {
		if strings.HasPrefix(name, "snippets/") {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	for _, name := range names {
		b.WriteString("\n# File " + name + ":\n")
		b.WriteString(string(f.Content[name]))
	}

	return b.String(), nil
}
