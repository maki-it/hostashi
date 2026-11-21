// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build hostashi_fasthash

package auth

import "testing"

// hashCost is cheap so that the many logins in the tests do not spend most
// of the CI time in Argon2id (go test -tags hostashi_fasthash). Verifying
// reads the cost from the stored hash, so both kinds of hash still verify.
var hashCost = argonCost{memoryKiB: 64, time: 1}

// A binary other than a test binary never runs with cheap hashes.
func init() {
	if !testing.Testing() {
		panic("auth: built with the hostashi_fasthash tag, which is for tests only")
	}
}
