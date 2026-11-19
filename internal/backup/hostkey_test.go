// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

// sshServer accepts connections and runs the handshake with the given host
// key; no login ever succeeds.
func sshServer(t *testing.T, signer ssh.Signer) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	cfg := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
		return nil, errors.New("no")
	}}
	cfg.AddHostKey(signer)

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer c.Close()

				_, _, _, _ = ssh.NewServerConn(c, cfg)
			}()
		}
	}()

	return ln.Addr().String()
}

func TestFetchHostKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)

	key, err := fetchHostKey(context.Background(), sshServer(t, signer), "u1")
	if err != nil {
		t.Fatal(err)
	}

	if ssh.FingerprintSHA256(key) != ssh.FingerprintSHA256(signer.PublicKey()) {
		t.Error("wrong key")
	}

	weak, _ := rsa.GenerateKey(rand.Reader, 2048)
	ws, _ := ssh.NewSignerFromKey(weak)

	if _, err := fetchHostKey(context.Background(), sshServer(t, ws), "u1"); !errors.Is(err, ErrBadHostKey) {
		t.Errorf("weak RSA host key: %v", err)
	}

	if _, err := FetchHostKey(context.Background(), Target{User: "u1", Host: "localhost", Port: 22, Path: "x"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid target: %v", err)
	}
}
