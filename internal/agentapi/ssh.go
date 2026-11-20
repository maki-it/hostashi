// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/sshkeys"
)

// SSHKey is one public key of a site (ADR-0032), in the canonical form
// sshkeys.Parse returns.
type SSHKey struct {
	Label  string         `json:"label"`
	Access sshkeys.Access `json:"access"`
	Public string         `json:"public"`
}

// SSHApplyRequest replaces all keys of a site. The panel database holds the
// desired state; every change sends the complete list.
type SSHApplyRequest struct {
	Site string   `json:"site"`
	Keys []SSHKey `json:"keys"`
}

// Validate checks the site and parses every key again.
func (r SSHApplyRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	return sshkeys.Check(r.KeyList())
}

// KeyList returns the keys in sshkeys form.
func (r SSHApplyRequest) KeyList() []sshkeys.Key {
	keys := make([]sshkeys.Key, len(r.Keys))
	for i, k := range r.Keys {
		keys[i] = sshkeys.Key{Label: k.Label, Access: k.Access, Public: k.Public}
	}

	return keys
}

// SSHApplied answers ssh.apply.
type SSHApplied struct {
	// Shell reports whether the site user now has a login shell.
	Shell bool `json:"shell"`
}

// SSHApply writes a site's key file and sets its login shell.
var SSHApply = agentrpc.Op[SSHApplyRequest, SSHApplied]{Name: "ssh.apply"}
