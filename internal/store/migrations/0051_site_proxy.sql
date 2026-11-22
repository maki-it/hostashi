-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Reverse proxy sites (ADR-0054), without a Linux account like redirect
-- sites (migration 0050):
-- proxy_target: the http(s) URL requests go to; '' unless the site is or
--   was a reverse proxy. Kept after a type change, so switching back
--   restores it.
-- proxy_target_host: send the target's host as Host instead of the
--   visitor's.
-- proxy_insecure: do not verify the certificate of an https target.

ALTER TABLE sites ADD COLUMN proxy_target TEXT NOT NULL DEFAULT ''
    CHECK (length(proxy_target) <= 2048 AND (proxy_target = '' OR proxy_target GLOB 'http://?*' OR proxy_target GLOB 'https://?*'));
ALTER TABLE sites ADD COLUMN proxy_target_host INTEGER NOT NULL DEFAULT 0 CHECK (proxy_target_host IN (0, 1));
ALTER TABLE sites ADD COLUMN proxy_insecure INTEGER NOT NULL DEFAULT 0 CHECK (proxy_insecure IN (0, 1));
