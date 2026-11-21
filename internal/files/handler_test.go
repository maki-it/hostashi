// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type result struct {
	StatusCode int
	Header     http.Header
}

func call(t *testing.T, h http.Handler, method, target, body string) (result, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	b, _ := io.ReadAll(rec.Body)

	return result{StatusCode: rec.Code, Header: rec.Header()}, string(b)
}

func TestHandler(t *testing.T) {
	r, home, _ := newRoot(t)
	h := &Handler{Root: r}

	res, body := call(t, h, "GET", "/v1/list?path=htdocs", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list: %d %s", res.StatusCode, body)
	}

	var l Listing
	if err := json.Unmarshal([]byte(body), &l); err != nil || len(l.Entries) != 2 {
		t.Fatalf("list: %v %s", err, body)
	}

	res, body = call(t, h, "GET", "/v1/file?path=htdocs/index.php", "")
	if res.StatusCode != http.StatusOK || body != "<?php echo 1;" || res.Header.Get("X-Hostashi-Version") == "" ||
		!strings.Contains(res.Header.Get("Content-Disposition"), `filename=index.php`) {
		t.Fatalf("download: %d %q %v", res.StatusCode, body, res.Header)
	}

	v := res.Header.Get("X-Hostashi-Version")

	res, body = call(t, h, "PUT", "/v1/file?path=htdocs/index.php&version="+url.QueryEscape(v), "saved")
	if res.StatusCode != http.StatusOK || read(t, filepath.Join(home, "htdocs", "index.php")) != "saved" {
		t.Fatalf("save: %d %s", res.StatusCode, body)
	}

	res, body = call(t, h, "PUT", "/v1/file?path=htdocs/index.php&version="+url.QueryEscape(v), "again")
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, `"changed"`) {
		t.Fatalf("stale save: %d %s", res.StatusCode, body)
	}

	cases := []struct {
		target, body string
		status       int
		code         string
	}{
		{"/v1/mkdir", `{"path":"htdocs/new"}`, http.StatusOK, ""},
		{"/v1/mkdir", `{"path":"htdocs/new"}`, http.StatusConflict, "exists"},
		{"/v1/touch", `{"path":"htdocs/new/f.txt"}`, http.StatusOK, ""},
		{"/v1/rename", `{"from":"htdocs/new/f.txt","to":"htdocs/new/g.txt"}`, http.StatusOK, ""},
		{"/v1/copy", `{"paths":["htdocs/new/g.txt"],"to":""}`, http.StatusOK, ""},
		{"/v1/move", `{"paths":["g.txt"],"to":"htdocs"}`, http.StatusOK, ""},
		{"/v1/chmod", `{"paths":["htdocs/g.txt"],"mode":"0600"}`, http.StatusOK, ""},
		{"/v1/chmod", `{"paths":["htdocs/g.txt"],"mode":"4755"}`, http.StatusBadRequest, "invalid"},
		{"/v1/delete", `{"paths":["htdocs/new","htdocs/g.txt"]}`, http.StatusOK, ""},
		{"/v1/delete", `{"paths":[""]}`, http.StatusBadRequest, "home"},
		{"/v1/delete", `{"paths":["../x"]}`, http.StatusBadRequest, "invalid"},
		{"/v1/delete", `{"paths":["link/secret"]}`, http.StatusBadRequest, "symlink"},
		{"/v1/delete", `{"paths":["x"],"extra":1}`, http.StatusBadRequest, "invalid"},
		{"/v1/delete", `not json`, http.StatusBadRequest, "invalid"},
	}

	for _, c := range cases {
		res, body := call(t, h, "POST", c.target, c.body)
		if res.StatusCode != c.status {
			t.Errorf("%s %s: %d %s, want %d", c.target, c.body, res.StatusCode, body, c.status)

			continue
		}

		if c.code != "" {
			var e ErrorBody
			if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error != c.code {
				t.Errorf("%s %s: %s, want code %s", c.target, c.body, body, c.code)
			}
		}
	}

	for _, c := range [][2]string{{"GET", "/v1/nope"}, {"DELETE", "/v1/file"}, {"GET", "/v1/mkdir"}} {
		if res, _ := call(t, h, c[0], c[1], ""); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", c[0], c[1], res.StatusCode)
		}
	}
}

func TestServeExitsWhenIdle(t *testing.T) {
	r, _, _ := newRoot(t)

	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() {
		done <- Serve(context.Background(), ln, &Handler{Root: r}, 200*time.Millisecond, testLog())
	}()

	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer

		return d.DialContext(ctx, "unix", ln.Addr().String())
	}}}

	req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://files/v1/list", nil)

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = res.Body.Close()

	client.CloseIdleConnections()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit when idle")
	}
}

func TestServeStopsWithContext(t *testing.T) {
	r, _, _ := newRoot(t)

	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- Serve(ctx, ln, &Handler{Root: r}, time.Hour, testLog()) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not stop")
	}
}

func TestSystemdListenerNeedsSocket(t *testing.T) {
	t.Setenv("LISTEN_PID", "1")
	t.Setenv("LISTEN_FDS", "1")

	if _, err := SystemdListener(); err == nil {
		t.Fatal("accepted another process's sockets")
	}
}

func testLog() *slog.Logger { return slog.New(slog.DiscardHandler) }
