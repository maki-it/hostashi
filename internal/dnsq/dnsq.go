// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dnsq asks the server's recursive resolver for the A and AAAA
// records of a name and reports their TTL (ADR-0031). Go's resolver hides
// the TTL; the cut-over shows it so that the admin lowers it in time.
//
// The answer of a caching resolver carries the time left in its cache, so
// the TTL shown is at most the one set in the zone.
package dnsq

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Errors of a lookup.
var (
	ErrNotFound = errors.New("dnsq: no such domain or no address")
	ErrNoServer = errors.New("dnsq: no name server configured")
)

// maxCNAMEs bounds the chain followed inside one answer.
const maxCNAMEs = 8

// Result is the addresses of a name and the largest TTL of the records
// that led to them.
type Result struct {
	Addrs []netip.Addr
	TTL   time.Duration
}

// Client queries the name servers in order until one answers.
type Client struct {
	Servers []netip.AddrPort
	// Dial opens a connection, net.Dialer.DialContext if nil.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// FromResolvConf returns a client for the name servers of a resolv.conf.
func FromResolvConf(path string) (*Client, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("dnsq: %w", err)
	}

	defer func() { _ = f.Close() }()

	c := &Client{}
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))

	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}

		a, err := netip.ParseAddr(fields[1])
		if err != nil {
			continue
		}

		c.Servers = append(c.Servers, netip.AddrPortFrom(a.WithZone(""), 53))
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("dnsq: %w", err)
	}

	if len(c.Servers) == 0 {
		return nil, ErrNoServer
	}

	return c, nil
}

// Lookup returns the A and AAAA addresses of name. A name without any
// address is ErrNotFound.
func (c *Client) Lookup(ctx context.Context, name string) (Result, error) {
	if len(c.Servers) == 0 {
		return Result{}, ErrNoServer
	}

	if !strings.HasSuffix(name, ".") {
		name += "."
	}

	n, err := dnsmessage.NewName(name)
	if err != nil {
		return Result{}, fmt.Errorf("dnsq: %w", err)
	}

	var out Result

	for _, t := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		r, err := c.query(ctx, n, t)
		if errors.Is(err, ErrNotFound) {
			continue
		}

		if err != nil {
			return Result{}, err
		}

		out.Addrs = append(out.Addrs, r.Addrs...)
		out.TTL = max(out.TTL, r.TTL)
	}

	if len(out.Addrs) == 0 {
		return Result{}, ErrNotFound
	}

	slices.SortFunc(out.Addrs, netip.Addr.Compare)
	out.Addrs = slices.Compact(out.Addrs)

	return out, nil
}

func (c *Client) query(ctx context.Context, n dnsmessage.Name, t dnsmessage.Type) (Result, error) {
	var last error

	for _, s := range c.Servers {
		r, err := c.ask(ctx, s, n, t)
		if err == nil || errors.Is(err, ErrNotFound) {
			return r, err
		}

		last = err

		if ctx.Err() != nil {
			break
		}
	}

	return Result{}, last
}

func (c *Client) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if c.Dial != nil {
		return c.Dial(ctx, network, addr)
	}

	var d net.Dialer

	return d.DialContext(ctx, network, addr)
}

// ask sends one query over UDP and repeats it over TCP if the answer was
// truncated.
func (c *Client) ask(ctx context.Context, server netip.AddrPort, n dnsmessage.Name, t dnsmessage.Type) (Result, error) {
	var idb [2]byte

	_, _ = rand.Read(idb[:]) // crypto/rand.Read never fails on Linux (Go 1.24+)

	id := binary.BigEndian.Uint16(idb[:])

	q := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: n, Type: t, Class: dnsmessage.ClassINET}},
	}

	b, err := q.Pack()
	if err != nil {
		return Result{}, fmt.Errorf("dnsq: %w", err)
	}

	resp, err := c.exchange(ctx, "udp", server, b)
	if err != nil {
		return Result{}, err
	}

	var m dnsmessage.Message
	if err := m.Unpack(resp); err != nil {
		return Result{}, fmt.Errorf("dnsq: bad answer: %w", err)
	}

	if m.Truncated {
		if resp, err = c.exchange(ctx, "tcp", server, b); err != nil {
			return Result{}, err
		}

		if err := m.Unpack(resp); err != nil {
			return Result{}, fmt.Errorf("dnsq: bad answer: %w", err)
		}
	}

	return parse(&m, id, n, t)
}

func (c *Client) exchange(ctx context.Context, network string, server netip.AddrPort, q []byte) ([]byte, error) {
	conn, err := c.dial(ctx, network, server.String())
	if err != nil {
		return nil, fmt.Errorf("dnsq: %w", err)
	}

	defer func() { _ = conn.Close() }()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}

	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("dnsq: %w", err)
	}

	if network == "tcp" {
		msg := binary.BigEndian.AppendUint16(nil, uint16(len(q))) //nolint:gosec // a packed query is far below 64 KiB
		if _, err := conn.Write(append(msg, q...)); err != nil {
			return nil, fmt.Errorf("dnsq: %w", err)
		}

		var l [2]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return nil, fmt.Errorf("dnsq: %w", err)
		}

		buf := make([]byte, binary.BigEndian.Uint16(l[:]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, fmt.Errorf("dnsq: %w", err)
		}

		return buf, nil
	}

	if _, err := conn.Write(q); err != nil {
		return nil, fmt.Errorf("dnsq: %w", err)
	}

	buf := make([]byte, 4096)

	k, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("dnsq: %w", err)
	}

	return buf[:k], nil
}

// parse takes the records of type t for n, following CNAMEs inside the
// answer section.
func parse(m *dnsmessage.Message, id uint16, n dnsmessage.Name, t dnsmessage.Type) (Result, error) {
	if !m.Response || m.ID != id || len(m.Questions) != 1 || m.Questions[0].Name != n || m.Questions[0].Type != t {
		return Result{}, errors.New("dnsq: answer does not match the question")
	}

	switch m.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		return Result{}, ErrNotFound
	case dnsmessage.RCodeFormatError, dnsmessage.RCodeServerFailure, dnsmessage.RCodeNotImplemented, dnsmessage.RCodeRefused:
		return Result{}, fmt.Errorf("dnsq: server answered %v", m.RCode)
	default:
		return Result{}, fmt.Errorf("dnsq: server answered %v", m.RCode)
	}

	var out Result

	want := n

	for range maxCNAMEs + 1 {
		next := want

		for _, a := range m.Answers {
			if !strings.EqualFold(a.Header.Name.String(), want.String()) || a.Header.Class != dnsmessage.ClassINET {
				continue
			}

			ttl := time.Duration(a.Header.TTL) * time.Second

			switch b := a.Body.(type) {
			case *dnsmessage.AResource:
				if t == dnsmessage.TypeA {
					out.Addrs = append(out.Addrs, netip.AddrFrom4(b.A))
					out.TTL = max(out.TTL, ttl)
				}
			case *dnsmessage.AAAAResource:
				if t == dnsmessage.TypeAAAA {
					out.Addrs = append(out.Addrs, netip.AddrFrom16(b.AAAA).Unmap())
					out.TTL = max(out.TTL, ttl)
				}
			case *dnsmessage.CNAMEResource:
				next = b.CNAME
				out.TTL = max(out.TTL, ttl)
			}
		}

		if len(out.Addrs) > 0 || next == want {
			break
		}

		want = next
	}

	if len(out.Addrs) == 0 {
		return Result{}, ErrNotFound
	}

	return out, nil
}
