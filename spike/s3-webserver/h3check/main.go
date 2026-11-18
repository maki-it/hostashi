// Command h3check fetches a URL over HTTP/1.1, HTTP/2 or HTTP/3 from a local
// test server. Certificate verification is off on purpose (spike only).
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func main() {
	proto := flag.String("proto", "h2", "h1, h2 or h3")
	host := flag.String("host", "site01.test", "Host and SNI name")
	addr := flag.String("addr", "127.0.0.1:18443", "address to connect to")
	path := flag.String("path", "/", "request path")
	flag.Parse()

	tc := &tls.Config{InsecureSkipVerify: true, ServerName: *host}
	var rt http.RoundTripper
	switch *proto {
	case "h3":
		tc.NextProtos = []string{"h3"}
		rt = &http3.Transport{
			TLSClientConfig: tc,
			Dial: func(ctx context.Context, _ string, t *tls.Config, c *quic.Config) (*quic.Conn, error) {
				ua, err := net.ResolveUDPAddr("udp", *addr)
				if err != nil {
					return nil, err
				}
				pc, err := net.ListenUDP("udp", nil)
				if err != nil {
					return nil, err
				}
				return quic.Dial(ctx, pc, ua, t, c)
			},
		}
	case "h2", "h1":
		dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, *addr)
		}
		tr := &http.Transport{TLSClientConfig: tc, DialContext: dial, DisableKeepAlives: true}
		if *proto == "h2" {
			tr.ForceAttemptHTTP2 = true
		} else {
			tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		}
		rt = tr
	default:
		fmt.Fprintln(os.Stderr, "unknown proto")
		os.Exit(2)
	}
	client := &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get("https://" + *host + ":18443" + *path)
	if err != nil {
		fmt.Println("ERR", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	fmt.Printf("%d %s", resp.StatusCode, resp.Proto)
	for _, h := range []string{"Location", "Alt-Svc", "Content-Encoding", "Cache-Control", "X-Cache"} {
		if v := resp.Header.Get(h); v != "" {
			fmt.Printf(" %s=%q", h, v)
		}
	}
	fmt.Printf(" body=%q\n", strings.TrimSpace(string(body)))
}
