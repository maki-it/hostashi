// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestValidateHostname(t *testing.T) {
	good := []string{"web2", "web2.maki-it.de", "a", "x1-y2", strings.Repeat("a", 63), strings.Repeat("a.", 31) + "ab"}
	for _, n := range good {
		if err := ValidateHostname(n); err != nil {
			t.Errorf("ValidateHostname(%q) = %v", n, err)
		}
	}

	bad := []string{
		"", "Web2", "web2.", ".web2", "web..2", "-web", "web-", "web_2", "web 2", "web2;reboot",
		"web2\n", strings.Repeat("a", 64), strings.Repeat("a.", 32) + "a", "10.0.0.1", "1234",
		"wéb", "web/2", "$(id)",
	}
	for _, n := range bad {
		if err := ValidateHostname(n); err == nil {
			t.Errorf("ValidateHostname(%q) = nil", n)
		}
	}
}

func TestShortHostname(t *testing.T) {
	if got := ShortHostname("web2.maki-it.de"); got != "web2" {
		t.Fatalf("got %q", got)
	}

	if got := ShortHostname("web2"); got != "web2" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateTimezone(t *testing.T) {
	good := []string{"UTC", "Europe/Berlin", "America/Argentina/Buenos_Aires", "Etc/GMT+1", "America/Port-au-Prince"}
	for _, z := range good {
		if err := ValidateTimezone(z); err != nil {
			t.Errorf("ValidateTimezone(%q) = %v", z, err)
		}
	}

	bad := []string{
		"", "../etc/passwd", "Europe/../../etc", "/Europe/Berlin", "Europe/Berlin/", "Europe//Berlin",
		"Europe/.Berlin", "A/B/C/D", "Europe/Ber lin", "Europe/Berlin\n", strings.Repeat("A", 65), "1Europe", "Local", "europe/berlin",
	}
	for _, z := range bad {
		if err := ValidateTimezone(z); err == nil {
			t.Errorf("ValidateTimezone(%q) = nil", z)
		}
	}
}
