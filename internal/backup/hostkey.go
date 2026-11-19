// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// errGotKey ends the handshake once the host key is known.
var errGotKey = errors.New("host key received")

// FetchHostKey connects to an SSH server and returns the host key it
// presents, without logging in. The key is not trusted by this: the panel
// shows its fingerprint and the administrator confirms it against the
// provider's published fingerprints before the agent pins it.
func FetchHostKey(ctx context.Context, t Target) (ssh.PublicKey, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}

	return fetchHostKey(ctx, net.JoinHostPort(t.Host, strconv.Itoa(t.Port)), t.User)
}

func fetchHostKey(ctx context.Context, addr, user string) (ssh.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var d net.Dialer

	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("backup: connect %s: %w", addr, err)
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	var key ssh.PublicKey

	cfg := &ssh.ClientConfig{
		User: user,
		// Ed25519 and ECDSA first, the types ParseHostKey accepts.
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
		},
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			key = k

			return errGotKey
		},
	}

	_, _, _, err = ssh.NewClientConn(conn, addr, cfg)
	if key == nil {
		return nil, fmt.Errorf("backup: no host key from %s: %w", addr, err)
	}

	if _, err := ParseHostKey(string(ssh.MarshalAuthorizedKey(key))); err != nil {
		return nil, err
	}

	return key, nil
}
