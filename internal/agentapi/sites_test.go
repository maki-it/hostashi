// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestValidateSiteUser(t *testing.T) {
	t.Parallel()

	good := []string{"ab", "example", "my-site", "my_site", "a1", "site-0123456789-0123456789-abcd"}
	for _, u := range good {
		if err := ValidateSiteUser(u); err != nil {
			t.Errorf("%q: unexpected error %v", u, err)
		}
	}

	bad := []string{
		"", "a", "A", "Root", "1site", "-site", "_site", "my.site", "my site", "my/site", "../x", "..",
		"site\n", "site\x00", "site;id", "$(id)", "über", "root", "www-data", "hostashi", "systemd-resolve",
		strings.Repeat("a", 33),
	}
	for _, u := range bad {
		if err := ValidateSiteUser(u); err == nil {
			t.Errorf("%q: want error", u)
		}
	}
}

func TestSiteRequestsValidate(t *testing.T) {
	t.Parallel()

	if err := (SiteCreateRequest{User: "blog"}).Validate(); err != nil {
		t.Fatal(err)
	}

	if err := (SiteCreateRequest{User: "blog", Pool: "fast-ssd"}).Validate(); err != nil {
		t.Fatal(err)
	}

	for _, r := range []SiteCreateRequest{{User: "../x"}, {User: "blog", Pool: "../etc"}, {User: "blog", Pool: "A"}, {}} {
		if r.Validate() == nil {
			t.Errorf("%+v: want error", r)
		}
	}

	if (SiteDeleteRequest{User: "blog"}).Validate() != nil || (SiteDeleteRequest{User: "x/y"}).Validate() == nil {
		t.Error("delete validation wrong")
	}
}
