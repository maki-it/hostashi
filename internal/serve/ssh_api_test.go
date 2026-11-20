// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type stubSSH struct {
	applied map[string][]agentapi.SSHKey
}

func (s *stubSSH) SSHApply(_ context.Context, req agentapi.SSHApplyRequest) (agentapi.SSHApplied, error) {
	s.applied[req.Site] = req.Keys

	return agentapi.SSHApplied{}, nil
}

func testSSHKey(t *testing.T) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
}

func TestSSHKeysOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	ag := &stubSSH{applied: map[string][]agentapi.SSHKey{}}
	e.sites.EnableSSH(ag)

	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	pub := testSSHKey(t)
	body, _ := json.Marshal(map[string]string{"label": "laptop", "public": pub + " kim@laptop"})

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/ssh-keys", cookie: c, body: string(body)})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var key sshKeyJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &key); err != nil || key.ID == 0 || key.Access != "sftp" || key.Public != pub ||
		!strings.HasPrefix(key.Fingerprint, "SHA256:") {
		t.Fatalf("%s %v", rec.Body, err)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/ssh-keys", cookie: c, body: string(body)}); rec.Code != http.StatusConflict ||
		errCode(t, rec) != "ssh_key_exists" {
		t.Errorf("duplicate: %d %s", rec.Code, rec.Body)
	}

	other := testSSHKey(t)

	for b, code := range map[string]string{
		`{"label":"x","public":"command=\"sh\" ` + other + `"}`:  "invalid_argument",
		`{"label":"x","public":"ssh-dss AAAA"}`:                  "invalid_argument",
		`{"label":"x\ny","public":"` + other + `"}`:              "invalid_argument",
		`{"label":"x","access":"root","public":"` + other + `"}`: "invalid_argument",
		`{"label":"x","public":"` + other + `","bogus":1}`:       "bad_request",
	} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/ssh-keys", cookie: c, body: b})
		if rec.Code < 400 || errCode(t, rec) != code {
			t.Errorf("%s: %d %s", b, rec.Code, rec.Body)
		}
	}

	path := "/api/v1/sites/shop/ssh-keys/" + strconv.FormatInt(key.ID, 10)

	rec = e.do(t, reqOpts{method: "PUT", path: path, cookie: c, body: `{"label":"deploy","access":"shell"}`})
	if rec.Code != http.StatusOK || ag.applied["shop"][0].Access != "shell" || ag.applied["shop"][0].Label != "deploy" {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/ssh-keys", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"access":"shell"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	for _, p := range []string{"/api/v1/sites/shop/ssh-keys/999", "/api/v1/sites/shop/ssh-keys/x", "/api/v1/sites/shop/ssh-keys/0"} {
		if rec := e.do(t, reqOpts{method: "GET", path: p, cookie: c}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/nope/ssh-keys", cookie: c}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	// ssh:read does not write.
	read := e.createToken(t, c, `{"name":"r","scopes":["ssh:read"],"expiresInDays":1}`)

	if rec := e.do(t, reqOpts{method: "GET", path: path, header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("read with ssh:read: %d", rec.Code)
	}

	for _, o := range []reqOpts{
		{method: "POST", path: "/api/v1/sites/shop/ssh-keys", body: `{"label":"x","public":"` + other + `"}`},
		{method: "PUT", path: path, body: `{"label":"x","access":"sftp"}`},
		{method: "DELETE", path: path},
	} {
		o.header = bearerHeader(read.Token)
		if rec := e.do(t, o); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with ssh:read: %d", o.method, o.path, rec.Code)
		}
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: path, cookie: c}); rec.Code != http.StatusNoContent || len(ag.applied["shop"]) != 0 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}
