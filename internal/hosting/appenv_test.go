// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

func TestAppEnv(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()
	apps := &fakeAppAgent{installed: []string{"24"}, specs: map[string]agentapi.AppSpec{}}
	s.EnableApps(apps)

	if _, err := s.Create(ctx, 1, CreateRequest{
		Name: "app", Domains: []string{"app.example.com"},
		App: &store.SiteApp{Node: "24", StartKind: agentapi.AppStartScript, Start: "server.js"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	env := []agentapi.AppEnvVar{{Name: "DB_URL", Value: "postgres://db/x"}, {Name: "API_KEY", Value: "s3cr3t"}}

	if _, err := s.SetAppEnv(ctx, 1, "app", env); !errors.Is(err, ErrNoAppEnv) {
		t.Fatalf("without a key: %v", err)
	}

	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	s.EnableAppEnv(box)

	for name, e := range map[string][]agentapi.AppEnvVar{"reserved": {{Name: "PORT", Value: "80"}}, "bad name": {{Name: "a"}}} {
		if _, err := s.SetAppEnv(ctx, 1, "app", e); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := s.SetAppEnv(ctx, 1, "blog", env); !errors.Is(err, ErrSiteType) {
		t.Fatalf("php site: %v", err)
	}

	if _, err := s.SetAppEnv(ctx, 1, "app", env); err != nil {
		t.Fatal(err)
	}

	sorted := []agentapi.AppEnvVar{env[1], env[0]}
	if got := apps.specs["app"].Env; !reflect.DeepEqual(got, sorted) {
		t.Fatalf("sent %v", got)
	}

	if names, err := s.AppEnvNames(ctx, "app"); err != nil || !reflect.DeepEqual(names, []string{"API_KEY", "DB_URL"}) {
		t.Fatalf("names %v %v", names, err)
	}

	if got, err := s.AppEnv(ctx, "app"); err != nil || !reflect.DeepEqual(got, sorted) {
		t.Fatalf("values %v %v", got, err)
	}

	// Sealed in the database, bound to the site and the name.
	site, _ := s.Get(ctx, "app")

	rows, _ := st.AppEnv(ctx, site.ID)
	for _, r := range rows {
		if bytes.Contains(r.Value, []byte("s3cr3t")) || bytes.Contains(r.Value, []byte("postgres")) {
			t.Fatalf("plain value stored: %s", r.Name)
		}
	}

	swapped := []store.SealedEnvVar{{Name: "API_KEY", Value: rows[1].Value}, {Name: "DB_URL", Value: rows[0].Value}}
	if err := st.SetAppEnv(ctx, site.ID, swapped); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AppEnv(ctx, "app"); err == nil {
		t.Fatal("moved value opened")
	}

	if err := st.SetAppEnv(ctx, site.ID, rows); err != nil {
		t.Fatal(err)
	}

	// Other app changes keep the variables.
	if _, _, err := s.SetApp(ctx, 1, "app", store.SiteApp{Node: "24", Workdir: "htdocs", StartKind: agentapi.AppStartNPM, Start: "start"}); err != nil {
		t.Fatal(err)
	}

	if got := apps.specs["app"].Env; !reflect.DeepEqual(got, sorted) {
		t.Fatalf("after SetApp %v", got)
	}

	// The agent refuses: the previous variables stay.
	apps.applyErr = errDown
	if _, err := s.SetAppEnv(ctx, 1, "app", []agentapi.AppEnvVar{{Name: "OTHER", Value: "x"}}); !errors.Is(err, errDown) {
		t.Fatalf("refused: %v", err)
	}

	apps.applyErr = nil

	if names, _ := s.AppEnvNames(ctx, "app"); !reflect.DeepEqual(names, []string{"API_KEY", "DB_URL"}) {
		t.Fatalf("after a refused change: %v", names)
	}

	// The audit log names the variables, never the values.
	if _, err := s.SetAppEnv(ctx, 1, "app", []agentapi.AppEnvVar{{Name: "API_KEY", Value: "n3w"}}); err != nil {
		t.Fatal(err)
	}

	entries, _, err := st.AuditEntries(ctx, store.AuditFilter{Action: "site.app.env"}, 0, 10)
	if err != nil || len(entries) != 2 {
		t.Fatalf("audit %d %v", len(entries), err)
	}

	for _, e := range entries {
		raw := append(append([]byte{}, e.Diff...), e.Detail...)
		if bytes.Contains(raw, []byte("s3cr3t")) || bytes.Contains(raw, []byte("n3w")) || bytes.Contains(raw, []byte("postgres")) || !bytes.Contains(raw, []byte("API_KEY")) {
			t.Fatalf("value in the audit log: %s", raw)
		}
	}

	if got := apps.specs["app"].Env; !reflect.DeepEqual(got, []agentapi.AppEnvVar{{Name: "API_KEY", Value: "n3w"}}) {
		t.Fatalf("sent %v", got)
	}
}
