// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/statuspage"
)

func TestMaintenanceWithBuiltinPage(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	site, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{Enabled: true, AllowIPs: []string{" 2001:DB8::1 ", "10.0.0.0/8", "10.0.0.0/8"}})
	if err != nil || !site.Maintenance || site.MaintenancePage != 0 {
		t.Fatalf("%+v %v", site, err)
	}

	// PHP keeps running; addresses are canonical and unique.
	if got := ag.live(); got != "[blog=[blog.example.com]+php8.4+maint(builtin-maintenance[2001:db8::1 10.0.0.0/8])]" {
		t.Fatalf("live %s", got)
	}

	if ag.uploads != 1 {
		t.Fatalf("%d uploads", ag.uploads)
	}

	// The next change does not send the page again.
	if _, err := s.SetPHP(ctx, 1, "blog", "8.3"); err != nil || ag.uploads != 1 {
		t.Fatalf("%v, %d uploads", err, ag.uploads)
	}

	// Disabled wins.
	if _, err := s.SetDisabled(ctx, 1, "blog", true, ""); err != nil || ag.live() != "[blog=[blog.example.com]+disabled(builtin-maintenance)]" {
		t.Fatalf("%v live %s", err, ag.live())
	}

	if _, err := s.SetDisabled(ctx, 1, "blog", false, ""); err != nil {
		t.Fatal(err)
	}

	for _, bad := range [][]string{{"10.1.2.3/8"}, {"example.com"}, {"1.2.3.4; allow all"}} {
		if _, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{Enabled: true, AllowIPs: bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", bad, err)
		}
	}

	many := make([]string, 0, agentapi.MaxAllowIPs+1)
	for i := range agentapi.MaxAllowIPs + 1 {
		many = append(many, fmt.Sprintf("192.0.2.%d", i))
	}

	if _, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{Enabled: true, AllowIPs: many}); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many: %v", err)
	}

	if _, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{Enabled: true, Page: "p99"}); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("unknown page: %v", err)
	}

	// The agent refuses: the old settings stay.
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "nginx down"}

	if _, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{}); err == nil {
		t.Fatal("change succeeded although the agent refused")
	}

	if got, _ := s.Get(ctx, "blog"); !got.Maintenance || !slices.Equal(got.MaintenanceAllow, []string{"2001:db8::1", "10.0.0.0/8"}) {
		t.Fatalf("failed change was stored: %+v", got)
	}

	ag.webErr = nil

	if site, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{}); err != nil || site.Maintenance || ag.live() != "[blog=[blog.example.com]+php8.3]" {
		t.Fatalf("%+v %v live %s", site, err, ag.live())
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action LIKE ?`, "site.maintenance.%", &n); err != nil || n != 2 {
		t.Fatalf("audit entries %d %v", n, err)
	}
}

func TestUploadedPages(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	p, err := s.CreatePage(ctx, 1, " Summer break ", statuspage.KindMaintenance, PageUpload{Filename: "page.html", Data: []byte("<h1>Back in August</h1>")})
	if err != nil || p.ID != "p1" || p.Name != "Summer break" || p.Builtin || len(p.Files) != 1 {
		t.Fatalf("%+v %v", p, err)
	}

	parking, err := s.CreatePage(ctx, 1, "Parked", statuspage.KindParking, PageUpload{Filename: "p.html", Data: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}

	for name, try := range map[string]func() error{
		"bad kind": func() error {
			_, err := s.CreatePage(ctx, 1, "x", "other", PageUpload{Filename: "a.html", Data: []byte("x")})
			return err
		},
		"bad file": func() error {
			_, err := s.CreatePage(ctx, 1, "x", statuspage.KindMaintenance, PageUpload{Filename: "a.php", Data: []byte("x")})
			return err
		},
		"no name": func() error {
			_, err := s.CreatePage(ctx, 1, " ", statuspage.KindMaintenance, PageUpload{Filename: "a.html", Data: []byte("x")})
			return err
		},
		"parking page for maintenance": func() error {
			_, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{Enabled: true, Page: parking.ID})
			return err
		},
	} {
		if err := try(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{Enabled: true, Page: p.ID}); err != nil {
		t.Fatal(err)
	}

	if got := ag.live(); got != "[blog=[blog.example.com]+maint(p1[])]" {
		t.Fatalf("live %s", got)
	}

	// New files go live at once.
	before := ag.web.Sites[0].Maintenance.Digest

	p, err = s.UpdatePage(ctx, 1, p.ID, "", &PageUpload{Filename: "page.html", Data: []byte("<h1>Back in September</h1>")})
	if err != nil || p.Digest == before || ag.web.Sites[0].Maintenance.Digest != p.Digest {
		t.Fatalf("%+v %v", p, err)
	}

	// If that fails, the old files stay.
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "nginx down"}

	if _, err := s.UpdatePage(ctx, 1, p.ID, "", &PageUpload{Filename: "page.html", Data: []byte("broken")}); err == nil {
		t.Fatal("update succeeded although the agent refused")
	}

	if got, _ := s.Page(ctx, p.ID); got.Digest != p.Digest {
		t.Fatal("failed update was stored")
	}

	ag.webErr = nil

	if err := s.DeletePage(ctx, 1, p.ID); !errors.Is(err, ErrPageInUse) {
		t.Fatalf("delete in use: %v", err)
	}

	for _, id := range []string{statuspage.BuiltinMaintenance, statuspage.BuiltinParking} {
		if err := s.DeletePage(ctx, 1, id); !errors.Is(err, ErrPageBuiltin) {
			t.Errorf("delete %s: %v", id, err)
		}
	}

	all, err := s.Pages(ctx)
	if err != nil || len(all) != 4 || all[0].ID != statuspage.BuiltinMaintenance || all[1].ID != statuspage.BuiltinParking || !slices.Equal(all[3].Sites, []string{"blog"}) {
		t.Fatalf("%+v %v", all, err)
	}

	if _, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeletePage(ctx, 1, p.ID); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(ag.pageGones, []string{"p1"}) {
		t.Fatalf("agent deletes %v", ag.pageGones)
	}

	if _, err := s.Page(ctx, "p1"); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("deleted page: %v", err)
	}

	for _, id := range []string{"p0", "p01", "x", "p-1", ""} {
		if _, err := s.Page(ctx, id); !errors.Is(err, ErrPageNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action LIKE ?`, "page.%", &n); err != nil || n != 4 {
		t.Fatalf("audit entries %d %v", n, err)
	}
}

