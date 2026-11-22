// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestSiteRealIP(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if r, err := s.SiteRealIP(ctx, site.ID); err != nil || !r.RealIP.IsZero() || r.SiteID != site.ID || !r.UpdatedAt.IsZero() {
		t.Fatalf("none: %+v %v", r, err)
	}

	want := agentapi.WebRealIP{Cloudflare: true, Ranges: []string{"10.0.0.2", "198.51.100.0/24"}, Header: agentapi.RealIPCFConnectingIP}

	old, err := s.SetSiteRealIP(ctx, SiteRealIP{SiteID: site.ID, RealIP: want}, now)
	if err != nil || !old.RealIP.IsZero() {
		t.Fatalf("set: %+v %v", old, err)
	}

	got, err := s.SiteRealIP(ctx, site.ID)
	if err != nil || !reflect.DeepEqual(got.RealIP, want) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("get: %+v %v", got, err)
	}

	all, err := s.AllSiteRealIP(ctx)
	if err != nil || len(all) != 1 || !reflect.DeepEqual(all[site.ID].RealIP, want) {
		t.Fatalf("all: %+v %v", all, err)
	}

	// Trusting nobody removes the row.
	if _, err := s.SetSiteRealIP(ctx, SiteRealIP{SiteID: site.ID, RealIP: agentapi.WebRealIP{Ranges: []string{}}}, now); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllSiteRealIP(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after reset: %+v %v", all, err)
	}

	if _, err := s.w.ExecContext(ctx, `INSERT INTO site_realip (site_id, realip, updated_at) VALUES (?, '"x"', 0)`, site.ID); err == nil {
		t.Fatal("JSON string accepted")
	}

	// Deleting the site deletes its setting.
	if _, err := s.SetSiteRealIP(ctx, SiteRealIP{SiteID: site.ID, RealIP: agentapi.WebRealIP{Cloudflare: true}}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, site.ID); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllSiteRealIP(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after site delete: %+v %v", all, err)
	}
}
