// SPDX-License-Identifier: AGPL-3.0-or-later

package dnsq

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type record struct {
	name, cname string
	ip          string
	ttl         uint32
}

// zone answers like a recursive resolver: the whole CNAME chain and the
// final records. truncate makes the UDP answer truncated.
type zone struct {
	records  []record
	truncate bool
	badID    bool
}

func (z *zone) answer(t *testing.T, q []byte, tcp bool) []byte {
	t.Helper()

	var m dnsmessage.Message
	if err := m.Unpack(q); err != nil {
		t.Fatal(err)
	}

	qu := m.Questions[0]
	resp := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: m.ID, Response: true, RecursionAvailable: true},
		Questions: m.Questions,
	}

	if z.badID {
		resp.ID++
	}

	if z.truncate && !tcp {
		resp.Truncated = true

		b, _ := resp.Pack()

		return b
	}

	name, found := strings.ToLower(qu.Name.String()), false

	for range 10 {
		next := ""

		for _, r := range z.records {
			if r.name != name {
				continue
			}

			found = true
			h := dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(r.name), Class: dnsmessage.ClassINET, TTL: r.ttl}

			switch ip := netip.MustParseAddr(orDefault(r.ip, "0.0.0.0")); {
			case r.cname != "":
				h.Type = dnsmessage.TypeCNAME
				resp.Answers = append(resp.Answers, dnsmessage.Resource{Header: h, Body: &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(r.cname)}})
				next = r.cname
			case ip.Is4() && qu.Type == dnsmessage.TypeA:
				h.Type = dnsmessage.TypeA
				resp.Answers = append(resp.Answers, dnsmessage.Resource{Header: h, Body: &dnsmessage.AResource{A: ip.As4()}})
			case ip.Is6() && qu.Type == dnsmessage.TypeAAAA:
				h.Type = dnsmessage.TypeAAAA
				resp.Answers = append(resp.Answers, dnsmessage.Resource{Header: h, Body: &dnsmessage.AAAAResource{AAAA: ip.As16()}})
			}
		}

		if next == "" {
			break
		}

		name = next
	}

	if !found {
		resp.RCode = dnsmessage.RCodeNameError
	}

	b, err := resp.Pack()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}

	return s
}

// serve runs the zone on a local UDP and TCP port and returns its address.
func serve(t *testing.T, z *zone) netip.AddrPort {
	t.Helper()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := netip.MustParseAddrPort(pc.LocalAddr().String())

	ln, err := net.Listen("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = pc.Close(); _ = ln.Close() })

	go func() {
		buf := make([]byte, 4096)

		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}

			_, _ = pc.WriteTo(z.answer(t, buf[:n], false), from)
		}
	}()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			var l [2]byte
			if _, err := io.ReadFull(c, l[:]); err == nil {
				q := make([]byte, binary.BigEndian.Uint16(l[:]))
				if _, err := io.ReadFull(c, q); err == nil {
					a := z.answer(t, q, true)
					_, _ = c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(a))), a...))
				}
			}

			_ = c.Close()
		}
	}()

	return addr
}

func lookup(t *testing.T, z *zone, name string) (Result, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	return (&Client{Servers: []netip.AddrPort{serve(t, z)}}).Lookup(ctx, name)
}

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}

	return out
}

func TestLookup(t *testing.T) {
	t.Parallel()

	z := &zone{records: []record{
		{name: "blog.example.com.", ip: "203.0.113.7", ttl: 300},
		{name: "blog.example.com.", ip: "2001:db8::7", ttl: 3600},
		{name: "www.example.com.", cname: "blog.example.com.", ttl: 86400},
		{name: "loop.example.com.", cname: "loop2.example.com.", ttl: 60},
		{name: "loop2.example.com.", cname: "loop.example.com.", ttl: 60},
	}}

	r, err := lookup(t, z, "blog.example.com")
	if err != nil || !slices.Equal(r.Addrs, addrs("203.0.113.7", "2001:db8::7")) || r.TTL != time.Hour {
		t.Fatalf("direct: %+v %v", r, err)
	}

	// The CNAME's TTL counts too: a cached CNAME keeps the old target.
	r, err = lookup(t, z, "WWW.example.com.")
	if err != nil || len(r.Addrs) != 2 || r.TTL != 24*time.Hour {
		t.Fatalf("cname: %+v %v", r, err)
	}

	for _, name := range []string{"nothing.example.com", "loop.example.com"} {
		if _, err := lookup(t, z, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLookupTruncatedUsesTCP(t *testing.T) {
	t.Parallel()

	z := &zone{truncate: true, records: []record{{name: "a.example.com.", ip: "198.51.100.1", ttl: 120}}}

	r, err := lookup(t, z, "a.example.com")
	if err != nil || !slices.Equal(r.Addrs, addrs("198.51.100.1")) || r.TTL != 2*time.Minute {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestLookupRefusesForeignAnswer(t *testing.T) {
	t.Parallel()

	z := &zone{badID: true, records: []record{{name: "a.example.com.", ip: "198.51.100.1", ttl: 120}}}

	if _, err := lookup(t, z, "a.example.com"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("answer with another id: %v", err)
	}
}

func TestNoServer(t *testing.T) {
	t.Parallel()

	if _, err := (&Client{}).Lookup(t.Context(), "a.example.com"); !errors.Is(err, ErrNoServer) {
		t.Fatal(err)
	}
}

func TestFromResolvConf(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(p, []byte("# x\nsearch example.com\nnameserver 192.0.2.53\nnameserver fe80::1%eth0\nnameserver bogus\noptions edns0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := FromResolvConf(p)
	if err != nil || len(c.Servers) != 2 || c.Servers[0].String() != "192.0.2.53:53" || c.Servers[1].String() != "[fe80::1]:53" {
		t.Fatalf("%+v %v", c, err)
	}

	if err := os.WriteFile(p, []byte("search x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := FromResolvConf(p); !errors.Is(err, ErrNoServer) {
		t.Fatalf("empty: %v", err)
	}
}
