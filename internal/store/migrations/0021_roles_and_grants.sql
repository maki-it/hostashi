-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Roles and site grants (ADR-0035), and the actor's name in the audit log.

-- A changed CHECK needs a new column: SQLite cannot alter a constraint, and
-- rebuilding users would cascade-delete sessions, tokens and passkeys. A new
-- account gets the least role unless the code names another.
ALTER TABLE users ADD COLUMN role_new TEXT NOT NULL DEFAULT 'user'
    CHECK (role_new IN ('admin', 'manager', 'user'));
UPDATE users SET role_new = role;
ALTER TABLE users DROP COLUMN role;
ALTER TABLE users RENAME COLUMN role_new TO role;

-- A grant gives a user with the role 'user' one whole site. Admins and
-- managers reach every site and hold no grants.
CREATE TABLE site_grants (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    site_id    INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, site_id)
) STRICT;

CREATE INDEX site_grants_site ON site_grants (site_id);

CREATE TRIGGER site_grants_only_users
BEFORE INSERT ON site_grants
WHEN (SELECT role FROM users WHERE id = NEW.user_id) IS NOT 'user'
BEGIN
    SELECT RAISE(ABORT, 'site grants are only for the role user');
END;

CREATE TRIGGER site_grants_no_update
BEFORE UPDATE ON site_grants
BEGIN
    SELECT RAISE(ABORT, 'site grants are replaced, not changed');
END;

-- Leaving the role 'user' ends every grant.
CREATE TRIGGER users_role_drops_grants
AFTER UPDATE OF role ON users
WHEN NEW.role IS NOT 'user'
BEGIN
    DELETE FROM site_grants WHERE user_id = NEW.id;
END;

-- The audit log keeps the username at the time of the action, so the
-- history stays readable after the account is deleted.
ALTER TABLE audit_log ADD COLUMN actor TEXT NOT NULL DEFAULT '' CHECK (length(actor) <= 64);
UPDATE audit_log SET actor = coalesce((SELECT username FROM users WHERE users.id = audit_log.user_id), '');

CREATE TRIGGER audit_log_actor_no_update
BEFORE UPDATE OF actor ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
