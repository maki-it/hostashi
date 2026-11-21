// SPDX-License-Identifier: AGPL-3.0-or-later

// Package smtptest is a tiny SMTP server for tests (ADR-0044: no network
// in CI). It speaks just enough of RFC 5321 for the mail package: EHLO,
// STARTTLS, AUTH PLAIN and LOGIN, MAIL, RCPT, DATA, RSET, NOOP and QUIT,
// on 127.0.0.1 with a fresh self-signed certificate.
package smtptest

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mode is what the server offers.
type Mode int

const (
	// StartTLS offers STARTTLS and AUTH only after it.
	StartTLS Mode = iota
	// Implicit speaks TLS from the first byte.
	Implicit
	// NoTLS offers neither STARTTLS nor TLS, like a plain relay.
	NoTLS
	// Silent accepts connections and never answers.
	Silent
)

// Received is one message the server accepted.
type Received struct {
	From string
	To   []string
	Data string
	User string // who logged in, empty without AUTH
}

// Server is a running test server.
type Server struct {
	Addr  string // 127.0.0.1:port
	Host  string // 127.0.0.1
	Port  int
	CAPEM string // the certificate, to trust as CA

	// Users maps user names to passwords; empty means no AUTH is offered.
	Users map[string]string
	// RejectRcpt makes every RCPT TO answer 550.
	RejectRcpt bool
	// TempFail makes every RCPT TO answer 451.
	TempFail bool

	mode Mode
	ln   net.Listener
	tls  *tls.Config

	mu   sync.Mutex
	msgs []Received
}

// New starts a server and stops it at the end of the test.
func New(t *testing.T, mode Mode) *Server {
	t.Helper()

	certPEM, cert := selfSigned(t)

	s := &Server{
		CAPEM: string(certPEM),
		mode:  mode,
		tls:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	if mode == Implicit {
		ln = tls.NewListener(ln, s.tls)
	}

	s.ln = ln
	s.Addr = ln.Addr().String()
	s.Host = "127.0.0.1"

	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		s.Port = a.Port
	}

	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			wg.Go(func() { s.serve(c) })
		}
	})

	t.Cleanup(func() {
		_ = ln.Close()

		wg.Wait()
	})

	return s
}

// Messages returns what the server accepted so far.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Received(nil), s.msgs...)
}

func (s *Server) serve(c net.Conn) {
	defer func() { _ = c.Close() }()

	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	if s.mode == Silent {
		_, _ = c.Read(make([]byte, 1))

		return
	}

	_, secure := c.(*tls.Conn)
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)

	say := func(lines ...string) {
		for _, l := range lines {
			_, _ = w.WriteString(l + "\r\n")
		}

		_ = w.Flush()
	}

	say("220 smtptest ESMTP")

	var (
		cur  Received
		user string
	)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimRight(line, "\r\n")
		verb, arg, _ := strings.Cut(line, " ")

		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			ext := []string{"250-smtptest"}
			if s.mode == StartTLS && !secure {
				ext = append(ext, "250-STARTTLS")
			}

			if len(s.Users) > 0 && secure {
				ext = append(ext, "250-AUTH PLAIN LOGIN")
			}

			ext = append(ext, "250 8BITMIME")
			say(ext...)
		case "STARTTLS":
			if s.mode != StartTLS || secure {
				say("502 not offered")

				continue
			}

			say("220 go ahead")

			tc := tls.Server(c, s.tls)
			if err := tc.HandshakeContext(context.Background()); err != nil {
				return
			}

			c, secure = tc, true
			r, w = bufio.NewReader(tc), bufio.NewWriter(tc)
			cur, user = Received{}, ""
		case "AUTH":
			u, ok := s.auth(arg, r, say)
			if !ok {
				say("535 5.7.8 authentication failed")

				continue
			}

			user = u

			say("235 2.7.0 ok")
		case "MAIL":
			if len(s.Users) > 0 && user == "" {
				say("530 5.7.0 authentication required")

				continue
			}

			cur = Received{From: addr(arg), User: user}

			say("250 ok")
		case "RCPT":
			switch {
			case s.RejectRcpt:
				say("550 5.1.1 no such user")
			case s.TempFail:
				say("451 4.3.0 try again later")
			default:
				cur.To = append(cur.To, addr(arg))

				say("250 ok")
			}
		case "DATA":
			if len(cur.To) == 0 {
				say("554 no valid recipients")

				continue
			}

			say("354 go ahead")

			data, err := readData(r)
			if err != nil {
				return
			}

			cur.Data = data

			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()

			cur = Received{User: user}

			say("250 2.0.0 queued")
		case "RSET":
			cur = Received{User: user}

			say("250 ok")
		case "NOOP":
			say("250 ok")
		case "QUIT":
			say("221 bye")

			return
		default:
			say("500 unknown command")
		}
	}
}

func (s *Server) auth(arg string, r *bufio.Reader, say func(...string)) (string, bool) {
	mech, initial, _ := strings.Cut(arg, " ")

	readLine := func() string {
		l, _ := r.ReadString('\n')

		return strings.TrimRight(l, "\r\n")
	}

	decode := func(s string) string {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return "\x00invalid"
		}

		return string(b)
	}

	var u, p string

	switch strings.ToUpper(mech) {
	case "PLAIN":
		if initial == "" {
			say("334 ")

			initial = readLine()
		}

		parts := strings.Split(decode(initial), "\x00")
		if len(parts) != 3 {
			return "", false
		}

		u, p = parts[1], parts[2]
	case "LOGIN":
		say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))

		u = decode(readLine())

		say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))

		p = decode(readLine())
	default:
		return "", false
	}

	want, ok := s.Users[u]

	return u, ok && want == p
}

func addr(arg string) string {
	_, a, _ := strings.Cut(arg, ":")
	a, _, _ = strings.Cut(strings.TrimSpace(a), " ")

	return strings.Trim(a, "<>")
}

var errTooLong = errors.New("smtptest: message too long")

func readData(r *bufio.Reader) (string, error) {
	var b strings.Builder

	for {
		l, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}

		if l == ".\r\n" {
			return b.String(), nil
		}

		if strings.HasPrefix(l, "..") {
			l = l[1:]
		}

		b.WriteString(l)

		if b.Len() > 1<<20 {
			return "", errTooLong
		}
	}
}

func selfSigned(t *testing.T) ([]byte, tls.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "smtptest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
	if err != nil {
		t.Fatal(fmt.Errorf("smtptest: %w", err))
	}

	return certPEM, cert
}
