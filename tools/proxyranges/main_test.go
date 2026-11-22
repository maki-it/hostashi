// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func serve(t *testing.T, v4, v6 []string, api4, api6 []string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/ips-v4", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Join(v4, "\n") + "\n")) })
	mux.HandleFunc("/ips-v6", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Join(v6, "\n"))) })
	mux.HandleFunc("/api", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"ipv4_cidrs": api4, "ipv6_cidrs": api6}})
	})

	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)

	return s
}

func split() (v4, v6 []string) {
	for _, s := range agentapi.CloudflareRanges {
		if strings.Contains(s, ":") {
			v6 = append(v6, s)
		} else {
			v4 = append(v4, s)
		}
	}

	return v4, v6
}

func fetch(t *testing.T, s *httptest.Server) ([]string, error) {
	t.Helper()

	return Fetch(context.Background(), s.Client(), s.URL+"/ips-v4", s.URL+"/ips-v6", s.URL+"/api")
}

func TestFetch(t *testing.T) {
	v4, v6 := split()

	got, err := fetch(t, serve(t, v4, v6, v4, v6))
	if err != nil || !slices.Equal(got, agentapi.CloudflareRanges) {
		t.Fatalf("%v %v", got, err)
	}

	// The API in another order is the same set.
	rev := slices.Clone(v4)
	slices.Reverse(rev)

	if _, err := fetch(t, serve(t, v4, v6, rev, v6)); err != nil {
		t.Errorf("order: %v", err)
	}

	for name, c := range map[string][4][]string{
		"disagree":   {append(slices.Clone(v4), "198.51.100.0/24"), v6, v4, v6},
		"truncated":  {v4[:2], v6, v4[:2], v6},
		"too wide":   {append(slices.Clone(v4), "0.0.0.0/0"), v6, append(slices.Clone(v4), "0.0.0.0/0"), v6},
		"loopback":   {append(slices.Clone(v4), "127.0.0.0/8"), v6, append(slices.Clone(v4), "127.0.0.0/8"), v6},
		"not masked": {append(slices.Clone(v4), "198.51.100.1/24"), v6, append(slices.Clone(v4), "198.51.100.1/24"), v6},
		"wrong list": {append(slices.Clone(v4), "2001:db8::/32"), v6, append(slices.Clone(v4), "2001:db8::/32"), v6},
		"garbage":    {append(slices.Clone(v4), "<html>"), v6, append(slices.Clone(v4), "<html>"), v6},
		"duplicate":  {append(slices.Clone(v4), v4[0]), v6, append(slices.Clone(v4), v4[0]), v6},
		"injection":  {append(slices.Clone(v4), "10.0.0.0/8;"), v6, append(slices.Clone(v4), "10.0.0.0/8;"), v6},
	} {
		if _, err := fetch(t, serve(t, c[0], c[1], c[2], c[3])); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusServiceUnavailable) })
	down := httptest.NewServer(mux)
	t.Cleanup(down.Close)

	if _, err := fetch(t, down); err == nil {
		t.Error("an error answer accepted")
	}
}

// The checked-in file is exactly what Render writes, so a run without a
// change keeps it, and the date is a real day.
func TestRenderMatchesFile(t *testing.T) {
	have, err := os.ReadFile(filepath.Join("..", "..", goFile))
	if err != nil {
		t.Fatal(err)
	}

	day, err := time.Parse(time.DateOnly, agentapi.CloudflareRangesDate)
	if err != nil {
		t.Fatal(err)
	}

	if got := Render(agentapi.CloudflareRanges, day); string(got) != string(have) {
		t.Errorf("%s differs from Render:\n%s", goFile, got)
	}
}

func TestDiffAndSummary(t *testing.T) {
	added, removed := Diff([]string{"a", "b"}, []string{"b", "c"})
	if !slices.Equal(added, []string{"c"}) || !slices.Equal(removed, []string{"a"}) {
		t.Fatalf("%v %v", added, removed)
	}

	if s := Summary(added, removed); !strings.Contains(s, "added `c`") || !strings.Contains(s, "removed `a`") {
		t.Error(s)
	}

	if s := Summary(nil, nil); !strings.Contains(s, "unchanged") {
		t.Error(s)
	}
}
