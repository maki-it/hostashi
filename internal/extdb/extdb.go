// SPDX-License-Identifier: AGPL-3.0-or-later

// Package extdb is the list of databases on external servers that the
// panel hands to the backup (ADR-0041). The panel writes it next to its
// database copy whenever a server or an external database changes; the
// backup run, as root, reads it and opens each server's sealed
// administrator password with the server key.
//
// The backup trusts the file only as far as the panel already reaches: the
// panel holds the same credentials. Every field is checked again here, so a
// broken or hostile file cannot put anything but validated names, hosts
// and ports into the dump's option file.
package extdb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/statefile"
)

// FileName is the manifest in the panel's backup directory.
const FileName = "external-databases.json"

// MaxSize bounds the file: 2000 databases and a few servers with CAs.
const MaxSize = 4 << 20

const version = 1

// Server is an external server with its sealed admin password; the purpose
// of the seal is PasswordPurpose(ID).
type Server struct {
	// Engine is dbserver.EnginePostgreSQL for PostgreSQL (ADR-0051);
	// empty for MariaDB and MySQL, as in files written before it.
	Engine         string `json:"engine,omitempty"`
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	AdminUser      string `json:"adminUser"`
	SealedPassword []byte `json:"sealedPassword"`
	TLSMode        string `json:"tlsMode"`
	CAPEM          string `json:"caPem,omitempty"`
}

// PasswordPurpose is the context of a server's sealed password.
func PasswordPurpose(id int64) string { return fmt.Sprintf("db-server:%d", id) }

// Config is the connection with the opened password.
func (s Server) Config(password string) dbserver.Config {
	return dbserver.Config{
		Engine: s.Engine, Host: s.Host, Port: s.Port, AdminUser: s.AdminUser, AdminPassword: password,
		TLS: dbserver.TLSMode(s.TLSMode), CAPEM: s.CAPEM,
	}
}

// Database is a site's database on an external server.
type Database struct {
	Name   string `json:"name"`
	Site   string `json:"site"`
	Server string `json:"server"`
}

// Manifest is the whole file.
type Manifest struct {
	Version   int        `json:"version"`
	Servers   []Server   `json:"servers"`
	Databases []Database `json:"databases"`
}

// Validate checks every field. The password is checked when it is opened.
func (m Manifest) Validate() error {
	if m.Version != version {
		return fmt.Errorf("extdb: unknown version %d", m.Version)
	}

	servers := map[string]bool{}

	for _, s := range m.Servers {
		cfg := s.Config("x")
		if s.ID < 2 || s.Name == "" || len(s.Name) > 32 || servers[s.Name] || len(s.SealedPassword) == 0 ||
			(s.Engine != "" && !dbserver.IsPostgres(s.Engine)) {
			return fmt.Errorf("extdb: bad server %q", s.Name)
		}

		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("extdb: server %q: %w", s.Name, err)
		}

		servers[s.Name] = true
	}

	seen := map[string]bool{}

	for _, d := range m.Databases {
		if err := agentapi.ValidateDBName(d.Name); err != nil || seen[d.Name] {
			return fmt.Errorf("extdb: bad database %q", d.Name)
		}

		if err := agentapi.ValidateSiteUser(d.Site); err != nil {
			return fmt.Errorf("extdb: database %q: %w", d.Name, err)
		}

		if !servers[d.Server] {
			return fmt.Errorf("extdb: database %q is on unknown server %q", d.Name, d.Server)
		}

		seen[d.Name] = true
	}

	return nil
}

// Write replaces the manifest in dir atomically, mode 0600.
func Write(dir string, m Manifest) error {
	m.Version = version

	if m.Servers == nil {
		m.Servers = []Server{}
	}

	if m.Databases == nil {
		m.Databases = []Database{}
	}

	if err := m.Validate(); err != nil {
		return err
	}

	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("extdb: %w", err)
	}

	return statefile.Write(dir, FileName, raw)
}

// Read returns the manifest in dir, which ownerUID must own with mode
// 0700. A missing file is an empty manifest: no external databases.
func Read(dir string, ownerUID uint32) (Manifest, error) {
	raw, err := statefile.Read(dir, FileName, ownerUID, MaxSize)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{Version: version}, nil
	}

	if err != nil {
		return Manifest{}, err
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("extdb: %w", err)
	}

	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}

	return m, nil
}
