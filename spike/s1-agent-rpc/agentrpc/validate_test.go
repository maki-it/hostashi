package agentrpc

import (
	"strings"
	"testing"
)

func TestValidateDomain(t *testing.T) {
	good := []string{"example.com", "a.b.c.example.de", "xn--mnchen-3ya.de", "1example.org", "a-b.io"}
	for _, d := range good {
		if err := ValidateDomain(d); err != nil {
			t.Errorf("%q: unexpected error %v", d, err)
		}
	}
	bad := []string{
		"", "localhost", "Example.com", "example.com.", ".example.com", "ex..com",
		"-a.com", "a-.com", "*.example.com", "example.com:80", "1.2.3.4", "münchen.de",
		"a b.com", "a/b.com", "../etc.com", "a\x00.com", "example.com\n",
		strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 127) + "com",
	}
	for _, d := range bad {
		if err := ValidateDomain(d); err == nil {
			t.Errorf("%q: expected error", d)
		}
	}
}

func TestValidateUser(t *testing.T) {
	for _, u := range []string{"site1", "shop-maki", "a_b", "ab"} {
		if err := ValidateUser(u); err != nil {
			t.Errorf("%q: unexpected error %v", u, err)
		}
	}
	bad := []string{
		"", "a", "1site", "Site", "root", "www-data", "hostashi", "systemd-foo",
		"../root", "a/b", "a.b", "-a", "a b", "a\nb", "site;rm", strings.Repeat("a", 33),
	}
	for _, u := range bad {
		if err := ValidateUser(u); err == nil {
			t.Errorf("%q: expected error", u)
		}
	}
}

func TestSiteCreateValidate(t *testing.T) {
	cases := []struct {
		req SiteCreateReq
		ok  bool
	}{
		{SiteCreateReq{"example.com", "site1", SitePHP, "8.3"}, true},
		{SiteCreateReq{"example.com", "site1", SiteStatic, ""}, true},
		{SiteCreateReq{"example.com", "site1", SitePHP, ""}, false},
		{SiteCreateReq{"example.com", "site1", SitePHP, "8.3; id"}, false},
		{SiteCreateReq{"example.com", "site1", SiteStatic, "8.3"}, false},
		{SiteCreateReq{"example.com", "site1", "shell", ""}, false},
		{SiteCreateReq{"example.com", "root", SiteStatic, ""}, false},
		{SiteCreateReq{"bad_domain", "site1", SiteStatic, ""}, false},
	}
	for _, c := range cases {
		if err := c.req.Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: ok=%v, err=%v", c.req, c.ok, err)
		}
	}
}
