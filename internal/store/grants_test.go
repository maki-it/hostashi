// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"testing"
	"testing/fstest"
	"time"
)

// grantsEnv has an admin (id 1), a user (id 2) and the sites alpha and beta.
func grantsEnv(t *testing.T) (*Store, User, int64) {
	t.Helper()

	s := open(t, dbPath(t))
	ctx := context.Background()

	admin, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `INSERT INTO users (username) VALUES ('customer')`); err != nil {
		t.Fatal(err)
	}

	u, err := s.UserByUsername(ctx, "customer")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"alpha", "beta"} {
		if _, err := s.CreateSite(ctx, name, "default", "8.4", []string{name + ".example"}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	return s, admin, u.ID
}

func TestNewAccountGetsLeastRole(t *testing.T) {
	s, admin, uid := grantsEnv(t)
	ctx := context.Background()

	if admin.Role != "admin" {
		t.Fatalf("first user role %q", admin.Role)
	}

	u, err := s.UserByID(ctx, uid)
	if err != nil || u.Role != "user" {
		t.Fatalf("default role %q, %v", u.Role, err)
	}

	for _, bad := range []string{"root", "Admin", ""} {
		if err := s.RawExec(ctx, `UPDATE users SET role = ? WHERE id = ?`, bad, uid); err == nil {
			t.Errorf("role %q accepted", bad)
		}
	}

	for _, ok := range []string{"manager", "user", "admin"} {
		if err := s.RawExec(ctx, `UPDATE users SET role = ? WHERE id = ?`, ok, uid); err != nil {
			t.Errorf("role %q: %v", ok, err)
		}
	}
}

func TestSiteGrants(t *testing.T) {
	s, admin, uid := grantsEnv(t)
	ctx := context.Background()
	now := time.Now()

	added, removed, err := s.SetSiteGrants(ctx, uid, []string{"beta", "alpha", "alpha"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(added, []string{"alpha", "beta"}) || len(removed) != 0 {
		t.Fatalf("added %v removed %v", added, removed)
	}

	got, err := s.SiteGrants(ctx, uid)
	if err != nil || !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("grants %v, %v", got, err)
	}

	added, removed, err = s.SetSiteGrants(ctx, uid, []string{"beta"}, now)
	if err != nil || len(added) != 0 || !slices.Equal(removed, []string{"alpha"}) {
		t.Fatalf("added %v removed %v, %v", added, removed, err)
	}

	if ok, err := s.HasSiteGrant(ctx, uid, "beta"); err != nil || !ok {
		t.Fatalf("beta granted %v, %v", ok, err)
	}

	for _, site := range []string{"alpha", "missing", ""} {
		if ok, err := s.HasSiteGrant(ctx, uid, site); err != nil || ok {
			t.Fatalf("%q granted %v, %v", site, ok, err)
		}
	}

	// An unknown site changes nothing.
	if _, _, err := s.SetSiteGrants(ctx, uid, []string{"alpha", "missing"}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	if got, _ := s.SiteGrants(ctx, uid); !slices.Equal(got, []string{"beta"}) {
		t.Fatalf("grants after failed set %v", got)
	}

	if _, _, err := s.SetSiteGrants(ctx, 999, nil, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}

	// Admins hold no grants, neither through the code nor through SQL.
	if _, _, err := s.SetSiteGrants(ctx, admin.ID, []string{"alpha"}, now); !errors.Is(err, ErrNotGrantee) {
		t.Fatalf("admin grant: %v", err)
	}

	if err := s.RawExec(ctx, `INSERT INTO site_grants (user_id, site_id, created_at) VALUES (?, 1, 0)`, admin.ID); err == nil {
		t.Fatal("trigger let an admin grant through")
	}

	if err := s.RawExec(ctx, `UPDATE site_grants SET site_id = 1`); err == nil {
		t.Fatal("grant changed in place")
	}

	// Clearing works.
	if _, removed, err := s.SetSiteGrants(ctx, uid, nil, now); err != nil || !slices.Equal(removed, []string{"beta"}) {
		t.Fatalf("clear: %v %v", removed, err)
	}

	big := make([]string, MaxGrantsPerUser+1)
	for i := range big {
		big[i] = "s" + strconv.Itoa(i)
	}

	if _, _, err := s.SetSiteGrants(ctx, uid, big, now); err == nil {
		t.Fatal("too many grants accepted")
	}
}

func TestGrantsFollowRoleAndSite(t *testing.T) {
	s, _, uid := grantsEnv(t)
	ctx := context.Background()

	if _, _, err := s.SetSiteGrants(ctx, uid, []string{"alpha", "beta"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteSite(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SiteGrants(ctx, uid); !slices.Equal(got, []string{"beta"}) {
		t.Fatalf("grants after site delete %v", got)
	}

	// Becoming a manager ends every grant.
	if err := s.RawExec(ctx, `UPDATE users SET role = 'manager' WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SiteGrants(ctx, uid); len(got) != 0 {
		t.Fatalf("manager keeps grants %v", got)
	}
}

func TestAuditKeepsActorName(t *testing.T) {
	s, _, uid := grantsEnv(t)
	ctx := context.Background()

	if err := s.Audit(ctx, &uid, "site.create", "alpha", nil); err != nil {
		t.Fatal(err)
	}

	if err := s.Audit(ctx, nil, "token.rejected", "", nil); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `DELETE FROM users WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE actor = ? AND user_id IS NULL`, "customer", &n); err != nil || n != 1 {
		t.Fatalf("actor kept %d, %v", n, err)
	}

	if err := s.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE actor = ? AND action = 'token.rejected'`, "", &n); err != nil || n != 1 {
		t.Fatalf("anonymous entry %d, %v", n, err)
	}

	if err := s.RawExec(ctx, `UPDATE audit_log SET actor = 'someone'`); err == nil {
		t.Fatal("actor changed")
	}
}

// The role migration must keep the existing admin, its sessions, tokens and
// audit entries: rebuilding users would have cascaded deletes.
func TestRoleMigrationKeepsAccount(t *testing.T) {
	const before = 20 // last migration without roles

	old := fstest.MapFS{}

	err := fs.WalkDir(embedded, "migrations", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		var v int
		if _, err := fmt.Sscanf(path.Base(p), "%04d", &v); err != nil {
			return err
		}

		if v <= before {
			b, err := fs.ReadFile(embedded, p)
			if err != nil {
				return err
			}

			old[p] = &fstest.MapFile{Data: b}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	migrations = old

	t.Cleanup(func() { migrations = embedded })

	p := dbPath(t)

	s, err := Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	u, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateSession(ctx, u.ID, make32(1), time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `INSERT INTO audit_log (user_id, action) VALUES (?, 'login')`, u.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `INSERT INTO api_tokens (user_id, name, prefix, token_hash, scopes, created_at)
		VALUES (?, 't', 'abc', ?, '[]', 0)`, u.ID, make32(2)); err != nil {
		t.Fatal(err)
	}

	_ = s.Close()
	migrations = embedded
	s = open(t, p)

	got, err := s.UserByID(ctx, u.ID)
	if err != nil || got.Role != "admin" || got.PasswordHash != "hash" {
		t.Fatalf("user after migration %+v, %v", got, err)
	}

	var n int
	for q, want := range map[string]int{
		`SELECT count(*) FROM sessions WHERE user_id = ?`:                    1,
		`SELECT count(*) FROM api_tokens WHERE user_id = ?`:                  1,
		`SELECT count(*) FROM audit_log WHERE user_id = ? AND actor = 'kim'`: 1,
	} {
		if err := s.RawCount(ctx, q, u.ID, &n); err != nil || n != want {
			t.Errorf("%s: %d, %v", q, n, err)
		}
	}
}

func make32(b byte) []byte {
	out := make([]byte, 32)
	out[0] = b

	return out
}
