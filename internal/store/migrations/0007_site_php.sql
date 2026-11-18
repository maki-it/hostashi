-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The PHP version of a site's FPM pool (ADR-0019), empty for a site without
-- PHP. The list of accepted versions is in agentapi.PHPVersions.

ALTER TABLE sites ADD COLUMN php TEXT NOT NULL DEFAULT '' CHECK (php = '' OR php GLOB '[0-9].[0-9]');
