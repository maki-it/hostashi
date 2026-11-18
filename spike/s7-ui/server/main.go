// Command server is the Go side of spike S7: a throwaway panel backend that
// serves an embedded SPA, a small REST API, an SSE metrics stream and a PTY
// terminal over WebSocket. It exists only to test the frontend candidates.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
)

//go:embed all:ui/dist
var uiFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (loopback only)")
	token := flag.String("token", "", "API token (random if empty)")
	term := flag.Bool("terminal", false, "enable the PTY terminal (runs a shell as the server user, spike only)")
	flag.Parse()

	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		log.Fatalf("spike server refuses non-loopback address %q", *addr)
	}
	if *token == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			log.Fatal(err)
		}
		*token = hex.EncodeToString(b)
	}

	dist, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		log.Fatal(err)
	}
	s := newSites()
	mux := http.NewServeMux()
	mux.Handle("GET /api/sites", auth(*token, http.HandlerFunc(s.list)))
	mux.Handle("POST /api/sites", auth(*token, http.HandlerFunc(s.create)))
	mux.Handle("GET /api/metrics/stream", auth(*token, http.HandlerFunc(metricsStream)))
	if *term {
		mux.Handle("GET /api/terminal", auth(*token, http.HandlerFunc(terminal)))
	}
	mux.Handle("/", spa(dist))

	fmt.Fprintf(os.Stderr, "listening on http://%s/#token=%s (terminal=%v)\n", *addr, *token, *term)
	srv := &http.Server{Addr: *addr, Handler: secHeaders(mux)}
	log.Fatal(srv.ListenAndServe())
}

// auth accepts the token as a bearer header or, for WebSocket handshakes
// (browsers cannot set headers there), as a query parameter.
func auth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" && r.Header.Get("Upgrade") == "websocket" {
			got = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Strict CSP: no inline script, no eval. xterm.js and uPlot need
		// inline styles, so style-src allows 'unsafe-inline' (see report).
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws://127.0.0.1:* ws://localhost:*; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// spa serves files from dist and falls back to index.html for client routes.
func spa(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			if strings.HasPrefix(p, "assets/") || strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}
