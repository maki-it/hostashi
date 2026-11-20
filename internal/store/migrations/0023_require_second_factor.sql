-- SPDX-License-Identifier: AGPL-3.0-or-later
-- An admin can require a second factor of an account with the role user
-- (ADR-0035). Admins and managers always need one; the code decides that
-- from the role, so this flag only matters for the role user.
ALTER TABLE users ADD COLUMN require_second_factor INTEGER NOT NULL DEFAULT 0
    CHECK (require_second_factor IN (0, 1));
