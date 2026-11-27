// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"strings"
	"testing"
)

func TestWebHeadersValidateAccepts(t *testing.T) {
	good := []WebHeaders{
		{},
		DefaultWebHeaders,
		{FrameOptions: FrameDeny, ReferrerPolicy: "no-referrer"},
		{PermissionsPolicy: PermissionsPolicyStrict},
		{PermissionsPolicy: `geolocation=(self "https://maps.example.com"), camera=*, fullscreen=self`},
		{CSP: "default-src 'self'; img-src 'self' data: https://cdn.example.com; frame-ancestors 'none'"},
		{CSP: "default-src 'self'; script-src 'self' 'sha256-abc+/=' 'nonce-xyz'; upgrade-insecure-requests;", CSPReportOnly: true},
		{CSP: "Default-Src 'self'"},
	}
	for _, h := range good {
		if err := h.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", h, err)
		}
	}
}

func TestWebHeadersValidateRefuses(t *testing.T) {
	bad := map[string]WebHeaders{
		"frame allow-from":   {FrameOptions: "ALLOW-FROM https://a.example"},
		"frame lower case":   {FrameOptions: "sameorigin"},
		"referrer unknown":   {ReferrerPolicy: "never"},
		"report only empty":  {CSPReportOnly: true},
		"csp double quote":   {CSP: `default-src 'self'" always; return 200 "x`},
		"csp dollar":         {CSP: "default-src $host"},
		"csp backslash":      {CSP: `default-src 'self' \"`},
		"csp newline":        {CSP: "default-src 'self'\nreturn 200;"},
		"csp tab":            {CSP: "default-src\t'self'"},
		"csp brace":          {CSP: "default-src {a}"},
		"csp non-ascii":      {CSP: "default-src 'sélf'"},
		"csp unknown":        {CSP: "default-src 'self'; evil-src *"},
		"csp twice":          {CSP: "default-src 'self'; default-src *"},
		"csp comma":          {CSP: "default-src 'self', script-src *"},
		"csp empty":          {CSP: " ; ;"},
		"csp leading space":  {CSP: " default-src 'self'"},
		"csp too long":       {CSP: "default-src " + strings.Repeat("a", MaxHeaderValueBytes)},
		"pp single quote":    {PermissionsPolicy: "camera=('self')"},
		"pp dollar":          {PermissionsPolicy: "camera=($x)"},
		"pp no allowlist":    {PermissionsPolicy: "camera"},
		"pp bad origin":      {PermissionsPolicy: `camera=("javascript:alert(1)")`},
		"pp twice":           {PermissionsPolicy: "camera=(), camera=*"},
		"pp semicolon":       {PermissionsPolicy: "camera=(); return 200"},
		"pp newline":         {PermissionsPolicy: "camera=()\n"},
		"pp upper":           {PermissionsPolicy: "Camera=()"},
		"pp trailing comma":  {PermissionsPolicy: "camera=(),"},
		"pp double quote in": {PermissionsPolicy: `camera=("https://a.example" always; x")`},
	}
	for name, h := range bad {
		err := h.Validate()
		if err == nil {
			t.Errorf("%s: accepted", name)

			continue
		}

		if !errors.Is(err, ErrHeaderRejected) {
			t.Errorf("%s: %v is not ErrHeaderRejected", name, err)
		}
	}
}

func TestWebHeadersList(t *testing.T) {
	h := WebHeaders{NoSniff: true, FrameOptions: FrameDeny, ReferrerPolicy: "origin", PermissionsPolicy: "usb=()", CSP: "default-src 'self'", CSPReportOnly: true, NoIndex: true}

	var names []string
	for _, x := range h.List() {
		names = append(names, x.Name+": "+x.Value)
	}

	want := "X-Content-Type-Options: nosniff|X-Frame-Options: DENY|Referrer-Policy: origin|Permissions-Policy: usb=()|Content-Security-Policy-Report-Only: default-src 'self'|X-Robots-Tag: noindex, nofollow"
	if got := strings.Join(names, "|"); got != want {
		t.Errorf("got %s", got)
	}

	if (WebHeaders{}).List() != nil || !(WebHeaders{}).IsZero() || h.IsZero() {
		t.Error("zero value")
	}
}

func TestWebApplyRequestChecksHeaders(t *testing.T) {
	r := WebApplyRequest{Sites: []WebSite{{User: "blog", Domains: []string{"blog.example.com"}, Headers: &WebHeaders{FrameOptions: "ALLOWALL"}}}}
	if err := r.Validate(); !errors.Is(err, ErrHeaderRejected) {
		t.Errorf("got %v", err)
	}

	r.Sites[0].Headers = &DefaultWebHeaders
	if err := r.Validate(); err != nil {
		t.Error(err)
	}
}

func TestWebHeadersConflict(t *testing.T) {
	h := DefaultWebHeaders

	for name, sn := range map[string][2]string{
		"server":        {"add_header x-frame-options DENY;\n", ""},
		"root quoted":   {"", `add_header "X-Content-Type-Options" nosniff always;`},
		"after comment": {"# own header\nexpires 1h;\nadd_header Referrer-Policy origin;", ""},
	} {
		if err := h.Conflict(sn[0], sn[1]); !errors.Is(err, ErrHeaderConflict) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	for name, sn := range map[string][2]string{
		"other header":    {"add_header X-Custom yes;", "add_header Permissions-Policy 'usb=()';"},
		"nested location": {"location /api/ { add_header X-Frame-Options DENY; }", ""},
		"empty":           {"", ""},
		"unparsable":      {"add_header X-Frame-Options {", ""},
	} {
		if err := h.Conflict(sn[0], sn[1]); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	if err := (WebHeaders{}).Conflict("add_header X-Frame-Options DENY;", ""); err != nil {
		t.Errorf("no headers on: %v", err)
	}
}
