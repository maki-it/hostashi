// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Temporary users of the web database tool (ADR-0038). They are not in the
// registry: they live only as long as the agent, which drops every leftover
// one at start (RemoveToolUsers).

// ToolLogin is the login of one tool session.
type ToolLogin struct {
	Database string
	User     string
	Password string
	Expires  time.Time
}

type toolSession struct {
	database string
	expires  time.Time
	stop     func() bool
}

// toolCloseTimeout bounds dropping a user when its session ends.
const toolCloseTimeout = 30 * time.Second

// OpenTool creates a temporary user with all privileges on database, at
// most agentapi.DBToolMaxConnections connections, and no GRANT OPTION. The
// user is dropped and its connections killed after ttl.
func (m *Manager) OpenTool(ctx context.Context, database string, ttl time.Duration) (ToolLogin, error) {
	if err := agentapi.ValidateDBName(database); err != nil {
		return ToolLogin{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if ttl <= 0 {
		return ToolLogin{}, fmt.Errorf("%w: session length %s", ErrInvalid, ttl)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.reg.dbs[database]; !ok {
		return ToolLogin{}, ErrNotFound
	}

	user := agentapi.DBToolUserPrefix + randomHex(8)
	pw := agentapi.GenerateDBPassword()
	acct := toolAccount(user)

	grantOn, err := quoteGrantName(database)
	if err != nil {
		return ToolLogin{}, err
	}

	create := fmt.Sprintf("CREATE USER %s IDENTIFIED BY '%s' WITH MAX_USER_CONNECTIONS %d", acct, pw, agentapi.DBToolMaxConnections)
	if err := m.exec(ctx, "create tool user", create); err != nil {
		return ToolLogin{}, err
	}

	if err := m.exec(ctx, "grant tool user", "GRANT ALL PRIVILEGES ON "+grantOn+".* TO "+acct); err != nil {
		m.undo(ctx, "DROP USER IF EXISTS "+acct)

		return ToolLogin{}, err
	}

	now := m.now()
	bg := context.WithoutCancel(ctx)

	if m.tools == nil {
		m.tools = map[string]*toolSession{}
	}

	m.tools[user] = &toolSession{database: database, expires: now.Add(ttl), stop: m.toolTimer(bg, user, ttl)}

	return ToolLogin{Database: database, User: user, Password: pw, Expires: now.Add(ttl)}, nil
}

// toolTimer drops user after d. The caller holds m.mu.
func (m *Manager) toolTimer(bg context.Context, user string, d time.Duration) func() bool {
	return m.after(d, func() {
		cctx, cancel := context.WithTimeout(bg, toolCloseTimeout)
		defer cancel()

		if err := m.CloseTool(cctx, user); err != nil {
			m.log.ErrorContext(cctx, "mariadb: drop expired tool user", "user", user, "err", err)
		}
	})
}

// ExtendTool moves the end of user's session to expires, which must lie
// ahead. An unknown user is ErrNotFound: the session has ended.
func (m *Manager) ExtendTool(ctx context.Context, user string, expires time.Time) (ToolSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.tools[user]
	if !ok {
		return ToolSession{}, ErrNotFound
	}

	d := expires.Sub(m.now())
	if d <= 0 {
		return ToolSession{}, fmt.Errorf("%w: the new end has passed", ErrInvalid)
	}

	// A timer that already fired is about to drop the user; the session
	// ends anyway.
	if !s.stop() {
		return ToolSession{}, ErrNotFound
	}

	s.expires = expires
	s.stop = m.toolTimer(context.WithoutCancel(ctx), user, d)

	return ToolSession{Database: s.database, User: user, Expires: expires}, nil
}

// CloseTool ends a session: it drops the user, then kills its connections,
// so a connection cannot log in again in between. An unknown user is not
// an error, the session may have ended already.
func (m *Manager) CloseTool(ctx context.Context, user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.closeTool(ctx, user)
}

func (m *Manager) closeTool(ctx context.Context, user string) error {
	s, ok := m.tools[user]
	if !ok {
		return nil
	}

	if err := m.dropToolUser(ctx, user); err != nil {
		return err
	}

	s.stop()
	delete(m.tools, user)

	return nil
}

func (m *Manager) dropToolUser(ctx context.Context, user string) error {
	if !agentapi.ValidDBToolUser(user) {
		return fmt.Errorf("%w: tool user %q", ErrInvalid, user)
	}

	acct := toolAccount(user)
	if err := m.exec(ctx, "drop tool user", "DROP USER IF EXISTS "+acct); err != nil {
		return err
	}

	// KILL USER of an account without connections succeeds.
	return m.exec(ctx, "kill tool connections", "KILL HARD CONNECTION USER "+acct)
}

// closeToolsOf ends every session on database, before it is dropped. The
// caller holds m.mu.
func (m *Manager) closeToolsOf(ctx context.Context, database string) error {
	for _, user := range m.toolUsers() {
		if m.tools[user].database == database {
			if err := m.closeTool(ctx, user); err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Manager) toolUsers() []string {
	out := make([]string, 0, len(m.tools))
	for u := range m.tools {
		out = append(out, u)
	}

	slices.Sort(out)

	return out
}

// ToolSession describes an open session, for hostashi doctor.
type ToolSession struct {
	Database string
	User     string
	Expires  time.Time
}

// ToolSessions lists the open sessions sorted by user.
func (m *Manager) ToolSessions() []ToolSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]ToolSession, 0, len(m.tools))
	for _, u := range m.toolUsers() {
		s := m.tools[u]
		out = append(out, ToolSession{Database: s.database, User: u, Expires: s.expires})
	}

	return out
}

// RemoveToolUsers drops the temporary users of an earlier agent run, with
// their connections. Sessions of this run are kept.
func (m *Manager) RemoveToolUsers(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
},
) error {
	rows, err := q.QueryContext(ctx, `SELECT User FROM mysql.user WHERE Host = 'localhost' AND User LIKE 'hostashi\_tool\_%'`)
	if err != nil {
		return classify("list tool users", err)
	}

	var users []string

	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			_ = rows.Close()

			return classify("list tool users", err)
		}

		if agentapi.ValidDBToolUser(u) {
			users = append(users, u)
		}
	}

	_ = rows.Close()

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, u := range users {
		if _, ok := m.tools[u]; ok {
			continue
		}

		if err := m.dropToolUser(ctx, u); err != nil {
			return err
		}
	}

	return nil
}

func toolAccount(user string) string { return "'" + user + "'@'" + userHost + "'" }

func (m *Manager) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}

	return time.Now()
}

// after runs f once d has passed; the result stops it.
func (m *Manager) after(d time.Duration, f func()) func() bool {
	if m.afterFunc != nil {
		return m.afterFunc(d, f)
	}

	return time.AfterFunc(d, f).Stop
}
