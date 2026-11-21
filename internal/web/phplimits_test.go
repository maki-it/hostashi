// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func renderOne(t *testing.T, s Site) string {
	t.Helper()

	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80"},
		ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x",
	}

	f, err := n.Render(Desired{Sites: []Site{s}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	return string(f.Content["sites/"+s.User+".conf"])
}

func TestPHPLimitsRender(t *testing.T) {
	s := Site{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", PHPSocket: "/run/php/a.sock", BodyMax: 64 << 20, FastCGIReadTimeout: 70}

	conf := renderOne(t, s)
	for _, want := range []string{"\n    client_max_body_size 64m;\n", "\n        fastcgi_read_timeout 70s;\n"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}

	// Without the values nginx keeps its defaults.
	s.BodyMax, s.FastCGIReadTimeout = 0, 0
	if conf := renderOne(t, s); strings.Contains(conf, "client_max_body_size") || strings.Contains(conf, "fastcgi_read_timeout") {
		t.Errorf("limits without values:\n%s", conf)
	}

	// A disabled site serves nothing, so it gets no limits.
	d := Site{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", Disabled: true}
	if conf := renderOne(t, d); strings.Contains(conf, "client_max_body_size") {
		t.Errorf("disabled site:\n%s", conf)
	}
}

func TestPHPLimitsServerSnippetWins(t *testing.T) {
	s := Site{
		User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a/htdocs", Home: "/srv/a", PHPSocket: "/run/php/a.sock",
		BodyMax: 64 << 20, FastCGIReadTimeout: 70,
		Snippets: Snippets{Server: "client_max_body_size 300m;\nfastcgi_read_timeout 600;\n"},
	}

	conf := renderOne(t, s)
	if strings.Contains(conf, "client_max_body_size 64m") || strings.Contains(conf, "fastcgi_read_timeout 70s") {
		t.Errorf("own limits next to the snippet's:\n%s", conf)
	}

	// A location / snippet does not clash: the server level keeps ours.
	s.Snippets = Snippets{Root: "client_max_body_size 300m;\n"}
	if conf := renderOne(t, s); !strings.Contains(conf, "client_max_body_size 64m;") {
		t.Errorf("root snippet dropped our limit:\n%s", conf)
	}
}

func TestPHPLimitsValidate(t *testing.T) {
	base := Site{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", PHPSocket: "/run/php/a.sock"}

	bad := map[string]func(*Site){
		"no PHP":         func(s *Site) { s.PHPSocket = ""; s.BodyMax = 1 << 20 },
		"no PHP timeout": func(s *Site) { s.PHPSocket = ""; s.FastCGIReadTimeout = 60 },
		"not MiB":        func(s *Site) { s.BodyMax = 1<<20 + 1 },
		"too large":      func(s *Site) { s.BodyMax = 3 << 30 },
		"timeout low":    func(s *Site) { s.FastCGIReadTimeout = 59 },
		"timeout high":   func(s *Site) { s.FastCGIReadTimeout = 3701 },
	}
	for name, f := range bad {
		s := base
		f(&s)

		if err := (Desired{Sites: []Site{s}}).Validate(); err == nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	ok := base
	ok.BodyMax, ok.FastCGIReadTimeout = 2<<30, 3700

	if err := (Desired{Sites: []Site{ok}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

// post sends a POST with a body of n bytes and returns the status.
func (e *env) post(t *testing.T, host, p string, n int) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(e.port)+p, bytes.NewReader(make([]byte, n)))
	if err != nil {
		t.Fatal(err)
	}

	req.Host = host
	// Like curl: nginx answers 413 to the headers, before the body.
	req.Header.Set("Expect", "100-continue")

	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{ExpectContinueTimeout: time.Second}}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", p, err)
	}

	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode
}

// drainServer is a FastCGI pool that reads the whole body, as PHP does,
// and answers with its length.
func drainServer(t *testing.T, sock string) {
	t.Helper()

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	go func() {
		_ = fcgi.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n, _ := io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, strconv.FormatInt(n, 10))
		}))
	}()
}

// TestPHPBodyLimitServed checks with the real nginx that the body size
// follows BodyMax: today's 1 MiB default would refuse a 2 MiB upload.
func TestPHPBodyLimitServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.site(t, "alpha", "static", "alpha.example.com")
	s.PHPSocket = filepath.Join(e.dir, "php.sock")
	drainServer(t, s.PHPSocket)

	if err := os.WriteFile(filepath.Join(s.Root, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	if code := e.post(t, "alpha.example.com", "/index.php", 2<<20); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("2 MiB with the default: %d, want 413", code)
	}

	s.BodyMax, s.FastCGIReadTimeout = 4<<20, 70
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	if code := e.post(t, "alpha.example.com", "/index.php", 2<<20); code != http.StatusOK {
		t.Fatalf("2 MiB with 4 MiB allowed: %d, want 200", code)
	}

	if code := e.post(t, "alpha.example.com", "/index.php", 5<<20); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("5 MiB with 4 MiB allowed: %d, want 413", code)
	}

	// A server snippet with its own value passes nginx -t.
	s.Home = filepath.Dir(s.Root)
	s.Snippets = Snippets{Server: "client_max_body_size 8m;\nfastcgi_read_timeout 300;\n"}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	if code := e.post(t, "alpha.example.com", "/index.php", 5<<20); code != http.StatusOK {
		t.Fatalf("5 MiB with the snippet's 8 MiB: %d, want 200", code)
	}
}
