// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"reflect"
	"testing"
	"time"
)

func TestAppEnv(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "app", "default", "", []string{"app.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if env, err := s.AppEnv(ctx, site.ID); err != nil || len(env) != 0 {
		t.Fatalf("none: %v %v", env, err)
	}

	want := []SealedEnvVar{{Name: "API_KEY", Value: []byte{1, 2}}, {Name: "DB_URL", Value: []byte{3}}}
	if err := s.SetAppEnv(ctx, site.ID, []SealedEnvVar{want[1], want[0]}); err != nil {
		t.Fatal(err)
	}

	if env, err := s.AppEnv(ctx, site.ID); err != nil || !reflect.DeepEqual(env, want) {
		t.Fatalf("set: %v %v", env, err)
	}

	// A bad name fails the whole replacement.
	if err := s.SetAppEnv(ctx, site.ID, []SealedEnvVar{{Name: "OK", Value: []byte{1}}, {Name: "bad-name", Value: []byte{1}}}); err == nil {
		t.Fatal("bad name stored")
	}

	if env, _ := s.AppEnv(ctx, site.ID); !reflect.DeepEqual(env, want) {
		t.Fatalf("after a failed replacement: %v", env)
	}

	if err := s.SetAppEnv(ctx, site.ID, nil); err != nil {
		t.Fatal(err)
	}

	if env, _ := s.AppEnv(ctx, site.ID); len(env) != 0 {
		t.Fatalf("cleared: %v", env)
	}

	// Deleting the site deletes its variables.
	if err := s.SetAppEnv(ctx, site.ID, want); err != nil {
		t.Fatal(err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, site.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM site_app_env`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left: %d %v", n, err)
	}
}
