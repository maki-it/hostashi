package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthRejectsMissingAndWrongToken(t *testing.T) {
	h := auth("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	for _, tc := range []struct {
		hdr  string
		want int
	}{{"", 401}, {"Bearer nope", 401}, {"Bearer secret", 200}} {
		r := httptest.NewRequest("GET", "/api/sites", nil)
		if tc.hdr != "" {
			r.Header.Set("Authorization", tc.hdr)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("header %q: got %d want %d", tc.hdr, w.Code, tc.want)
		}
	}
	// A token in the query string is only accepted for WebSocket handshakes.
	r := httptest.NewRequest("GET", "/api/sites?token=secret", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Errorf("query token on plain request: got %d want 401", w.Code)
	}
}

func TestCreateSiteValidation(t *testing.T) {
	s := newSites()
	cases := []struct {
		body string
		want int
	}{
		{`{"domain":"ok.example.org","user":"okuser","kind":"php"}`, 201},
		{`{"domain":"ok.example.org","user":"other","kind":"php"}`, 422}, // duplicate domain
		{`{"domain":"x.example.org","user":"okuser","kind":"php"}`, 422}, // duplicate user
		{`{"domain":"a b.example.org","user":"abc","kind":"php"}`, 422},
		{`{"domain":"../etc","user":"abc","kind":"php"}`, 422},
		{`{"domain":"x.example.org","user":"Root","kind":"php"}`, 422},
		{`{"domain":"x.example.org","user":"abc;id","kind":"php"}`, 422},
		{`{"domain":"x.example.org","user":"abc","kind":"cgi"}`, 422},
		{`{"domain":"x.example.org","user":"abc","kind":"php","extra":1}`, 400}, // unknown field
		{`not json`, 400},
		{`{"domain":"` + strings.Repeat("a", 5000) + `.com","user":"abc","kind":"php"}`, 400}, // body cap
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		s.create(w, httptest.NewRequest("POST", "/api/sites", strings.NewReader(tc.body)))
		if w.Code != tc.want {
			t.Errorf("%.60s: got %d want %d", tc.body, w.Code, tc.want)
		}
	}
}

func TestSPAFallback(t *testing.T) {
	h := spa(mapFS{"index.html": "INDEX", "assets/a.js": "JS"}.fs())
	for path, want := range map[string]int{"/": 200, "/sites/12": 200, "/assets/a.js": 200, "/assets/missing.js": 404, "/api/nope": 404} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Errorf("%s: got %d want %d", path, w.Code, want)
		}
	}
}

func TestTerminalRejectsCrossOriginAndMissingToken(t *testing.T) {
	srv := httptest.NewServer(auth("secret", http.HandlerFunc(terminal)))
	defer srv.Close()
	dial := func(origin, token string) int {
		req, _ := http.NewRequest("GET", srv.URL+"/?token="+token, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := dial("", "wrong"); got != 401 {
		t.Errorf("wrong token: got %d want 401", got)
	}
	if got := dial("http://evil.example", "secret"); got != 403 {
		t.Errorf("cross-origin with valid token: got %d want 403", got)
	}
}
