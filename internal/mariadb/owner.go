// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"regexp"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// ownerUserPfx names the owner accounts of ADR-0030. The "hostashi" prefix
// is reserved for database users (ADR-0024), so no site can create one.
const ownerUserPfx = "hostashi_own_"

var ownerUserRe = regexp.MustCompile(`^hostashi_own_[0-9a-f]{16}$`)

func ownerAccount(user string) string { return "'" + user + "'@'" + userHost + "'" }

// ensureOwner returns the owner account of the database and creates it,
// locked and with all privileges on that database only, on the first load.
// Its password is random and not kept anywhere.
func (m *Manager) ensureOwner(ctx context.Context, database string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.reg.dbs[database]
	if !ok {
		return "", ErrNotFound
	}

	if d.Owner != "" {
		return d.Owner, nil
	}

	grantOn, err := quoteGrantName(database)
	if err != nil {
		return "", err
	}

	user := ownerUserPfx + randomHex(8)
	acct := ownerAccount(user)

	if err := m.exec(ctx, "create owner", "CREATE USER "+acct+" IDENTIFIED BY '"+agentapi.GenerateDBPassword()+"' ACCOUNT LOCK"); err != nil {
		return "", err
	}

	if err := m.exec(ctx, "grant owner", "GRANT ALL PRIVILEGES ON "+grantOn+".* TO "+acct); err != nil {
		m.undo(ctx, "DROP USER IF EXISTS "+acct)

		return "", err
	}

	before := m.reg.snapshot()
	d.Owner = user

	if err := m.reg.commit(before); err != nil {
		m.undo(ctx, "DROP USER IF EXISTS "+acct)

		return "", err
	}

	return user, nil
}

// unlockOwner gives the owner a new random password for one load and
// unlocks it. The returned function locks it again and replaces the
// password, also when ctx has ended.
func (m *Manager) unlockOwner(ctx context.Context, user string) (string, func(), error) {
	pw := agentapi.GenerateDBPassword()
	acct := ownerAccount(user)
	relock := func() {
		m.undo(ctx, "ALTER USER "+acct+" IDENTIFIED BY '"+agentapi.GenerateDBPassword()+"' ACCOUNT LOCK")
	}

	if err := m.exec(ctx, "unlock owner", "ALTER USER "+acct+" IDENTIFIED BY '"+pw+"' ACCOUNT UNLOCK"); err != nil {
		relock()

		return "", nil, err
	}

	return pw, relock, nil
}

// LockOwners locks every owner account again, in case an agent crash left
// one open during a load.
func (m *Manager) LockOwners(ctx context.Context) error {
	m.mu.Lock()
	owners := make([]string, 0, len(m.reg.dbs))

	for _, d := range m.reg.dbs {
		if d.Owner != "" {
			owners = append(owners, d.Owner)
		}
	}
	m.mu.Unlock()

	for _, u := range owners {
		if err := m.exec(ctx, "lock owner", "ALTER USER IF EXISTS "+ownerAccount(u)+" IDENTIFIED BY '"+agentapi.GenerateDBPassword()+"' ACCOUNT LOCK"); err != nil {
			return err
		}
	}

	return nil
}
