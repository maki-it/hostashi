-- SPDX-License-Identifier: AGPL-3.0-or-later
-- A scan can cover only the files and folders someone picked in the file
-- manager (ADR-0064, addendum 2026-11-25). paths holds them as a JSON
-- array of paths relative to the home; '[]' is the whole home. Open
-- findings outside the paths stay open when such a scan does not see them.

ALTER TABLE scan_runs ADD COLUMN paths TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(paths) AND json_type(paths) = 'array' AND length(paths) <= 500000);
