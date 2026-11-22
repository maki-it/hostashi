// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/nginxconf"
)

// Security headers of a site (ADR-0056). The panel and the agent both check
// them with Validate; package web renders them.

// X-Frame-Options values.
const (
	FrameSameOrigin = "SAMEORIGIN"
	FrameDeny       = "DENY"
)

// ReferrerPolicies are the values of Referrer-Policy a site may choose.
var ReferrerPolicies = []string{
	"no-referrer", "no-referrer-when-downgrade", "origin", "origin-when-cross-origin",
	"same-origin", "strict-origin", "strict-origin-when-cross-origin", "unsafe-url",
}

// PermissionsPolicyStrict is the panel's "strict" preset of
// Permissions-Policy: the powerful features off for every origin.
const PermissionsPolicyStrict = "camera=(), microphone=(), geolocation=(), payment=(), usb=()"

// MaxHeaderValueBytes bounds an own header value (CSP, Permissions-Policy).
const MaxHeaderValueBytes = 4096

// Default security headers of a site created in the panel or the API
// (ADR-0056, Kim 2026-11-22). Existing and imported sites start with none.
var DefaultWebHeaders = WebHeaders{
	NoSniff:        true,
	FrameOptions:   FrameSameOrigin,
	ReferrerPolicy: "strict-origin-when-cross-origin",
}

// WebHeaders are the security headers of a site. The zero value sends none;
// Strict-Transport-Security stays the separate HSTS switch.
type WebHeaders struct {
	// NoSniff sends X-Content-Type-Options: nosniff.
	NoSniff bool `json:"noSniff,omitempty"`
	// FrameOptions is X-Frame-Options: "", FrameSameOrigin or FrameDeny.
	FrameOptions string `json:"frameOptions,omitempty"`
	// ReferrerPolicy is "" or one of ReferrerPolicies.
	ReferrerPolicy string `json:"referrerPolicy,omitempty"`
	// PermissionsPolicy is the value of Permissions-Policy, "" for none.
	PermissionsPolicy string `json:"permissionsPolicy,omitempty"`
	// CSP is the value of Content-Security-Policy, "" for none. With
	// CSPReportOnly it goes into Content-Security-Policy-Report-Only.
	CSP           string `json:"csp,omitempty"`
	CSPReportOnly bool   `json:"cspReportOnly,omitempty"`
}

// IsZero reports whether no header is switched on.
func (h WebHeaders) IsZero() bool { return h == WebHeaders{} }

// WebHeader is one rendered header: its name and value.
type WebHeader struct {
	Name, Value string
}

// List returns the switched-on headers in a fixed order.
func (h WebHeaders) List() []WebHeader {
	var l []WebHeader

	if h.NoSniff {
		l = append(l, WebHeader{"X-Content-Type-Options", "nosniff"})
	}

	if h.FrameOptions != "" {
		l = append(l, WebHeader{"X-Frame-Options", h.FrameOptions})
	}

	if h.ReferrerPolicy != "" {
		l = append(l, WebHeader{"Referrer-Policy", h.ReferrerPolicy})
	}

	if h.PermissionsPolicy != "" {
		l = append(l, WebHeader{"Permissions-Policy", h.PermissionsPolicy})
	}

	if h.CSP != "" {
		name := "Content-Security-Policy"
		if h.CSPReportOnly {
			name += "-Report-Only"
		}

		l = append(l, WebHeader{name, h.CSP})
	}

	return l
}

// ErrHeaderRejected wraps every reason Validate refuses a header.
var ErrHeaderRejected = errors.New("header rejected")

// ErrHeaderConflict is a custom snippet that sets a header which is also
// switched on as a security header.
var ErrHeaderConflict = errors.New("header conflict")

// Validate checks the fixed choices and the own values. Own values are
// written into nginx in quotes by package web, so they may hold none of
// the characters that could end the string or start a variable.
func (h WebHeaders) Validate() error {
	if h.FrameOptions != "" && h.FrameOptions != FrameSameOrigin && h.FrameOptions != FrameDeny {
		return fmt.Errorf("%w: X-Frame-Options %q", ErrHeaderRejected, h.FrameOptions)
	}

	if h.ReferrerPolicy != "" && !slices.Contains(ReferrerPolicies, h.ReferrerPolicy) {
		return fmt.Errorf("%w: Referrer-Policy %q", ErrHeaderRejected, h.ReferrerPolicy)
	}

	if h.PermissionsPolicy != "" {
		if err := checkPermissionsPolicy(h.PermissionsPolicy); err != nil {
			return fmt.Errorf("%w: Permissions-Policy: %w", ErrHeaderRejected, err)
		}
	}

	if h.CSPReportOnly && h.CSP == "" {
		return fmt.Errorf("%w: report only needs a policy", ErrHeaderRejected)
	}

	if h.CSP != "" {
		if err := checkCSP(h.CSP); err != nil {
			return fmt.Errorf("%w: Content-Security-Policy: %w", ErrHeaderRejected, err)
		}
	}

	return nil
}

