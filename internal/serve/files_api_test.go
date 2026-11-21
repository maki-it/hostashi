// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/files"
)

// stubFiles stands in for the agent's files.open: it serves a real helper
// on a socket of its own, for every site, from one home.
type stubFiles struct {
	t     *testing.T
	err   error
	mu    sync.Mutex
	opens int
	home  string
	sock  string
}

func (s *stubFiles) open(ctx context.Context, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.opens++

	if s.err != nil {
		return "", s.err
	}

	if s.sock != "" {
		return s.sock, nil
	}

	dir, err := os.MkdirTemp("", "hf")
	if err != nil {
		return "", err
	}

	s.t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s.home = filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(s.home, "htdocs"), 0o755); err != nil {
		return "", err
	}

	if err := os.WriteFile(filepath.Join(s.home, "htdocs", "index.php"), []byte("<?php echo 1;"), 0o644); err != nil {
		return "", err
	}

	root, err := files.Open(s.home)
	if err != nil {
		return "", err
	}

	s.sock = filepath.Join(dir, "s")

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "unix", s.sock)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})

	go func() {
		_ = files.Serve(ctx, ln, &files.Handler{Root: root}, time.Hour, slog.New(slog.DiscardHandler))

		close(done)
	}()

	s.t.Cleanup(func() { cancel(); <-done; _ = root.Close() })

	return s.sock, nil
}

func TestFilesOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/files", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/files?path=htdocs", cookie: c})

	var l files.Listing
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &l) != nil || len(l.Entries) != 1 || l.Entries[0].Name != "index.php" {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("listing may be cached")
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/files/content?path=htdocs/index.php", cookie: c})
	if rec.Code != http.StatusOK || rec.Body.String() != "<?php echo 1;" || rec.Header().Get("X-Hostashi-Version") == "" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") || rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download: %d %q %v", rec.Code, rec.Body, rec.Header())
	}

	v := rec.Header().Get("X-Hostashi-Version")

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/files/content?path=htdocs/index.php&version=" + v, cookie: c, body: "<?php echo 2;"})
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}

	if b, _ := os.ReadFile(filepath.Join(e.files.home, "htdocs", "index.php")); string(b) != "<?php echo 2;" {
		t.Fatalf("saved %q", b)
	}

	// The old version is stale now.
	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/files/content?path=htdocs/index.php&version=" + v, cookie: c, body: "x"})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "files_changed" {
		t.Fatalf("stale save: %d %s", rec.Code, rec.Body)
	}

	for _, s := range []struct {
		call, body string
		status     int
		code       string
	}{
		{"mkdir", `{"path":"htdocs/img"}`, http.StatusOK, ""},
		{"mkdir", `{"path":"htdocs/img"}`, http.StatusConflict, "files_exists"},
		{"touch", `{"path":"htdocs/img/a.txt"}`, http.StatusOK, ""},
		{"rename", `{"from":"htdocs/img/a.txt","to":"htdocs/img/b.txt"}`, http.StatusOK, ""},
		{"copy", `{"paths":["htdocs/img"],"to":""}`, http.StatusOK, ""},
		{"move", `{"paths":["img"],"to":"htdocs/img"}`, http.StatusOK, ""},
		{"chmod", `{"paths":["htdocs/img/b.txt"],"mode":"0600"}`, http.StatusOK, ""},
		{"chmod", `{"paths":["htdocs/img/b.txt"],"mode":"4755"}`, http.StatusUnprocessableEntity, "invalid_argument"},
		{"delete", `{"paths":["htdocs/img"]}`, http.StatusOK, ""},
		{"delete", `{"paths":[]}`, http.StatusUnprocessableEntity, "invalid_argument"},
		{"delete", `{"paths":["/"]}`, http.StatusUnprocessableEntity, "invalid_argument"},
		{"delete", `{"paths":["../etc"]}`, http.StatusUnprocessableEntity, "invalid_argument"},
		{"delete", `{"paths":["nope"]}`, http.StatusNotFound, "files_not_found"},
		{"rename", `{"from":"","to":"x"}`, http.StatusUnprocessableEntity, "invalid_argument"},
		{"delete", `{"paths":["x"],"more":1}`, http.StatusBadRequest, "bad_request"},
		{"exec", `{}`, http.StatusNotFound, "not_found"},
	} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/files/" + s.call, cookie: c, body: s.body})
		if rec.Code != s.status || (s.code != "" && errCode(t, rec) != s.code) {
			t.Errorf("%s %s: %d %s", s.call, s.body, rec.Code, rec.Body)
		}
	}

	if _, err := os.Stat(filepath.Join(e.files.home, "htdocs", "img")); !errors.Is(err, os.ErrNotExist) {
		t.Error("folder not deleted")
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/files?path=../x", cookie: c}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad path: %d", rec.Code)
	}

	// The agent was asked once; then the panel connects directly.
	if e.files.opens != 1 {
		t.Errorf("agent asked %d times, want 1", e.files.opens)
	}

	// Writes and downloads are in the audit log, reads of listings are not.
	for action, want := range map[string]int{
		"files.download": 1, "files.save": 1, "files.mkdir": 1, "files.touch": 1, "files.rename": 1,
		"files.copy": 1, "files.move": 1, "files.chmod": 1, "files.delete": 1,
	} {
		var n int
		if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = ? AND target = 'shop'`, action, &n); err != nil || n != want {
			t.Errorf("%s: %d audit entries, want %d (%v)", action, n, want, err)
		}
	}

	// A token needs the scope.
	ro := e.createToken(t, c, `{"name":"r","scopes":["files:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/files", header: bearerHeader(ro.Token)}); rec.Code != http.StatusOK {
		t.Errorf("files:read list: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/files/mkdir", header: bearerHeader(ro.Token), body: `{"path":"x"}`}); rec.Code != http.StatusForbidden {
		t.Errorf("files:read mkdir: %d", rec.Code)
	}
}

func TestFilesAgentFailures(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	e.files.err = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "no"}
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/files", cookie: c}); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "files_unavailable" {
		t.Errorf("no file manager: %d %s", rec.Code, rec.Body)
	}

	e.files.err = errors.New("agent gone")
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/files", cookie: c}); rec.Code != http.StatusBadGateway {
		t.Errorf("agent gone: %d %s", rec.Code, rec.Body)
	}
}
