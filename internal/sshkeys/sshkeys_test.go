// SPDX-License-Identifier: AGPL-3.0-or-later

package sshkeys

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func authorized(t *testing.T, pub any) string {
	t.Helper()

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
}

func edKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	return authorized(t, pub), priv
}

func rsaKey(t *testing.T, bits int) string {
	t.Helper()

	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}

	return authorized(t, &k.PublicKey)
}

func TestParseAccepts(t *testing.T) {
	ed, _ := edKey(t)

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	for name, line := range map[string]string{
		"ed25519":         ed,
		"ed25519 comment": ed + " kim@laptop",
		"trailing CRLF":   ed + " kim@laptop\r\n",
		"leading space":   "  " + ed,
		"ecdsa":           authorized(t, &ec.PublicKey),
		"rsa 3072":        rsaKey(t, 3072),
	} {
		t.Run(name, func(t *testing.T) {
			pub, fp, err := Parse(line)
			if err != nil {
				t.Fatal(err)
			}

			if strings.Contains(pub, "kim") || strings.ContainsAny(pub, "\r\n") || strings.Count(pub, " ") != 1 {
				t.Fatalf("not canonical: %q", pub)
			}

			if !strings.HasPrefix(fp, "SHA256:") {
				t.Fatalf("fingerprint %q", fp)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	ed, priv := edKey(t)
	ed2, _ := edKey(t)

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ed2))
	if err != nil {
		t.Fatal(err)
	}

	cert := &ssh.Certificate{Key: pk, CertType: ssh.UserCert, ValidPrincipals: []string{"site"}, ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		t.Fatal(err)
	}

	for name, line := range map[string]string{
		"empty":           "",
		"garbage":         "not a key",
		"private key":     "-----BEGIN OPENSSH PRIVATE KEY-----",
		"options":         "restrict " + ed,
		"command option":  `command="/bin/sh" ` + ed,
		"from option":     `from="*" ` + ed,
		"two lines":       ed + "\n" + ed2,
		"carriage return": ed + "\r" + ed2,
		"nul":             ed + " a\x00b",
		"certificate":     strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(cert)), "\n"),
		"rsa 2048":        rsaKey(t, 2048),
		"too long":        ed + " " + strings.Repeat("a", maxLine),
		"truncated":       ed[:len(ed)-10],
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Parse(line); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestCheckLabel(t *testing.T) {
	for _, ok := range []string{"a", "kim@laptop", "CI deploy 2026", "x.y_z-1+2", strings.Repeat("a", MaxLabel)} {
		if err := CheckLabel(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}

	for _, bad := range []string{"", " a", "a ", "a\nb", "a\tb", `a"b`, "a,b", "a#b", "ä", strings.Repeat("a", MaxLabel+1)} {
		if err := CheckLabel(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRender(t *testing.T) {
	a, _ := edKey(t)
	b, _ := edKey(t)

	keys := []Key{{Label: "kim laptop", Access: SFTP, Public: a}, {Label: "deploy", Access: Shell, Public: b}}

	out, err := Render(keys)
	if err != nil {
		t.Fatal(err)
	}

	want := "# Managed by Hostashi. Changes here are overwritten.\n" +
		`restrict,command="internal-sftp" ` + a + " kim laptop\n" +
		"restrict,pty " + b + " deploy\n"
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}

	// sshd reads the options back exactly as rendered.
	rest := out[strings.IndexByte(string(out), '\n')+1:]
	for i, wantOpts := range [][]string{{"restrict", `command="internal-sftp"`}, {"restrict", "pty"}} {
		_, comment, opts, r, err := ssh.ParseAuthorizedKey(rest)
		if err != nil || strings.Join(opts, ",") != strings.Join(wantOpts, ",") || comment != keys[i].Label {
			t.Fatalf("line %d: opts %v comment %q err %v", i, opts, comment, err)
		}

		rest = r
	}

	if !NeedsShell(keys) || NeedsShell(keys[:1]) || NeedsShell(nil) {
		t.Fatal("NeedsShell")
	}

	empty, err := Render(nil)
	if err != nil || strings.Count(string(empty), "\n") != 1 || !strings.HasPrefix(string(empty), "#") {
		t.Fatalf("empty: %q %v", empty, err)
	}
}

func TestCheckRejects(t *testing.T) {
	a, _ := edKey(t)

	many := make([]Key, MaxKeys+1)
	for i := range many {
		k, _ := edKey(t)
		many[i] = Key{Label: "k", Access: SFTP, Public: k}
	}

	for name, keys := range map[string][]Key{
		"too many":      many,
		"duplicate":     {{Label: "a", Access: SFTP, Public: a}, {Label: "b", Access: Shell, Public: a}},
		"bad access":    {{Label: "a", Access: "root", Public: a}},
		"empty access":  {{Label: "a", Public: a}},
		"bad label":     {{Label: "a\nssh-ed25519 AAAA", Access: SFTP, Public: a}},
		"not canonical": {{Label: "a", Access: SFTP, Public: a + " comment"}},
		"options":       {{Label: "a", Access: SFTP, Public: "restrict " + a}},
		"not a key":     {{Label: "a", Access: SFTP, Public: "ssh-ed25519 AAAA"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(keys); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}

	if err := Check(many[:MaxKeys]); err != nil {
		t.Fatalf("%d keys: %v", MaxKeys, err)
	}
}

// FuzzParse checks that whatever Parse accepts renders to exactly one key
// line with the options this package chose.
func FuzzParse(f *testing.F) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pk, _ := ssh.NewPublicKey(pub)
	line := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")

	f.Add(line)
	f.Add(line + " comment")
	f.Add("restrict " + line)
	f.Add(line + "\n" + line)
	f.Add("")

	f.Fuzz(func(t *testing.T, in string) {
		public, _, err := Parse(in)
		if err != nil {
			return
		}

		out, err := Render([]Key{{Label: "x", Access: SFTP, Public: public}})
		if err != nil {
			t.Fatalf("accepted %q but render failed: %v", in, err)
		}

		body := out[strings.IndexByte(string(out), '\n')+1:]
		if strings.Count(string(body), "\n") != 1 {
			t.Fatalf("%q rendered to %q", in, body)
		}

		_, _, opts, rest, err := ssh.ParseAuthorizedKey(body)
		if err != nil || len(rest) != 0 || strings.Join(opts, ",") != `restrict,command="internal-sftp"` {
			t.Fatalf("%q rendered to %q: opts %v err %v", in, body, opts, err)
		}
	})
}
