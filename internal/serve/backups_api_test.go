// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/backup"
)

type nopUnits struct{ runs int }

func (u *nopUnits) StartRun(context.Context) error     { u.runs++; return nil }
func (u *nopUnits) Reload(context.Context) error       { return nil }
func (u *nopUnits) EnableTimer(context.Context) error  { return nil }
func (u *nopUnits) DisableTimer(context.Context) error { return nil }

// localBackups is Backups with the real agent side in a temp directory and
// a fixed host key for the scan.
type localBackups struct {
	a       *backup.Agent
	hostKey ssh.PublicKey
	scanErr error
	copies  int
}

func (l *localBackups) Get(context.Context) (agentapi.BackupInfo, error) { return l.a.Get() }
func (l *localBackups) Set(ctx context.Context, s agentapi.BackupSettings) (agentapi.BackupInfo, error) {
	return l.a.Set(ctx, s)
}

func (l *localBackups) SetHostKey(ctx context.Context, k string) (agentapi.BackupInfo, error) {
	return l.a.SetHostKey(ctx, k)
}

func (l *localBackups) Run(ctx context.Context) error {
	l.copies++
	return l.a.StartRun(ctx)
}

func (l *localBackups) Status(context.Context) (agentapi.BackupStatus, error) { return l.a.Status() }

func (l *localBackups) Snapshots(_ context.Context, site string) ([]agentapi.BackupSnapshot, error) {
	return l.a.Snapshots(site)
}

func (l *localBackups) ScanHostKey(context.Context, agentapi.BackupTarget) (ssh.PublicKey, error) {
	return l.hostKey, l.scanErr
}

func newBackupAPI(t *testing.T) (*apiEnv, *localBackups, *nopUnits) {
	t.Helper()

	e := newAPIWithAgent(t, nil)
	dir := t.TempDir()
	status := filepath.Join(dir, "status")

	if err := os.Mkdir(status, 0o700); err != nil {
		t.Fatal(err)
	}

	units := &nopUnits{}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	hk, _ := ssh.NewPublicKey(pub)
	lb := &localBackups{
		a:       &backup.Agent{ConfigDir: filepath.Join(dir, "config"), StatusDir: status, DropIn: filepath.Join(dir, "window.conf"), Units: units},
		hostKey: hk,
	}

	ok := func(context.Context) error { return nil }
	e.h = Handler(Config{}, Deps{PingAgent: ok, PingDB: ok, Auth: e.svc, Backups: lb})

	return e, lb, units
}

func TestBackupsOverHTTP(t *testing.T) {
	e, lb, units := newBackupAPI(t)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/backups", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ready":false`) || !strings.Contains(rec.Body.String(), `"daily":7`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/backups/hostkey/scan", cookie: c}); rec.Code != http.StatusConflict {
		t.Errorf("scan without target: %d %s", rec.Code, rec.Body)
	}

	settings := `{"version":1,"target":{"user":"u123456","host":"u123456.your-storagebox.de","port":23,"path":"hostashi"},` +
		`"retention":{"daily":7,"weekly":4,"monthly":6},"windowStart":1,"windowEnd":5,"readParts":12}`

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/backups/settings", cookie: c, body: settings})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"publicKey":"ssh-ed25519 `) {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	// Nothing returned by the API contains the repository password.
	pw, _ := os.ReadFile(filepath.Join(lb.a.ConfigDir, backup.PasswordFile))
	if strings.Contains(rec.Body.String(), strings.TrimSpace(string(pw))) || strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatal("secret in the response")
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/backups/run", cookie: c}); rec.Code != http.StatusConflict {
		t.Errorf("run before host key: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/backups/hostkey/scan", cookie: c})

	var scan struct {
		Key         string `json:"key"`
		Fingerprint string `json:"fingerprint"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &scan) != nil || scan.Fingerprint != ssh.FingerprintSHA256(lb.hostKey) {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/backups/hostkey", cookie: c, body: `{"key":"` + scan.Key + `"}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ready":true`) {
		t.Fatalf("confirm host key: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/backups/run", cookie: c}); rec.Code != http.StatusAccepted || units.runs != 1 || lb.copies != 2 {
		t.Errorf("run: %d %s, runs %d", rec.Code, rec.Body, units.runs)
	}

	for _, path := range []string{"/api/v1/sites/blog/backups", "/api/v1/backups/panel"} {
		if rec := e.do(t, reqOpts{method: "GET", path: path, cookie: c}); rec.Code != http.StatusOK || rec.Body.String() != "{\"snapshots\":[]}\n" {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}

	cases := []struct {
		method, path, body string
		code               int
	}{
		{"PUT", "/api/v1/backups/settings", `{"version":1,"retention":{"daily":0},"windowStart":1,"windowEnd":5}`, http.StatusUnprocessableEntity},
		{"PUT", "/api/v1/backups/settings", `{"version":1,"target":{"user":"u1","host":"127.0.0.1","port":23,"path":"x"},"retention":{"daily":7},"windowStart":1,"windowEnd":5}`, http.StatusUnprocessableEntity},
		{"PUT", "/api/v1/backups/settings", `{"version":1,"repository":"s3:x"}`, http.StatusBadRequest},
		{"PUT", "/api/v1/backups/hostkey", `{"key":"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQC"}`, http.StatusUnprocessableEntity},
		{"GET", "/api/v1/sites/..%2Fetc/backups", ``, http.StatusNotFound},
	}
	for _, tc := range cases {
		if rec := e.do(t, reqOpts{method: tc.method, path: tc.path, cookie: c, body: tc.body}); rec.Code != tc.code {
			t.Errorf("%s %s %s: %d, want %d: %s", tc.method, tc.path, tc.body, rec.Code, tc.code, rec.Body)
		}
	}

	lb.scanErr = errors.New("connection refused")

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/backups/hostkey/scan", cookie: c}); rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "refused") {
		t.Errorf("unreachable target: %d %s", rec.Code, rec.Body)
	}
}

func TestBackupScopes(t *testing.T) {
	e, _, _ := newBackupAPI(t)
	c := e.adminCookie(t)

	read := e.createToken(t, c, `{"name":"r","scopes":["backups:read"],"expiresInDays":1}`)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/backups", header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("read with backups:read: %d %s", rec.Code, rec.Body)
	}

	for _, o := range []reqOpts{
		{method: "PUT", path: "/api/v1/backups/settings", body: `{}`},
		{method: "POST", path: "/api/v1/backups/run"},
		{method: "PUT", path: "/api/v1/backups/hostkey", body: `{"key":"x"}`},
	} {
		o.header = bearerHeader(read.Token)
		if rec := e.do(t, o); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with backups:read: %d", o.method, o.path, rec.Code)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/backups"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("without login: %d", rec.Code)
	}
}
