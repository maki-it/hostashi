// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

// WPCLIRelease is the pinned WP-CLI build (ADR-0069). Hostashi downloads
// the phar of the GitHub release and checks it against SHA512 before it
// is moved into place; nothing it downloads is run as root.
type WPCLIRelease struct {
	Version string
	// SHA512 of wp-cli-<Version>.phar, from the release's .sha512 file.
	SHA512 string
}

// WPCLI is the WP-CLI release Hostashi installs. Checked on 2026-11-25
// against wp-cli-2.12.0.phar.sha512 of the GitHub release, which matches
// the stable build in wp-cli/builds.
var WPCLI = WPCLIRelease{
	Version: "2.12.0",
	SHA512:  "be928f6b8ca1e8dfb9d2f4b75a13aa4aee0896f8a9a0a1c45cd5d2c98605e6172e6d014dda2e27f88c98befc16c040cbb2bd1bfa121510ea5cdf5f6a30fe8832",
}
