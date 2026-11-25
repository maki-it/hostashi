// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func serve(t *testing.T, tag string, phar []byte, sum string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_, _ = w.Write([]byte(`{"tag_name":"` + tag + `","draft":false,"prerelease":false}`))
		case "/dl/v3.0.1/wp-cli-3.0.1.phar":
			_, _ = w.Write(phar)
		case "/dl/v3.0.1/wp-cli-3.0.1.phar.sha512":
			_, _ = w.Write([]byte(sum + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	oldL, oldR := latestURL, releaseURL
	latestURL, releaseURL = srv.URL+"/latest", srv.URL+"/dl"

	t.Cleanup(func() { latestURL, releaseURL = oldL, oldR })
}

func TestLatest(t *testing.T) {
	phar := []byte("phar")
	s := sha512.Sum512(phar)
	sum := hex.EncodeToString(s[:])

	serve(t, "v3.0.1", phar, sum)

	r, err := Latest(t.Context(), http.DefaultClient)
	if err != nil || r.Version != "3.0.1" || r.SHA512 != sum {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestLatestRefuses(t *testing.T) {
	phar := []byte("phar")
	s := sha512.Sum512([]byte("other"))

	for name, c := range map[string]struct{ tag, sum string }{
		"mismatch":   {"v3.0.1", hex.EncodeToString(s[:])},
		"not hex":    {"v3.0.1", strings.Repeat("z", 128)},
		"short":      {"v3.0.1", "abcd"},
		"odd tag":    {"v3.0.1-rc1", ""},
		"tag syntax": {"v3.0.1\"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, c.tag, phar, c.sum)

			if r, err := Latest(t.Context(), http.DefaultClient); err == nil {
				t.Fatalf("accepted %+v", r)
			}
		})
	}
}

func TestRewrite(t *testing.T) {
	src, err := os.ReadFile("../../" + goFile)
	if err != nil {
		t.Fatal(err)
	}

	r := agentapi.WPCLIRelease{Version: "3.0.1", SHA512: strings.Repeat("a", 128)}

	out, err := Rewrite(src, r, "2027-01-02")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`Version: "3.0.1",`, `SHA512:  "` + r.SHA512 + `",`, "Checked on 2027-01-02\n// against wp-cli-3.0.1.phar.sha512"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	if _, err := Rewrite([]byte("package agentapi\n"), r, "2027-01-02"); err == nil {
		t.Fatal("rewrote a file without the pin")
	}
}