// plainValue refuses what must never reach nginx: control and non-ASCII
// characters, the backslash, "$" (a variable), braces and the quote that
// package web puts around the value.
func plainValue(v, quote string) error {
	if len(v) > MaxHeaderValueBytes {
		return fmt.Errorf("longer than %d bytes", MaxHeaderValueBytes)
	}

	if strings.TrimSpace(v) != v {
		return errors.New("leading or trailing space")
	}

	for i := range len(v) {
		c := v[i]
		if c < 0x20 || c > 0x7e {
			return fmt.Errorf("character %q at %d is not printable ASCII", c, i)
		}

		if strings.IndexByte(`\${}`+quote, c) >= 0 {
			return fmt.Errorf("character %q at %d is not allowed", c, i)
		}
	}

	return nil
}

// cspDirectives are the directive names of CSP Level 3 and the
// still-common older ones.
var cspDirectives = []string{
	"base-uri", "block-all-mixed-content", "child-src", "connect-src", "default-src", "fenced-frame-src",
	"font-src", "form-action", "frame-ancestors", "frame-src", "img-src", "manifest-src", "media-src",
	"object-src", "report-to", "report-uri", "require-trusted-types-for", "sandbox", "script-src",
	"script-src-attr", "script-src-elem", "style-src", "style-src-attr", "style-src-elem", "trusted-types",
	"upgrade-insecure-requests", "webrtc", "worker-src",
}

// cspTokenRe is one source expression or value: keywords in single
// quotes, schemes, hosts, URLs, hashes and nonces. No comma (it would
// start a second policy) and no double quote.
var cspTokenRe = regexp.MustCompile(`^[A-Za-z0-9'*:/._~%+=?&#@!-]+$`)

// checkCSP accepts "directive value...; directive ..." with known,
// unique directive names. Package web quotes it with double quotes.
func checkCSP(v string) error {
	if err := plainValue(v, `"`); err != nil {
		return err
	}

	seen := map[string]bool{}

	for part := range strings.SplitSeq(v, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}

		name := strings.ToLower(fields[0])
		if !slices.Contains(cspDirectives, name) {
			return fmt.Errorf("unknown directive %q", fields[0])
		}

		if seen[name] {
			return fmt.Errorf("directive %q twice", name)
		}

		seen[name] = true

		for _, t := range fields[1:] {
			if !cspTokenRe.MatchString(t) {
				return fmt.Errorf("invalid value %q in %s", t, name)
			}
		}
	}

	if len(seen) == 0 {
		return errors.New("no directive")
	}

	return nil
}

// permissionsItemRe is one "feature=allowlist" of Permissions-Policy
// (a structured-field dictionary member).
var permissionsItemRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}=(\*|self|\((\s*(self|src|\*|"https?://[A-Za-z0-9.:\[\]*-]+")\s*)*\))$`)

// checkPermissionsPolicy accepts a comma-separated list of features with
// their allowlists. Package web quotes it with single quotes.
func checkPermissionsPolicy(v string) error {
	if err := plainValue(v, `'`); err != nil {
		return err
	}

	seen := map[string]bool{}

	for item := range strings.SplitSeq(v, ",") {
		item = strings.TrimSpace(item)
		if !permissionsItemRe.MatchString(item) {
			return fmt.Errorf("invalid entry %q", item)
		}

		name, _, _ := strings.Cut(item, "=")
		if seen[name] {
			return fmt.Errorf("feature %q twice", name)
		}

		seen[name] = true
	}

	return nil
}

// Conflict refuses custom snippets (ADR-0029) that set, at their top level,
// a header which is switched on here: the browser would get two values. A
// snippet that does not parse is left to the snippet check.
func (h WebHeaders) Conflict(server, root string) error {
	list := h.List()
	if len(list) == 0 {
		return nil
	}

	for _, sn := range []struct{ slot, src string }{{"server", server}, {"location /", root}} {
		if sn.src == "" {
			continue
		}

		ds, err := nginxconf.Parse(sn.src)
		if err != nil {
			continue
		}

		for _, d := range nginxconf.Find(ds, "add_header") {
			if len(d.Args) == 0 {
				continue
			}

			for _, x := range list {
				if strings.EqualFold(d.Args[0], x.Name) {
					return fmt.Errorf("%w: the %s snippet sets %s, which is switched on as a security header", ErrHeaderConflict, sn.slot, x.Name)
				}
			}
		}
	}

	return nil
}
