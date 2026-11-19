// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/cpsource"
)

// stubSource runs the real agent side in a temporary directory and maps its
// errors the way the agent does; only the host key scan is faked.
type stubSource struct {
	s       *cpsource.Source
	scanned ssh.PublicKey
}

func newStubSource(t *testing.T) *stubSource {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return &stubSource{s: &cpsource.Source{Dir: filepath.Join(t.TempDir(), "cloudpanel")}, scanned: k}
}

func agentErr(err error) error {
	if errors.Is(err, cpsource.ErrNotSet) || errors.Is(err, cpsource.ErrNotReady) {
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "set the old server's address first"}
	}

	return err
}

func (s *stubSource) Get(context.Context) (agentapi.CloudPanelSourceInfo, error) {
	i, err := s.s.Get()

	return i, agentErr(err)
}

func (s *stubSource) Set(_ context.Context, src agentapi.CloudPanelSource) (agentapi.CloudPanelSourceInfo, error) {
	i, err := s.s.Set(src)

	return i, agentErr(err)
}

func (s *stubSource) SetHostKey(_ context.Context, key string) (agentapi.CloudPanelSourceInfo, error) {
	i, err := s.s.SetHostKey(key)

	return i, agentErr(err)
}

func (s *stubSource) Delete(context.Context) (agentapi.CloudPanelSourceInfo, error) {
	i, err := s.s.Delete()

	return i, agentErr(err)
}

func (s *stubSource) ScanHostKey(context.Context, agentapi.CloudPanelSource) (ssh.PublicKey, error) {
	return s.scanned, nil
}

func TestCloudPanelSourceOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	const base = "/api/v1/migrations/cloudpanel/source"

	rec := e.do(t, reqOpts{method: "GET", path: base, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":null`) {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: base + "/hostkey/scan", cookie: c}); rec.Code != http.StatusConflict || errCode(t, rec) != "no_source" {
		t.Fatalf("scan without source: %d %s", rec.Code, rec.Body)
	}

	for _, body := range []string{`{"host":"bad host","port":22}`, `{"host":"203.0.113.7","port":0}`, `{"host":"203.0.113.7"}`} {
		if rec := e.do(t, reqOpts{method: "PUT", path: base, cookie: c, body: body}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "PUT", path: base, cookie: c, body: `{"host":"203.0.113.7","port":22}`})

	var info agentapi.CloudPanelSourceInfo
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &info) != nil || info.Ready || !strings.HasPrefix(info.AuthorizedKeysLine, "restrict,") {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if strings.Contains(rec.Body.String(), "PRIVATE") {
		t.Fatalf("private key in response: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: base + "/hostkey/scan", cookie: c})

	var scan struct {
		Key         string `json:"key"`
		Fingerprint string `json:"fingerprint"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &scan) != nil || !strings.HasPrefix(scan.Fingerprint, "SHA256:") {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: base + "/hostkey", cookie: c, body: `{"key":"ssh-dss AAAA"}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad host key: %d %s", rec.Code, rec.Body)
	}

	body, _ := json.Marshal(map[string]string{"key": scan.Key})

	rec = e.do(t, reqOpts{method: "PUT", path: base + "/hostkey", cookie: c, body: string(body)})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ready":true`) || !strings.Contains(rec.Body.String(), scan.Fingerprint) {
		t.Fatalf("pin: %d %s", rec.Code, rec.Body)
	}

	read := bearerHeader(e.createToken(t, c, `{"name":"r","scopes":["migrations:read"]}`).Token)
	if rec := e.do(t, reqOpts{method: "DELETE", path: base, header: read}); rec.Code != http.StatusForbidden {
		t.Fatalf("delete with read token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: base, cookie: c}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":null`) {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action LIKE ?`, "cloudpanel.source.%", &n); err != nil || n != 3 {
		t.Fatalf("audit entries: %d %v", n, err)
	}
}
