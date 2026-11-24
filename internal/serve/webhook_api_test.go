// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func hmacHex(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))

	return hex.EncodeToString(m.Sum(nil))
}

func TestWebhookProof(t *testing.T) {
	secret, body := []byte("s3cr3t"), []byte(`{"ref":"refs/heads/main"}`)
	sig := hmacHex(string(secret), string(body))

	for name, c := range map[string]struct {
		h    map[string]string
		want bool
	}{
		"github":         {map[string]string{"X-Hub-Signature-256": "sha256=" + sig}, true},
		"github no algo": {map[string]string{"X-Hub-Signature-256": sig}, false},
		"github wrong":   {map[string]string{"X-Hub-Signature-256": "sha256=" + strings.Repeat("0", 64)}, false},
		"gitea":          {map[string]string{"X-Gitea-Signature": sig}, true},
		"forgejo":        {map[string]string{"X-Forgejo-Signature": sig}, true},
		"gitlab":         {map[string]string{"X-Gitlab-Token": "s3cr3t"}, true},
		"gitlab wrong":   {map[string]string{"X-Gitlab-Token": "s3cr3"}, false},
		"bearer":         {map[string]string{"Authorization": "Bearer s3cr3t"}, true},
		"basic":          {map[string]string{"Authorization": "Basic s3cr3t"}, false},
		// The first proof present counts, a valid one after it does not.
		"bad first": {map[string]string{"X-Hub-Signature-256": "sha256=00", "Authorization": "Bearer s3cr3t"}, false},
		"none":      {map[string]string{}, false},
	} {
		h := http.Header{}
		for k, v := range c.h {
			h.Set(k, v)
		}

		if got := webhookProof(h, body, secret); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestWebhookWanted(t *testing.T) {
	for name, c := range map[string]struct {
		h      map[string]string
		body   string
		want   bool
		result string
	}{
		"github push":   {map[string]string{"X-GitHub-Event": "push"}, `{"ref":"refs/heads/main","commits":[]}`, true, ""},
		"github ping":   {map[string]string{"X-GitHub-Event": "ping"}, `{"zen":"x"}`, false, "pong"},
		"github issue":  {map[string]string{"X-GitHub-Event": "issues"}, `{"ref":"refs/heads/main"}`, false, "ignored"},
		"other branch":  {map[string]string{"X-Gitea-Event": "push"}, `{"ref":"refs/heads/dev"}`, false, "ignored"},
		"tag":           {map[string]string{"X-Gitlab-Event": "Push Hook"}, `{"ref":"refs/tags/main"}`, false, "ignored"},
		"gitlab tag ev": {map[string]string{"X-Gitlab-Event": "Tag Push Hook"}, `{"ref":"refs/heads/main"}`, false, "ignored"},
		"forgejo":       {map[string]string{"X-Forgejo-Event": "push"}, `{"ref":"refs/heads/main"}`, true, ""},
		"generic empty": {nil, "", true, ""},
		"no ref":        {nil, `{"after":"abc"}`, false, "ignored"},
		"not json":      {nil, `ref=refs/heads/main`, false, "ignored"},
	} {
		h := http.Header{}
		for k, v := range c.h {
			h.Set(k, v)
		}

		if want, result := webhookWanted(h, []byte(c.body), "main"); want != c.want || result != c.result {
			t.Errorf("%s: %v %q", name, want, result)
		}
	}
}

func TestWebhookOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &deployAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/git/webhook", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("webhook without git: %d %s", rec.Code, rec.Body)
	}

	body := `{"url":"https://github.com/acme/blog.git","auth":"none","branch":"main","deployDir":"htdocs","keep":5,"buildTimeoutMinutes":15}`
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/git", cookie: c, body: body}); rec.Code != http.StatusOK {
		t.Fatalf("set git: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/git/webhook", cookie: c})

	var hook webhookJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &hook); rec.Code != http.StatusOK || err != nil || len(hook.ID) != 32 || hook.Secret == "" ||
		hook.Path != "/api/v1/hooks/deploy/"+hook.ID {
		t.Fatalf("webhook on: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/git", cookie: c}); !strings.Contains(rec.Body.String(), `"webhookPath":"`+hook.Path+`"`) ||
		strings.Contains(rec.Body.String(), hook.Secret) {
		t.Fatalf("setting: %s", rec.Body)
	}

	push := `{"ref":"refs/heads/main","repository":{"name":"blog"}}`
	call := func(id, sig, payload string) (int, string) {
		t.Helper()

		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/hooks/deploy/" + id, body: payload, header: map[string]string{
			"Content-Type": "application/json", "X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sig,
		}})

		return rec.Code, rec.Body.String()
	}

	// Unknown id and wrong signature answer alike.
	if code, out := call(strings.Repeat("0", 32), hmacHex(hook.Secret, push), push); code != http.StatusUnauthorized {
		t.Fatalf("unknown id: %d %s", code, out)
	}

	if code, out := call(hook.ID, hmacHex("nope", push), push); code != http.StatusUnauthorized {
		t.Fatalf("wrong signature: %d %s", code, out)
	}

	if code, out := call(hook.ID, hmacHex(hook.Secret, push), push); code != http.StatusAccepted || !strings.Contains(out, `"started"`) {
		t.Fatalf("push: %d %s", code, out)
	}

	// While it runs, a push is noted once, not refused.
	if code, out := call(hook.ID, hmacHex(hook.Secret, push), push); code != http.StatusAccepted || !strings.Contains(out, `"queued"`) {
		t.Fatalf("push while running: %d %s", code, out)
	}

	dev := `{"ref":"refs/heads/dev"}`
	if code, out := call(hook.ID, hmacHex(hook.Secret, dev), dev); code != http.StatusAccepted || !strings.Contains(out, `"ignored"`) {
		t.Fatalf("other branch: %d %s", code, out)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/deploys", cookie: c}); !strings.Contains(rec.Body.String(), `"source":"webhook"`) {
		t.Fatalf("history: %s", rec.Body)
	}

	// A new secret keeps the address; the old secret stops working.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/git/webhook", cookie: c})

	var renewed webhookJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &renewed); err != nil || renewed.ID != hook.ID || renewed.Secret == hook.Secret {
		t.Fatalf("renew: %s", rec.Body)
	}

	if code, _ := call(hook.ID, hmacHex(hook.Secret, dev), dev); code != http.StatusUnauthorized {
		t.Fatalf("old secret: %d", code)
	}

	// Ten calls a minute per webhook (five so far), then 429.
	for range 5 {
		if code, out := call(hook.ID, hmacHex(renewed.Secret, dev), dev); code != http.StatusAccepted {
			t.Fatalf("within the limit: %d %s", code, out)
		}
	}

	if code, out := call(hook.ID, hmacHex(renewed.Secret, dev), dev); code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d %s", code, out)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/git/webhook", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("webhook off: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit?action=site.git.webhook", cookie: c}); rec.Code != http.StatusOK ||
		strings.Contains(rec.Body.String(), renewed.Secret) || !strings.Contains(rec.Body.String(), "off") {
		t.Fatalf("audit: %s", rec.Body)
	}
}
