// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func pubLine(t *testing.T, key any) string {
	t.Helper()

	pk, err := ssh.NewPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
}

func edLine(t *testing.T) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	return pubLine(t, pub)
}

func codes(fs []Finding) map[string]int {
	out := map[string]int{}
	for _, f := range fs {
		out[f.Code]++
	}

	return out
}

func TestParseAuthorizedKeys(t *testing.T) {
	t.Parallel()

	a, b := edLine(t), edLine(t)

	weak, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	src := strings.Join([]string{
		"# managed by hand",
		"",
		a + " kim@laptop",
		`from="203.0.113.0/24",command="/bin/true",no-pty ` + b + " deploy",
		a + " the same key again",
		pubLine(t, &weak.PublicKey) + " old-rsa",
		"ssh-dss AAAAB3NzaC1kc3MAAACBAP== x",
		"garbage",
	}, "\n")

	keys, fs := parseAuthorizedKeys(src, "/home/alpha/.ssh/authorized_keys", map[string]bool{})

	if len(keys) != 2 || keys[0].Public != a || keys[0].Comment != "kim@laptop" || keys[0].Line != 3 ||
		keys[1].Public != b || keys[1].Comment != "deploy" || !strings.HasPrefix(keys[0].Fingerprint, "SHA256:") {
		t.Fatalf("keys %+v", keys)
	}

	c := codes(fs)
	if c["ssh_key_options"] != 1 || c["ssh_key_rejected"] != 1 || c["ssh_key_unparsed"] != 2 {
		t.Fatalf("findings %+v", fs)
	}

	for _, f := range fs {
		if strings.Contains(f.Msg, "AAAA") || strings.Contains(f.Msg, "203.0.113") {
			t.Errorf("key material or options in a finding: %q", f.Msg)
		}
	}
}

func TestTrimComment(t *testing.T) {
	t.Parallel()

	if got := trimComment(strings.Repeat("ä", 150)); len(got) > maxComment || !strings.HasPrefix(got, "ää") {
		t.Fatalf("long comment: %d bytes", len(got))
	}

	if got := trimComment("bad \xff"); got != "" {
		t.Fatalf("invalid UTF-8 kept: %q", got)
	}
}
