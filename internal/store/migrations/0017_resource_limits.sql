-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Per-site resource limits (ADR-0015). Presets are data the admin edits;
-- exactly one is the default for new sites. A site keeps its own copy of
-- the values: preset_id names the preset it follows (a change of the
-- preset is applied to these sites), NULL means custom values. In every
-- limit column 0 means unlimited; the panel never picks that on its own.

CREATE TABLE limit_presets (
    id                INTEGER PRIMARY KEY,
    name              TEXT    NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 64),
    disk_bytes        INTEGER NOT NULL CHECK (disk_bytes >= 0),
    disk_inodes       INTEGER NOT NULL CHECK (disk_inodes >= 0),
    memory_bytes      INTEGER NOT NULL CHECK (memory_bytes >= 0),
    php_workers       INTEGER NOT NULL CHECK (php_workers >= 0),
    tasks             INTEGER NOT NULL CHECK (tasks >= 0),
    cpu_priority      TEXT    NOT NULL CHECK (cpu_priority IN ('low', 'normal', 'high')),
    cpu_quota_percent INTEGER NOT NULL DEFAULT 0 CHECK (cpu_quota_percent >= 0),
    is_default        INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX limit_presets_one_default ON limit_presets (is_default) WHERE is_default = 1;

CREATE TABLE site_limits (
    site_id           INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    preset_id         INTEGER REFERENCES limit_presets (id) ON DELETE SET NULL,
    disk_bytes        INTEGER NOT NULL CHECK (disk_bytes >= 0),
    disk_inodes       INTEGER NOT NULL CHECK (disk_inodes >= 0),
    memory_bytes      INTEGER NOT NULL CHECK (memory_bytes >= 0),
    php_workers       INTEGER NOT NULL CHECK (php_workers >= 0),
    tasks             INTEGER NOT NULL CHECK (tasks >= 0),
    cpu_priority      TEXT    NOT NULL CHECK (cpu_priority IN ('low', 'normal', 'high')),
    cpu_quota_percent INTEGER NOT NULL DEFAULT 0 CHECK (cpu_quota_percent >= 0),
    -- The last attempt to apply the values on the server: 0 and the error
    -- text when it failed, the time and '' when it worked.
    applied_at        INTEGER NOT NULL DEFAULT 0,
    apply_error       TEXT    NOT NULL DEFAULT '',
    updated_at        INTEGER NOT NULL
) STRICT;

CREATE INDEX site_limits_preset ON site_limits (preset_id);

-- The starting presets of ADR-0015; "small" is the default.
INSERT INTO limit_presets (name, disk_bytes, disk_inodes, memory_bytes, php_workers, tasks, cpu_priority, is_default, created_at, updated_at) VALUES
    ('small',  10737418240,  500000,  536870912,  5, 128, 'normal', 1, unixepoch(), unixepoch()),
    ('medium', 26843545600, 1000000, 1073741824, 10, 256, 'high',   0, unixepoch(), unixepoch()),
    ('large',  53687091200, 2000000, 2147483648, 20, 512, 'high',   0, unixepoch(), unixepoch());
