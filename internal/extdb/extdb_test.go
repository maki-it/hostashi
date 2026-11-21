// SPDX-License-Identifier: AGPL-3.0-or-later

package extdb

import (
	"os"
	"path/filepath"
	"testing"
)

func sample() Manifest {
	return Manifest{
		Servers: []Server{{
			ID: 2, Name: "db1", Host: "db.example.net", Port: 3306, AdminUser: "hostashi_admin",
			SealedPassword: []byte{1, 2, 3}, TLSMode: "verify",
		}},
		Databases: []Database{{Name: "shop_wp", Site: "shop", Server: "db1"}},
	}
}

func uid() uint32 { return uint32(os.Getuid()) }

func TestRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backup")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	m, err := Read(dir, uid())
	if err != nil || len(m.Databases) != 0 {
		t.Fatalf("missing file: %+v %v", m, err)
	}

	if err := Write(dir, sample()); err != nil {
		t.Fatal(err)
	}

	m, err = Read(dir, uid())
	if err != nil || len(m.Servers) != 1 || m.Servers[0].Host != "db.example.net" || string(m.Servers[0].SealedPassword) != "\x01\x02\x03" ||
		len(m.Databases) != 1 || m.Databases[0].Server != "db1" {
		t.Fatalf("%+v %v", m, err)
	}

	fi, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}

	// Another owner is refused.
	if _, err := Read(dir, uid()+1); err == nil {
		t.Fatal("foreign owner accepted")
	}
}

func TestValidate(t *testing.T) {
	bad := map[string]func(*Manifest){
		"version":        func(m *Manifest) { m.Version = 2 },
		"local id":       func(m *Manifest) { m.Servers[0].ID = 1 },
		"no seal":        func(m *Manifest) { m.Servers[0].SealedPassword = nil },
		"host":           func(m *Manifest) { m.Servers[0].Host = "db.example.net\nssl=0" },
		"user newline":   func(m *Manifest) { m.Servers[0].AdminUser = "a\nb" },
		"tls":            func(m *Manifest) { m.Servers[0].TLSMode = "socket" },
		"port":           func(m *Manifest) { m.Servers[0].Port = 0 },
		"duplicate srv":  func(m *Manifest) { m.Servers = append(m.Servers, m.Servers[0]) },
		"db name":        func(m *Manifest) { m.Databases[0].Name = "a`b" },
		"db reserved":    func(m *Manifest) { m.Databases[0].Name = "mysql" },
		"duplicate db":   func(m *Manifest) { m.Databases = append(m.Databases, m.Databases[0]) },
		"site":           func(m *Manifest) { m.Databases[0].Site = "../x" },
		"unknown server": func(m *Manifest) { m.Databases[0].Server = "db9" },
		"engine":         func(m *Manifest) { m.Servers[0].Engine = "MariaDB" },
	}

	for name, f := range bad {
		m := sample()
		m.Version = version
		f(&m)

		if err := m.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	m := sample()
	m.Version = version

	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}

	m.Servers[0].Engine, m.Servers[0].Port = "PostgreSQL", 5432
	if err := m.Validate(); err != nil || m.Servers[0].Config("pw").Engine != "PostgreSQL" {
		t.Fatalf("PostgreSQL server: %v", err)
	}
}
