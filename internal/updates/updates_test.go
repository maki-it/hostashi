// SPDX-License-Identifier: AGPL-3.0-or-later

package updates

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const status = `Package: nginx
Status: install ok installed
Version: 1.26.3-3

Package: hostashi
Status: install ok installed
Priority: optional
Architecture: amd64
Version: 0.1.0
Description: Hostashi
 a hosting panel
`

const sources = `# Hostashi (ADR-0050). Signed by X.
Types: deb
URIs: https://get.hostashi.com/apt
Suites: stable
Components: main
Signed-By: /usr/share/keyrings/hostashi-archive-keyring.asc
`

const packages = `Package: hostashi
Version: 0.1.0
Architecture: amd64

Package: hostashi
Version: 0.10.0
Architecture: amd64

Package: hostashi
Version: 0.9.1
Architecture: amd64
`

func write(t *testing.T, path, s string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) Paths {
	t.Helper()

	d := t.TempDir()
	p := Paths{
		DpkgStatus: filepath.Join(d, "status"),
		Sources:    filepath.Join(d, "hostashi.sources"),
		Lists:      filepath.Join(d, "lists"),
		Unattended: filepath.Join(d, "52hostashi-unattended-upgrades"),
	}
	write(t, p.DpkgStatus, status)
	write(t, p.Sources, sources)
	write(t, filepath.Join(p.Lists, "get.hostashi.com_apt_dists_stable_main_binary-amd64_Packages"), packages)
	write(t, p.Unattended, "Unattended-Upgrade::Origins-Pattern {\n\t\"origin=Debian,codename=${distro_codename}-security,label=Debian-Security\";\n};\n")

	return p
}

func TestRead(t *testing.T) {
	p := setup(t)

	when := time.Date(2026, 11, 22, 6, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(p.Lists, "get.hostashi.com_apt_dists_stable_main_binary-amd64_Packages"), when, when); err != nil {
		t.Fatal(err)
	}

	st, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}

	want := Status{Installed: "0.1.0", Available: "0.10.0", Update: true, Repository: "https://get.hostashi.com/apt", ListedAt: when}
	if st != want {
		t.Fatalf("Read = %+v, want %+v", st, want)
	}

	write(t, p.Unattended, "\t\"origin=Hostashi,codename=stable\";\n")
	write(t, p.DpkgStatus, status[:len(status)-len("Version: 0.1.0\nDescription: Hostashi\n a hosting panel\n")]+"Version: 0.10.0\n")

	st, err = Read(p)
	if err != nil {
		t.Fatal(err)
	}

	if st.Update || !st.Auto || st.Installed != "0.10.0" {
		t.Fatalf("newest installed: %+v", st)
	}
}

func TestReadWithoutPackageOrList(t *testing.T) {
	p := setup(t)

	if err := os.RemoveAll(p.Lists); err != nil {
		t.Fatal(err)
	}

	st, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}

	if st.Installed != "0.1.0" || st.Available != "" || st.Update || !st.ListedAt.IsZero() {
		t.Fatalf("without a list: %+v", st)
	}

	write(t, p.DpkgStatus, "Package: hostashi\nStatus: deinstall ok config-files\nVersion: 0.1.0\n")

	st, err = Read(p)
	if err != nil {
		t.Fatal(err)
	}

	if st != (Status{}) {
		t.Fatalf("removed package: %+v", st)
	}
}

func TestListName(t *testing.T) {
	for _, c := range []struct{ uri, want string }{
		{"https://get.hostashi.com/apt", "get.hostashi.com_apt_dists_stable_main_binary-arm64_Packages"},
		{"http://127.0.0.1:8080/a_b~c", "127.0.0.1:8080_a%5fb%7ec_dists_stable_main_binary-arm64_Packages"},
		{"https://user:pw@mirror.example/apt", "mirror.example_apt_dists_stable_main_binary-arm64_Packages"},
	} {
		if got := listName(c.uri, "stable", "main", "arm64"); got != c.want {
			t.Errorf("listName(%q) = %q, want %q", c.uri, got, c.want)
		}
	}
}
