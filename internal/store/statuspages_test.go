// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStatusPagesAndMaintenance(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)
	digest := strings.Repeat("a", 64)

	id, err := s.CreateStatusPage(ctx, "Our maintenance", "maintenance", digest, map[string][]byte{"index.html": []byte("<h1>x</h1>"), "css/a.css": {}}, now)
	if err != nil {
		t.Fatal(err)
	}

	p, err := s.StatusPageByID(ctx, id)
	if err != nil || p.Name != "Our maintenance" || p.Kind != "maintenance" || len(p.Files) != 2 || p.Files[0].Path != "css/a.css" || p.Files[1].Size != 10 || len(p.Sites) != 0 {
		t.Fatalf("%+v %v", p, err)
	}

	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if err := s.SetSiteMaintenance(ctx, "blog", true, id, []string{"10.0.0.0/8", "2001:db8::1"}, now); err != nil {
		t.Fatal(err)
	}

	site, err := s.SiteByName(ctx, "blog")
	if err != nil || !site.Maintenance || site.MaintenancePage != id || !slices.Equal(site.MaintenanceAllow, []string{"10.0.0.0/8", "2001:db8::1"}) {
		t.Fatalf("%+v %v", site, err)
	}

	if err := s.SetSiteMaintenance(ctx, "blog", false, id+100, nil, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown page: %v", err)
	}

	if err := s.SetSiteMaintenance(ctx, "nosuch", true, 0, nil, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	// Off, but the page is still named.
	if err := s.SetSiteMaintenance(ctx, "blog", false, id, nil, now); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteStatusPage(ctx, id); !errors.Is(err, ErrPageInUse) {
		t.Fatalf("delete in use: %v", err)
	}

	if p, _ := s.StatusPageByID(ctx, id); !slices.Equal(p.Sites, []string{"blog"}) {
		t.Fatalf("sites %v", p.Sites)
	}

	newDigest := strings.Repeat("b", 64)
	if err := s.UpdateStatusPage(ctx, id, "", newDigest, map[string][]byte{"index.html": []byte("new")}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	files, err := s.StatusPageFiles(ctx, id)
	if err != nil || len(files) != 1 || string(files["index.html"]) != "new" {
		t.Fatalf("%v %v", files, err)
	}

	if err := s.UpdateStatusPage(ctx, id, "Renamed", "", nil, now); err != nil {
		t.Fatal(err)
	}

	if p, _ := s.StatusPageByID(ctx, id); p.Name != "Renamed" || p.Digest != newDigest || len(p.Files) != 1 {
		t.Fatalf("%+v", p)
	}

	if err := s.SetSiteMaintenance(ctx, "blog", false, 0, nil, now); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteStatusPage(ctx, id); err != nil {
		t.Fatal(err)
	}

	if files, _ := s.StatusPageFiles(ctx, id); len(files) != 0 {
		t.Fatal("files left behind")
	}

	if err := s.DeleteStatusPage(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
