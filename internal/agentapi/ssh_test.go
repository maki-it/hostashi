// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/sshkeys"
)

func TestSSHApplyRequestValidate(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	key := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")

	ok := SSHApplyRequest{Site: "blog", Keys: []SSHKey{{Label: "kim", Access: sshkeys.Shell, Public: key}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}

	if err := (SSHApplyRequest{Site: "blog"}).Validate(); err != nil {
		t.Fatalf("empty list: %v", err)
	}

	for name, r := range map[string]SSHApplyRequest{
		"bad site":   {Site: "../x"},
		"root":       {Site: "root"},
		"bad access": {Site: "blog", Keys: []SSHKey{{Label: "kim", Access: "all", Public: key}}},
		"options":    {Site: "blog", Keys: []SSHKey{{Label: "kim", Access: sshkeys.SFTP, Public: "restrict " + key}}},
		"label":      {Site: "blog", Keys: []SSHKey{{Label: "a\nb", Access: sshkeys.SFTP, Public: key}}},
		"duplicate":  {Site: "blog", Keys: []SSHKey{{Label: "a", Access: sshkeys.SFTP, Public: key}, {Label: "b", Access: sshkeys.SFTP, Public: key}}},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
