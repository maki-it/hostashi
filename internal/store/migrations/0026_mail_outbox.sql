-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Outgoing email waiting for the SMTP relay (ADR-0044). The body holds
-- subject and text sealed with the server key, because invitation and reset
-- links are secrets; it is emptied as soon as a message is sent or has
-- failed for good. Times are Unix seconds.

CREATE TABLE mail_outbox (
    id           INTEGER PRIMARY KEY,
    created_at   INTEGER NOT NULL,
    recipient    TEXT    NOT NULL CHECK (length(recipient) BETWEEN 3 AND 254),
    template     TEXT    NOT NULL CHECK (length(template) BETWEEN 1 AND 64),
    category     TEXT    NOT NULL CHECK (category IN ('account', 'operations')),
    -- Same event for the same object; empty when the message is not deduplicated.
    dedup_key    TEXT    NOT NULL DEFAULT '' CHECK (length(dedup_key) <= 200),
    body         BLOB,
    state        TEXT    NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'sent', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt INTEGER NOT NULL,
    done_at      INTEGER NOT NULL DEFAULT 0,
    -- The SMTP status line of the last failure, never a body or a link.
    last_error   TEXT    NOT NULL DEFAULT '' CHECK (length(last_error) <= 300),
    message_id   TEXT    NOT NULL DEFAULT '' CHECK (length(message_id) <= 300),
    CHECK ((state = 'queued') = (body IS NOT NULL))
) STRICT;

CREATE INDEX mail_outbox_due ON mail_outbox (state, next_attempt);
CREATE INDEX mail_outbox_recipient ON mail_outbox (recipient, done_at);
CREATE INDEX mail_outbox_dedup ON mail_outbox (dedup_key, created_at) WHERE dedup_key != '';
