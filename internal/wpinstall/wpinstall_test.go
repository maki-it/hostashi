// SPDX-License-Identifier: AGPL-3.0-or-later

package wpinstall

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestInstall(t *testing.T) {
	body := []byte("<?php // phar")
	sum := sha512.Sum512(body)
	rel := agentapi.WPCLIRelease{Version: "9.9.9", SHA512: hex.EncodeToString(sum[:])}

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++

		if r.URL.Path != "/v9.9.9/wp-cli-9.9.9.phar" {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()

	var out bytes.Buffer

	in := &Installer{Root: root, URL: srv.URL, Out: &out, Release: rel}
	if err := in.Install(t.Context()); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(Path(root, rel))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("phar: %q %v", got, err)
	}

	if fi, _ := os.Stat(Path(root, rel)); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}

	if !strings.Contains(out.String(), DoneMarker+" 9.9.9") {
		t.Fatalf("log: %s", out.String())
	}

	// A second run finds it and downloads nothing.
	if err := in.Install(t.Context()); err != nil || hits != 1 {
		t.Fatalf("second run: %v, %d downloads", err, hits)
	}

	// No temporary file stays behind.
	if m, _ := filepath.Glob(filepath.Join(root, Dir, ".new-*")); len(m) != 0 {
		t.Fatalf("left over: %v", m)
	}
}

func TestInstallRefusesWrongChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<?php system($_GET['x']);"))
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	rel := agentapi.WPCLIRelease{Version: "9.9.9", SHA512: strings.Repeat("0", 128)}

	err := (&Installer{Root: root, URL: srv.URL, Release: rel}).Install(t.Context())
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err %v", err)
	}

	if _, err := os.Stat(Path(root, rel)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("phar written: %v", err)
	}

	if m, _ := filepath.Glob(filepath.Join(root, Dir, ".new-*")); len(m) != 0 {
		t.Fatalf("left over: %v", m)
	}

	// A phar that was changed in place is replaced.
	good := []byte("good")
	sum := sha512.Sum512(good)
	rel.SHA512 = hex.EncodeToString(sum[:])

	if err := os.MkdirAll(filepath.Dir(Path(root, rel)), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(Path(root, rel), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	if ok, err := Installed(root, rel); ok || err != nil {
		t.Fatalf("changed phar counts as installed: %v %v", ok, err)
	}
}

func TestInstallHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	err := (&Installer{Root: t.TempDir(), URL: srv.URL, Release: agentapi.WPCLIRelease{Version: "1.0.0", SHA512: "x"}}).Install(t.Context())
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err %v", err)
	}
}

func TestPinnedRelease(t *testing.T) {
	if agentapi.WPCLI.Version == "" || len(agentapi.WPCLI.SHA512) != 128 {
		t.Fatalf("%+v", agentapi.WPCLI)
	}

	if _, err := hex.DecodeString(agentapi.WPCLI.SHA512); err != nil {
		t.Fatal(err)
	}
}

func TestInstallFallback(t *testing.T) {
	body := []byte("<?php // phar")
	sum := sha512.Sum512(body)
	rel := agentapi.WPCLIRelease{Version: "9.9.9", SHA512: hex.EncodeToString(sum[:])}

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v9.9.9/wp-cli-9.9.9.phar" {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write(body)
	}))
	t.Cleanup(good.Close)

	// A mirror that is down, and one that serves a wrong file: both are
	// passed over, and nothing of the wrong file is left in the phar.
	down := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(down.Close)

	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<?php system($_GET['x']); // a longer file than the real one"))
	}))
	t.Cleanup(wrong.Close)

	root := t.TempDir()

	var out bytes.Buffer

	in := &Installer{Root: root, URL: down.URL, Fallback: []string{wrong.URL, good.URL}, Out: &out, Release: rel}
	if err := in.Install(t.Context()); err != nil {
		t.Fatalf("Install: %v\n%s", err, &out)
	}

	got, err := os.ReadFile(Path(root, rel))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("phar: %q %v", got, err)
	}

	if n := strings.Count(out.String(), "trying the next source"); n != 2 {
		t.Fatalf("log names %d fallbacks:\n%s", n, &out)
	}

	// Every source fails: the last error.
	in = &Installer{Root: t.TempDir(), URL: down.URL, Fallback: []string{wrong.URL}, Release: rel}
	if err := in.Install(t.Context()); !errors.Is(err, ErrChecksum) {
		t.Fatalf("Install = %v, want ErrChecksum", err)
	}
}

func TestSources(t *testing.T) {
	if MirrorURL != "https://get.hostashi.com/wp-cli" || ReleaseURL != "https://github.com/wp-cli/wp-cli/releases/download" {
		t.Fatalf("sources %q, %q", MirrorURL, ReleaseURL)
	}
}
