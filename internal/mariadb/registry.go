// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statefile"
)

// StateFile is the registry file name inside the agent's state directory.
const StateFile = "databases.json"

const (
	stateVersion = 1
	maxStateSize = 4 << 20
)

// ErrStateInvalid means the registry file does not parse or breaks a rule.
var ErrStateInvalid = errors.New("mariadb: state file is invalid")

// Database is root-owned state: the agent manages only what is listed here.
type Database struct {
	Name  string   `json:"name"`
	Site  string   `json:"site"`
	Users []string `json:"users"`
}

type state struct {
	Version   int        `json:"version"`
	Databases []Database `json:"databases"`
}

// registry is not safe for concurrent use; Manager serializes access.
type registry struct {
	dir string
	dbs map[string]*Database
	// users maps every registered user to its database.
	users map[string]string
}

func openRegistry(dir string, ownerUID uint32) (*registry, error) {
	reg := &registry{dir: dir, dbs: map[string]*Database{}, users: map[string]string{}}

	raw, err := statefile.Read(dir, StateFile, ownerUID, maxStateSize)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return reg, reg.save()
	case err != nil:
		return nil, err
	}

	var s state

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&s); err != nil || dec.More() {
		return nil, fmt.Errorf("%w: not a single valid document", ErrStateInvalid)
	}

	if s.Version != stateVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrStateInvalid, s.Version)
	}

	for _, d := range s.Databases {
		if err := (agentapi.DBCreateRequest{Site: d.Site, Name: d.Name}).Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStateInvalid, err)
		}

		if _, dup := reg.dbs[d.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate database %q", ErrStateInvalid, d.Name)
		}

		for _, u := range d.Users {
			if err := agentapi.ValidateDBUser(u); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrStateInvalid, err)
			}

			if _, dup := reg.users[u]; dup {
				return nil, fmt.Errorf("%w: duplicate user %q", ErrStateInvalid, u)
			}

			reg.users[u] = d.Name
		}

		reg.dbs[d.Name] = &Database{Name: d.Name, Site: d.Site, Users: append([]string{}, d.Users...)}
	}

	return reg, nil
}

func (r *registry) save() error {
	s := state{Version: stateVersion, Databases: r.list()}

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("mariadb: encode state: %w", err)
	}

	return statefile.Write(r.dir, StateFile, append(raw, '\n'))
}

// list returns copies sorted by name, users sorted too.
func (r *registry) list() []Database {
	out := make([]Database, 0, len(r.dbs))
	for _, d := range r.dbs {
		users := append([]string{}, d.Users...)
		sort.Strings(users)
		out = append(out, Database{Name: d.Name, Site: d.Site, Users: users})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// snapshot and restore let a caller undo an in-memory change when save fails.
func (r *registry) snapshot() []Database { return r.list() }

func (r *registry) restore(s []Database) {
	r.dbs = map[string]*Database{}
	r.users = map[string]string{}

	for _, d := range s {
		r.dbs[d.Name] = &Database{Name: d.Name, Site: d.Site, Users: append([]string{}, d.Users...)}
		for _, u := range d.Users {
			r.users[u] = d.Name
		}
	}
}

// commit saves the registry or, on failure, puts back the state before.
func (r *registry) commit(before []Database) error {
	if err := r.save(); err != nil {
		r.restore(before)

		return err
	}

	return nil
}
