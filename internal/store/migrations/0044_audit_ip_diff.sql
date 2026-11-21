-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The audit log gets the client's address and the change of an entry as
-- columns of their own (05-security.md: actor, source IP, action, target,
-- diff without secrets).
--
-- ip: the address of the request that caused the entry; empty for work the
-- panel does by itself, such as a scheduled job.
-- diff: the fields an update changed, {"field": {"old": "...", "new": "..."}};
-- {} for entries that create, delete or only report something.

ALTER TABLE audit_log ADD COLUMN ip TEXT NOT NULL DEFAULT '' CHECK (length(ip) <= 45);
ALTER TABLE audit_log ADD COLUMN diff TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(diff) AND json_type(diff) = 'object');

-- Older entries kept the address in detail.ip, where they had one.
UPDATE audit_log SET ip = json_extract(detail, '$.ip')
WHERE json_type(detail, '$.ip') = 'text' AND length(json_extract(detail, '$.ip')) BETWEEN 1 AND 45;

CREATE INDEX audit_log_ip ON audit_log (ip) WHERE ip <> '';

CREATE TRIGGER audit_log_ip_diff_no_update
BEFORE UPDATE OF ip, diff ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
