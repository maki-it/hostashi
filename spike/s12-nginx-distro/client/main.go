// Command client fetches a URL over HTTP/1.1, HTTP/2 or HTTP/3 from a local
// test server and prints the negotiated protocol and the response encoding.
// Certificate verification is off on purpose (spike only). The body is read
// as sent (no transparent decompression) and its length is printed.
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

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func main() {
	proto := flag.String("proto", "h2", "h1, h2 or h3")
	addr := flag.String("addr", "127.0.0.1:8443", "address to connect to")
	path := flag.String("path", "/", "request path")
	enc := flag.String("enc", "", "Accept-Encoding value to send (empty: none)")
	flag.Parse()

	tc := &tls.Config{InsecureSkipVerify: true, ServerName: "site.test"}
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
		tr := &http.Transport{TLSClientConfig: tc, DialContext: dial, DisableKeepAlives: true, DisableCompression: true}
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
	req, _ := http.NewRequest("GET", "https://site.test"+*path, nil)
	if *enc != "" {
		req.Header.Set("Accept-Encoding", *enc)
	}
	client := &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("ERR", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("status=%d proto=%s content-encoding=%q vary=%q alt-svc=%q bytes=%d\n",
		resp.StatusCode, resp.Proto, resp.Header.Get("Content-Encoding"), resp.Header.Get("Vary"), resp.Header.Get("Alt-Svc"), len(body))
}
