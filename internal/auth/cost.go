// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !hostashi_fasthash

package auth

// hashCost is the cost of new password hashes.
var hashCost = productionCost
