// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

func newService(t *testing.T, target cpmigrate.TargetFunc) (*cpmigrate.Service, *store.Store) {
	t.Helper()

	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = st.Close() })

	// The uploads below run as user 1.
	if err := st.RawExec(t.Context(), `INSERT INTO users (id, username) VALUES (1, 'kim')`); err != nil {
		t.Fatal(err)
	}

	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	if target == nil {
		target = func(context.Context) (plan.Target, error) { return plan.Target{PHP: []string{"8.4"}}, nil }
	}

	return cpmigrate.New(st, box, target, nil), st
}

func report(t *testing.T) []byte {
	t.Helper()

	b, err := json.Marshal(scan.Report{Format: scan.Format, Sites: []scan.Site{{
		Name: "a.example.com", User: "alpha", Type: "static", Status: scan.StatusClean,
		Domains: []string{"a.example.com"}, DocRoot: "/home/alpha/htdocs/a.example.com",
		Sources: []string{"/etc/nginx/sites-enabled/a.example.com.conf"},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestUploadLimitAndInvalid(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := t.Context()

	if _, err := svc.Upload(ctx, 1, strings.NewReader(`{"format":"nope"}`)); !errors.Is(err, cpmigrate.ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}

	for range cpmigrate.MaxReports {
		if _, err := svc.Upload(ctx, 1, bytes.NewReader(report(t))); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := svc.Upload(ctx, 1, bytes.NewReader(report(t))); !errors.Is(err, cpmigrate.ErrTooMany) {
		t.Fatalf("limit: %v", err)
	}
}

func TestSealedReportIsBoundToItsID(t *testing.T) {
	svc, st := newService(t, nil)
	ctx := t.Context()

	a, err := svc.Upload(ctx, 1, bytes.NewReader(report(t)))
	if err != nil {
		t.Fatal(err)
	}

	row, err := st.CloudPanelReportByID(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}

	other := strings.Repeat("c", 32)
	row.ID = other

	if err := st.CreateCloudPanelReport(ctx, row); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Get(ctx, other); err == nil || errors.Is(err, cpmigrate.ErrNotFound) {
		t.Fatalf("moved ciphertext opened: %v", err)
	}

	if got, err := svc.Get(ctx, a.ID); err != nil || got.Plan.Summary.Import != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestTargetError(t *testing.T) {
	svc, _ := newService(t, func(context.Context) (plan.Target, error) { return plan.Target{}, errors.New("agent down") })

	if _, err := svc.Upload(t.Context(), 1, bytes.NewReader(report(t))); err == nil || !strings.Contains(err.Error(), "agent down") {
		t.Fatalf("got %v", err)
	}
}

func TestFailedPlanKeepsNothing(t *testing.T) {
	svc, _ := newService(t, func(context.Context) (plan.Target, error) { return plan.Target{}, errors.New("agent down") })

	_, _ = svc.Upload(t.Context(), 1, bytes.NewReader(report(t)))

	if list, err := svc.List(t.Context()); err != nil || len(list) != 0 {
		t.Fatalf("%v %v", list, err)
	}
}
