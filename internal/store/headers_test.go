// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestSiteHeaders(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if h, err := s.SiteHeaders(ctx, site.ID); err != nil || !h.Headers.IsZero() || h.SiteID != site.ID || !h.UpdatedAt.IsZero() {
		t.Fatalf("none: %+v %v", h, err)
	}

	want := agentapi.WebHeaders{NoSniff: true, FrameOptions: agentapi.FrameDeny, CSP: "default-src 'self'", CSPReportOnly: true}

	old, err := s.SetSiteHeaders(ctx, SiteHeaders{SiteID: site.ID, Headers: want}, now)
	if err != nil || !old.Headers.IsZero() {
		t.Fatalf("set: %+v %v", old, err)
	}

	got, err := s.SiteHeaders(ctx, site.ID)
	if err != nil || got.Headers != want || !got.UpdatedAt.Equal(now) {
		t.Fatalf("get: %+v %v", got, err)
	}

	all, err := s.AllSiteHeaders(ctx)
	if err != nil || len(all) != 1 || all[site.ID].Headers != want {
		t.Fatalf("all: %+v %v", all, err)
	}

	// None remove the row.
	if _, err := s.SetSiteHeaders(ctx, SiteHeaders{SiteID: site.ID}, now); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllSiteHeaders(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after reset: %+v %v", all, err)
	}

	if _, err := s.w.ExecContext(ctx, `INSERT INTO site_headers (site_id, headers, updated_at) VALUES (?, '[1]', 0)`, site.ID); err == nil {
		t.Fatal("JSON array accepted")
	}

	// Deleting the site deletes its headers.
	if _, err := s.SetSiteHeaders(ctx, SiteHeaders{SiteID: site.ID, Headers: agentapi.DefaultWebHeaders}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, site.ID); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllSiteHeaders(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after site delete: %+v %v", all, err)
	}
}
