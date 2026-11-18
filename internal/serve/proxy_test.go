// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPanelDomain(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "",
		"https://Panel.Example.com:8443":    "panel.example.com",
		"https://panel.example.com/":        "panel.example.com",
		"http://panel.example.com:8443":     "",
		"https://localhost:8443":            "",
		"https://192.0.2.10:8443":           "",
		"https://[2001:db8::1]:8443":        "",
		"https://single:8443":               "",
		"https://bad_name.example.com:8443": "",
	} {
		if got := PanelDomain(in); got != want {
			t.Errorf("PanelDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

// Only the nginx socket trusts X-Real-IP; TCP keeps the peer address.
func TestClientIPFromProxy(t *testing.T) {
	var got string

	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = clientIP(r) })

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Real-IP", "203.0.113.7")

	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "127.0.0.1" {
		t.Fatalf("tcp listener trusted the header: %s", got)
	}

	r.RemoteAddr = "@"
	fromProxy(h).ServeHTTP(httptest.NewRecorder(), r)

	if got != "203.0.113.7" {
		t.Fatalf("proxy: %s", got)
	}

	for _, v := range []string{"", "203.0.113.7, 10.0.0.1", "nonsense"} {
		got = ""

		r.Header.Set("X-Real-IP", v)

		rec := httptest.NewRecorder()
		fromProxy(h).ServeHTTP(rec, r)

		if rec.Code != http.StatusBadRequest || got != "" {
			t.Fatalf("%q: code %d, ip %q", v, rec.Code, got)
		}
	}
}
