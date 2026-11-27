-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Git deploy per site (ADR-0062). site_git holds the repository and how a
-- deploy runs; the panel checks every value with the rules of
-- internal/agentapi/git.go before it stores it. token, ssh_key and
-- webhook_secret are sealed with the panel's secret key (internal/secrets),
-- bound to the site, so a row cannot be moved to another site.
-- site_deploys is the history of deploy runs and rollbacks.

CREATE TABLE site_git (
    site_id        INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    url            TEXT    NOT NULL CHECK (length(url) BETWEEN 1 AND 512),
    branch         TEXT    NOT NULL CHECK (length(branch) BETWEEN 1 AND 255),
    -- relative to the home; becomes the link to the live release
    deploy_dir     TEXT    NOT NULL CHECK (length(deploy_dir) BETWEEN 1 AND 255),
    -- JSON arrays of strings
    shared         TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(shared) AND json_type(shared) = 'array' AND length(shared) <= 16384),
    commands       TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(commands) AND json_type(commands) = 'array' AND length(commands) <= 131072),
    keep_releases  INTEGER NOT NULL DEFAULT 5 CHECK (keep_releases BETWEEN 2 AND 20),
    build_timeout  INTEGER NOT NULL DEFAULT 900 CHECK (build_timeout BETWEEN 60 AND 3600),
    submodules     INTEGER NOT NULL DEFAULT 0 CHECK (submodules IN (0, 1)),
    auth           TEXT    NOT NULL CHECK (auth IN ('none', 'token', 'ssh')),
    token_user     TEXT    NOT NULL DEFAULT '' CHECK (length(token_user) <= 64),
    token          BLOB    CHECK (token IS NULL OR length(token) <= 2048),
    -- the deploy key: sealed OpenSSH private key and its public line
    ssh_key        BLOB    CHECK (ssh_key IS NULL OR length(ssh_key) <= 4096),
    ssh_public     TEXT    NOT NULL DEFAULT '' CHECK (length(ssh_public) <= 512),
    -- the confirmed host key of an SSH repository, as "type base64"
    host_key       TEXT    NOT NULL DEFAULT '' CHECK (length(host_key) <= 8192),
    -- webhook (ADR-0062): random id in the URL, sealed secret; NULL: off
    webhook_id     TEXT    UNIQUE CHECK (webhook_id IS NULL OR (length(webhook_id) = 32 AND webhook_id NOT GLOB '*[^0-9a-f]*')),
    webhook_secret BLOB    CHECK (webhook_secret IS NULL OR length(webhook_secret) <= 512),
    updated_by     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at     INTEGER NOT NULL,
    CHECK (auth <> 'token' OR token IS NOT NULL),
    CHECK ((webhook_id IS NULL) = (webhook_secret IS NULL))
) STRICT;

CREATE TABLE site_deploys (
    id          INTEGER PRIMARY KEY,
    site_id     INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    -- the release directory name below .deploy/releases
    release_dir TEXT    NOT NULL CHECK (length(release_dir) BETWEEN 1 AND 64 AND release_dir NOT GLOB '*[^0-9A-Za-z_-]*'),
    -- deploy: a new release; rollback: back to a kept one
    kind        TEXT    NOT NULL CHECK (kind IN ('deploy', 'rollback')),
    -- what started it
    source      TEXT    NOT NULL CHECK (source IN ('panel', 'api', 'webhook')),
    actor       INTEGER REFERENCES users (id) ON DELETE SET NULL,
    commit_sha  TEXT    NOT NULL DEFAULT '' CHECK (length(commit_sha) <= 64 AND commit_sha NOT GLOB '*[^0-9a-f]*'),
    author      TEXT    NOT NULL DEFAULT '' CHECK (length(author) <= 256),
    message     TEXT    NOT NULL DEFAULT '' CHECK (length(message) <= 1024),
    state       TEXT    NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    error       TEXT    NOT NULL DEFAULT '' CHECK (length(error) <= 4096),
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    finished_at INTEGER
) STRICT;

CREATE INDEX site_deploys_site ON site_deploys (site_id, id);
