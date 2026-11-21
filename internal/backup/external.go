// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"fmt"
	"os/user"
	"strconv"

	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/extdb"
)

// Where the panel leaves its list of external databases (extdb), next to
// its database copy, as the panel's system user. PanelKeyFile opens
// their passwords.
const (
	PanelBackupDir = "/var/lib/hostashi/backup"
	PanelUser      = "hostashi"
)

// Opener opens a sealed secret; secrets.Box implements it.
type Opener interface {
	Open(data []byte, context string) ([]byte, error)
}

// AddExternal adds the external databases of the manifest to their sites.
// A database whose password cannot be opened becomes an item that fails
// with the reason, so the run shows it. A database of a site the agent
// does not know is returned as an error: the run still saves the rest.
func AddExternal(sites []Site, m extdb.Manifest, box Opener) ([]Site, error) {
	servers := map[string]*dbserver.Config{}
	failed := map[string]error{}

	for _, s := range m.Servers {
		pw, err := box.Open(s.SealedPassword, extdb.PasswordPurpose(s.ID))
		if err != nil {
			failed[s.Name] = fmt.Errorf("open the admin password of database server %s: %w", s.Name, err)

			continue
		}

		if dbserver.IsPostgres(s.Engine) {
			// pg_dump comes with internal/pgctl (ADR-0051); until then
			// the run shows these databases as failed, not as saved.
			failed[s.Name] = fmt.Errorf("database server %s: backups of PostgreSQL databases are not available yet", s.Name)

			continue
		}

		cfg := s.Config(string(pw))
		servers[s.Name] = &cfg
	}

	idx := map[string]int{}
	for i, s := range sites {
		idx[s.Name] = i
	}

	out := append([]Site{}, sites...)

	var unknown []string

	for _, d := range m.Databases {
		i, ok := idx[d.Site]
		if !ok {
			unknown = append(unknown, d.Name)

			continue
		}

		out[i].External = append(out[i].External, ExternalDB{Name: d.Name, Server: servers[d.Server], Err: failed[d.Server]})
	}

	if len(unknown) > 0 {
		return out, fmt.Errorf("external databases of unknown sites not saved: %v", unknown)
	}

	return out, nil
}

// panelUID is the uid of the panel's system user, which owns the manifest.
func panelUID(name string) (uint32, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, fmt.Errorf("panel user: %w", err)
	}

	id, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("panel user: %w", err)
	}

	return uint32(id), nil
}
