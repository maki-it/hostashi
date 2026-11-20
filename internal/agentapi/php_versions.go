// SPDX-License-Identifier: AGPL-3.0-or-later

// This file is rewritten by tools/phpversions, which the weekly "PHP
// versions" workflow runs (ADR-0036). Hand edits are fine as long as the
// layout stays: the tool renders the whole file from these values.

package agentapi

// PHPVersions are the PHP versions Hostashi can run a site on (ADR-0019):
// 8.3 to 8.6 as standard, 8.2, 8.1 and 7.4 as legacy. Whether a version is
// installed is a separate question (PHPVersionsList).
var PHPVersions = []string{"7.4", "8.1", "8.2", "8.3", "8.4", "8.5", "8.6"}

// PHPSecurityUntil is the last day of security fixes per version, from
// php.net/supported-versions.php and php.net/eol.php (ADR-0036). After it
// a version is end of life.
var PHPSecurityUntil = map[string]string{
	"7.4": "2022-11-28",
	"8.1": "2025-12-31",
	"8.2": "2026-12-31",
	"8.3": "2027-12-31",
	"8.4": "2028-12-31",
	"8.5": "2029-12-31",
	"8.6": "2030-12-31",
}

// PHPActiveUntil is the last day of active support (bug fixes) per
// version, same sources. Between it and PHPSecurityUntil a version gets
// security fixes only. Versions that are end of life have no entry.
var PHPActiveUntil = map[string]string{
	"8.2": "2024-12-31",
	"8.3": "2025-12-31",
	"8.4": "2026-12-31",
	"8.5": "2027-12-31",
	"8.6": "2028-12-31",
}
