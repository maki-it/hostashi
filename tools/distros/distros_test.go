// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSupportedFromTheInstaller(t *testing.T) {
	b, err := os.ReadFile("../../" + installerFile)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Supported(b)
	if err != nil {
		t.Fatal(err)
	}

	if len(got["debian"]) == 0 || len(got["ubuntu"]) == 0 {
		t.Fatalf("got %v", got)
	}

	pkgs, err := NginxPackages(b)
	if err != nil || !slices.Contains(pkgs, "nginx") {
		t.Fatalf("nginx packages %v, %v", pkgs, err)
	}

	for _, bad := range []string{"", "debian-13) ;;\n", "  debian-13) ;; # ADR-0020\n  ubuntu-26.04) ;; # ADR-0020\n"} {
		if _, err := Supported([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSupported(t *testing.T) {
	got, err := Supported([]byte("  case x in\n    debian-13 | debian-14 | ubuntu-26.04) ;;   # ADR-0020\n"))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(got["debian"], []string{"13", "14"}) || !slices.Equal(got["ubuntu"], []string{"26.04"}) {
		t.Fatalf("got %v", got)
	}
}

func TestParseDebianStable(t *testing.T) {
	r, err := ParseDebianStable([]byte("Origin: Debian\nSuite: stable\nVersion: 14.1\nCodename: forky\nMD5Sum:\n 3e49 1961125 contrib/Contents-all\n"))
	if err != nil {
		t.Fatal(err)
	}

	if r != (Release{ID: "debian", Version: "14", Codename: "forky", Name: "Debian 14"}) {
		t.Fatalf("got %+v", r)
	}

	for _, bad := range []string{"", "Suite: testing\nCodename: forky\n", "Suite: stable\nVersion: x\nCodename: forky\n", "Suite: stable\nVersion: 14\n"} {
		if _, err := ParseDebianStable([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

const metaRelease = `Dist: noble
Name: Noble Numbat
Version: 24.04.5 LTS
Date: Thu, 25 April 2024 00:24:04 UTC
Supported: 1

Dist: resolute
Name: Resolute Raccoon
Version: 26.04.1 LTS
Date: Thu, 23 April 2026 00:26:04 UTC
Supported: 1

Dist: future
Name: Future Fox
Version: 28.04 LTS
Date: Thu, 20 April 2028 00:00:00 UTC
Supported: 1

Dist: gone
Name: Gone Gnu
Version: 30.04 LTS
Date: Thu, 20 April 2024 00:00:00 UTC
Supported: 0
`

func TestParseUbuntuLTS(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	r, err := ParseUbuntuLTS([]byte(metaRelease), now)
	if err != nil {
		t.Fatal(err)
	}

	// Unsupported and future entries do not count.
	if r != (Release{ID: "ubuntu", Version: "26.04", Codename: "resolute", Name: "Ubuntu 26.04 LTS"}) {
		t.Fatalf("got %+v", r)
	}

	if r, _ = ParseUbuntuLTS([]byte(metaRelease), now.AddDate(2, 0, 0)); r.Codename != "future" {
		t.Fatalf("after its date: %+v", r)
	}

	if _, err := ParseUbuntuLTS([]byte("Dist: x\nVersion: 26.10\nSupported: 1\n"), now); err == nil {
		t.Fatal("no LTS accepted")
	}
}

func TestIsNew(t *testing.T) {
	sup := map[string][]string{"debian": {"13"}, "ubuntu": {"24.04", "26.04"}}

	for _, tc := range []struct {
		r    Release
		want bool
	}{
		{Release{ID: "debian", Version: "13"}, false},
		{Release{ID: "debian", Version: "14"}, true},
		{Release{ID: "debian", Version: "9"}, false},
		{Release{ID: "ubuntu", Version: "26.04"}, false},
		{Release{ID: "ubuntu", Version: "28.04"}, true},
		{Release{ID: "fedora", Version: "99"}, false},
	} {
		if got := IsNew(tc.r, sup); got != tc.want {
			t.Errorf("%+v: %v", tc.r, got)
		}
	}
}

func TestIssue(t *testing.T) {
	r := Release{ID: "debian", Version: "14", Codename: "forky", Name: "Debian 14"}
	rd := Readiness{Sury: false, Packages: map[string]bool{"nginx": true, "libnginx-mod-http-brotli-filter": false}}
	body := IssueBody(r, rd, map[string][]string{"debian": {"13"}})

	if Title(r) != "Support Debian 14 (forky)" {
		t.Fatalf("title %q", Title(r))
	}

	for _, want := range []string{
		"Debian 14 (forky) is released. Hostashi supports Debian 13 today",
		"sury.org PHP repository for `forky`: not yet",
		"`nginx` in the distribution (amd64): yes",
		"`libnginx-mod-http-brotli-filter` in the distribution (amd64): not yet",
		"- [ ] ADR-0020 updated",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
}
