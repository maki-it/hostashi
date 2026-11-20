// SPDX-License-Identifier: AGPL-3.0-or-later

package plan

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

func TestKeyLabel(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"kim@laptop":                   "kim@laptop",
		"Kim (Büro) 2024":              "Kim B ro 2024",
		"  ":                           "imported key 3",
		`x"; command="sh`:              "x command sh",
		strings.Repeat("a", 100):       strings.Repeat("a", 64),
		strings.Repeat("a", 63) + " b": strings.Repeat("a", 63),
	} {
		if got := keyLabel(in, 3); got != want {
			t.Errorf("keyLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSSHKeys(t *testing.T) {
	t.Parallel()

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	key := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
	s := scan.Site{SSHKeys: []scan.SSHKey{{Public: key, Comment: ""}, {Public: key, Comment: "twice"}, {Public: "ssh-rsa AAAA"}}}

	got := SSHKeys(s)
	if len(got) != 1 || got[0].Public != key || got[0].Label != "imported key 1" {
		t.Fatalf("%+v", got)
	}
}

func TestSSHKeysOfAdditionalUser(t *testing.T) {
	t.Parallel()

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	key := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")

	for comment, want := range map[string]string{"": "dev", "kim@laptop": "dev kim@laptop"} {
		got := SSHKeys(scan.Site{SSHKeys: []scan.SSHKey{{Public: key, Comment: comment, User: "dev"}}})
		if len(got) != 1 || got[0].Label != want {
			t.Fatalf("comment %q: %+v", comment, got)
		}
	}
}
