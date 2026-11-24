// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func testHash(t *testing.T) string {
	t.Helper()

	h, err := bcrypt.GenerateFromPassword([]byte("correct horse battery"), AccessBcryptCost)
	if err != nil {
		t.Fatal(err)
	}

	return string(h)
}

func TestWebAccessValidateAccepts(t *testing.T) {
	h := testHash(t)
	good := []WebAccess{
		{},
		{IPMode: AccessIPAllow, IPs: []string{"203.0.113.7", "2001:db8::/48"}},
		{IPMode: AccessIPDeny, IPs: []string{"198.51.100.0/24"}},
		{Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: h}}}},
		{Auth: &WebAuth{Paths: []string{"/wp-admin", "/wp-login.php", "/a/b_c~d/"}, Bypass: []string{"203.0.113.0/24"}, Users: []WebAuthUser{{Name: "a.b-c_d@e", Hash: h}, {Name: "two", Hash: h}}}},
		{IPMode: AccessIPAllow, IPs: []string{"203.0.113.7"}, Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: h}}}},
	}

	for i, a := range good {
		if err := a.Validate(); err != nil {
			t.Errorf("%d: %v", i, err)
		}
	}
}

func TestWebAccessValidateRefuses(t *testing.T) {
	h := testHash(t)
	user := []WebAuthUser{{Name: "kim", Hash: h}}
	many := func(n int, f func(int) string) []string {
		var out []string
		for i := range n {
			out = append(out, f(i))
		}

		return out
	}
	bad := map[string]WebAccess{
		"unknown mode":     {IPMode: "maybe", IPs: []string{"203.0.113.7"}},
		"mode without ips": {IPMode: AccessIPAllow},
		"ips without mode": {IPs: []string{"203.0.113.7"}},
		"bad ip":           {IPMode: AccessIPDeny, IPs: []string{"203.0.113.7; allow all"}},
		"zone":             {IPMode: AccessIPDeny, IPs: []string{"fe80::1%eth0"}},
		"duplicate ip":     {IPMode: AccessIPDeny, IPs: []string{"203.0.113.7", "203.0.113.7/32"}},
		"too many ips":     {IPMode: AccessIPDeny, IPs: many(MaxAccessIPs+1, func(i int) string { return netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}).String() })},
		"no users":         {Auth: &WebAuth{}},
		"too many users": {Auth: &WebAuth{Users: func() []WebAuthUser {
			var u []WebAuthUser
			for i := range MaxAccessUsers + 1 {
				u = append(u, WebAuthUser{Name: "u" + string(rune('a'+i%26)) + strings.Repeat("x", i), Hash: h})
			}
			return u
		}()}},
		"duplicate user":    {Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: h}, {Name: "kim", Hash: h}}}},
		"colon in name":     {Auth: &WebAuth{Users: []WebAuthUser{{Name: "a:b", Hash: h}}}},
		"newline in name":   {Auth: &WebAuth{Users: []WebAuthUser{{Name: "a\nb", Hash: h}}}},
		"empty name":        {Auth: &WebAuth{Users: []WebAuthUser{{Name: "", Hash: h}}}},
		"long name":         {Auth: &WebAuth{Users: []WebAuthUser{{Name: strings.Repeat("a", 65), Hash: h}}}},
		"apr1 hash":         {Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: "$apr1$q.r1yttn$MODtEu7WGYd3yY6eEjKNd/"}}}},
		"newline in hash":   {Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: h[:59] + "\n"}}}},
		"colon in hash":     {Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: h[:59] + ":"}}}},
		"cost too high":     {Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: h[:4] + "13" + h[6:]}}}},
		"cost not a number": {Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: h[:4] + "x5" + h[6:]}}}},
		"relative path":     {Auth: &WebAuth{Paths: []string{"wp-admin"}, Users: user}},
		"root path":         {Auth: &WebAuth{Paths: []string{"/"}, Users: user}},
		"dotdot path":       {Auth: &WebAuth{Paths: []string{"/a/../b"}, Users: user}},
		"dot path":          {Auth: &WebAuth{Paths: []string{"/a/./b"}, Users: user}},
		"double slash":      {Auth: &WebAuth{Paths: []string{"/a//b"}, Users: user}},
		"percent path":      {Auth: &WebAuth{Paths: []string{"/a%2fb"}, Users: user}},
		"regex path":        {Auth: &WebAuth{Paths: []string{"/a(.*)"}, Users: user}},
		"quote path":        {Auth: &WebAuth{Paths: []string{`/a"b`}, Users: user}},
		"semicolon path":    {Auth: &WebAuth{Paths: []string{"/a;b"}, Users: user}},
		"space path":        {Auth: &WebAuth{Paths: []string{"/a b"}, Users: user}},
		"long path":         {Auth: &WebAuth{Paths: []string{"/" + strings.Repeat("a", MaxAccessPathBytes)}, Users: user}},
		"duplicate path":    {Auth: &WebAuth{Paths: []string{"/wp-admin", "/WP-Admin/"}, Users: user}},
		"too many paths":    {Auth: &WebAuth{Paths: many(MaxAccessPaths+1, func(i int) string { return "/p" + strings.Repeat("x", i) }), Users: user}},
		"loopback bypass":   {Auth: &WebAuth{Bypass: []string{"127.0.0.1"}, Users: user}},
		"loopback6 bypass":  {Auth: &WebAuth{Bypass: []string{"::1"}, Users: user}},
		"everything bypass": {Auth: &WebAuth{Bypass: []string{"0.0.0.0/0"}, Users: user}},
		"bad bypass":        {Auth: &WebAuth{Bypass: []string{"nope"}, Users: user}},
	}

	for name, a := range bad {
		if err := a.Validate(); !errors.Is(err, ErrAccessRejected) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestValidateAccessPassword(t *testing.T) {
	for _, pw := range []string{"correct horse", "äöüäöüäöüäöü", strings.Repeat("x", 72)} {
		if err := ValidateAccessPassword(pw); err != nil {
			t.Errorf("%q: %v", pw, err)
		}
	}

	for _, pw := range []string{"short", strings.Repeat("x", 73), "correct horse\n", "correct\x00horse", "correct horse\x7f"} {
		if err := ValidateAccessPassword(pw); !errors.Is(err, ErrAccessRejected) {
			t.Errorf("%q: got %v", pw, err)
		}
	}
}

func TestWebAccessCheckOwn(t *testing.T) {
	h := testHash(t)
	a := WebAccess{Auth: &WebAuth{Bypass: []string{"203.0.113.0/24"}, Users: []WebAuthUser{{Name: "kim", Hash: h}}}}
	own := []netip.Addr{netip.MustParseAddr("::ffff:203.0.113.9")}

	if err := a.CheckOwn(own); !errors.Is(err, ErrAccessRejected) {
		t.Fatalf("own address in the bypass list: %v", err)
	}

	if err := a.CheckOwn([]netip.Addr{netip.MustParseAddr("198.51.100.1")}); err != nil {
		t.Fatal(err)
	}

	if err := (WebAccess{IPMode: AccessIPAllow, IPs: []string{"203.0.113.9"}}).CheckOwn(own); err != nil {
		t.Fatalf("the IP rule may name the server: %v", err)
	}
}

func TestWebAccessConflict(t *testing.T) {
	h := testHash(t)
	ip := WebAccess{IPMode: AccessIPDeny, IPs: []string{"203.0.113.7"}}
	auth := WebAccess{Auth: &WebAuth{Users: []WebAuthUser{{Name: "kim", Hash: h}}}}

	cases := []struct {
		a            WebAccess
		server, root string
		conflict     bool
	}{
		{ip, "deny 198.51.100.1;", "", true},
		{ip, "", "allow all;", true},
		{ip, "location /x {\n    allow all;\n}", "", true},
		{ip, "satisfy any;", "", true},
		{ip, "auth_basic \"x\";", "", false},
		{auth, "location /x {\n    auth_basic off;\n}", "", true},
		{auth, "", "auth_basic_user_file /home/blog/.htpasswd;", true},
		{auth, "deny 198.51.100.1;", "", false},
		{WebAccess{}, "auth_basic \"x\";\ndeny all;", "", false},
		{ip, "this is { not nginx", "", false},
	}

	for i, c := range cases {
		err := c.a.Conflict(c.server, c.root)
		if c.conflict != errors.Is(err, ErrAccessConflict) {
			t.Errorf("%d: got %v", i, err)
		}
	}
}

func TestWebApplyRequestChecksAccess(t *testing.T) {
	r := WebApplyRequest{Sites: []WebSite{{User: "blog", Domains: []string{"blog.example.com"}, Access: &WebAccess{IPMode: AccessIPAllow}}}}
	if err := r.Validate(); !errors.Is(err, ErrAccessRejected) {
		t.Fatalf("allow rule without addresses: %v", err)
	}

	r.Sites[0].Access = &WebAccess{IPMode: AccessIPAllow, IPs: []string{"203.0.113.7"}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}