// TestDisabledSiteShowsMaintenancePage checks ADR-0047: a disabled site
// shows the built-in maintenance page or a chosen one, parking pages and
// unknown pages are refused, a failed change keeps the old page, and the
// page counts as in use.
func TestDisabledSiteShowsMaintenancePage(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	p, err := s.CreatePage(ctx, 1, "Contract ended", statuspage.KindMaintenance, PageUpload{Filename: "page.html", Data: []byte("<h1>Ended</h1>")})
	if err != nil {
		t.Fatal(err)
	}

	parking, err := s.CreatePage(ctx, 1, "Parked", statuspage.KindParking, PageUpload{Filename: "p.html", Data: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}

	site, err := s.SetDisabled(ctx, 1, "blog", true, p.ID)
	if err != nil || DisabledPageID(site) != p.ID || ag.live() != "[blog=[blog.example.com]+disabled("+p.ID+")]" {
		t.Fatalf("%+v %v live %s", site, err, ag.live())
	}

	if err := s.DeletePage(ctx, 1, p.ID); !errors.Is(err, ErrPageInUse) {
		t.Fatalf("delete page in use: %v", err)
	}

	pages, err := s.Pages(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, pg := range pages {
		if pg.ID == p.ID && !slices.Equal(pg.Sites, []string{"blog"}) {
			t.Errorf("page sites %v", pg.Sites)
		}
	}

	for _, bad := range []string{parking.ID, "p99", "nonsense"} {
		if _, err := s.SetDisabled(ctx, 1, "blog", true, bad); err == nil {
			t.Errorf("page %q accepted", bad)
		}
	}

	if _, err := s.SetDisabled(ctx, 1, "blog", true, parking.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("parking page: %v", err)
	}

	// The agent refuses: page and state stay.
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "nginx down"}

	if _, err := s.SetDisabled(ctx, 1, "blog", true, ""); err == nil {
		t.Fatal("change succeeded although the agent refused")
	}

	if got, _ := s.Get(ctx, "blog"); !got.Disabled || DisabledPageID(got) != p.ID {
		t.Fatalf("failed change was stored: %+v", got)
	}

	ag.webErr = nil

	// Enabling keeps the choice; the built-in page is the default.
	site, err = s.SetDisabled(ctx, 1, "blog", false, "")
	if err != nil || site.Disabled || DisabledPageID(site) != p.ID {
		t.Fatalf("enable %+v %v", site, err)
	}

	site, err = s.SetDisabled(ctx, 1, "blog", true, "")
	if err != nil || DisabledPageID(site) != statuspage.BuiltinMaintenance || ag.live() != "[blog=[blog.example.com]+disabled(builtin-maintenance)]" {
		t.Fatalf("built-in %+v %v live %s", site, err, ag.live())
	}

	if err := s.DeletePage(ctx, 1, p.ID); err != nil {
		t.Fatalf("delete unused page: %v", err)
	}
}
