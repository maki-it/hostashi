// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/store"
)

func TestUserRecover(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "h.db")

	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	u, err := st.CreateFirstUser(ctx, "kim", "$argon2id$placeholder")
	if err != nil {
		t.Fatal(err)
	}

	if err := st.RawExec(ctx, `UPDATE users SET totp_enabled = 1, totp_secret = x'01', disabled = 1 WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}

	_ = st.Close()

	var out, errOut bytes.Buffer

	// Only root, an existing database and a known account.
	for _, tc := range []struct {
		args []string
		euid int
		code int
		msg  string
	}{
		{[]string{"user", "recover", "-db", db, "kim"}, 1000, 1, "must run as root"},
		{[]string{"user", "recover", "-db", db + ".none", "kim"}, 0, 1, "database"},
		{[]string{"user", "recover", "-db", db, "nobody"}, 0, 1, `no account "nobody"`},
		{[]string{"user", "recover", "-db", db}, 0, 2, "usage"},
		{[]string{"user", "list"}, 0, 2, "usage"},
	} {
		out.Reset()
		errOut.Reset()

		if code := run(ctx, tc.args, tc.euid, &out, &errOut); code != tc.code || !strings.Contains(strings.ToLower(errOut.String()), tc.msg) {
			t.Errorf("%v: exit %d, stderr %q", tc.args, code, &errOut)
		}
	}

	if _, err := os.Stat(db + ".none"); !os.IsNotExist(err) {
		t.Fatalf("a missing database was created: %v", err)
	}

	out.Reset()

	if code := run(ctx, []string{"user", "recover", "-db", db, "-public-url", "https://panel.example:8443/", "kim"}, 0, &out, &errOut); code != 0 {
		t.Fatalf("recover: %d %s", code, &errOut)
	}

	m := regexp.MustCompile(`https://panel\.example:8443/#/invite/([A-Za-z0-9_-]{43})\n`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no link in %q", &out)
	}

	st, err = store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = st.Close() }()

	got, err := st.UserByID(ctx, u.ID)
	if err != nil || got.PasswordHash != "" || got.TOTPEnabled || got.Disabled {
		t.Fatalf("after recover: %+v %v", got, err)
	}

	// The link works.
	svc := auth.NewService(auth.Config{Store: st})
	if _, _, err := svc.AcceptInvitation(ctx, m[1], "a long enough password", "192.0.2.1"); err != nil {
		t.Fatalf("accept: %v", err)
	}
}

func TestPublicURLFromEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hostashi.env")
	if err := os.WriteFile(p, []byte("HOSTASHI_LISTEN=127.0.0.1:8080\nHOSTASHI_PUBLIC_URL=https://panel.example:8443\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := publicURLFromEnv(p); got != "https://panel.example:8443" {
		t.Fatalf("%q", got)
	}

	if got := publicURLFromEnv(p + ".none"); got != "" {
		t.Fatalf("missing file: %q", got)
	}
}
