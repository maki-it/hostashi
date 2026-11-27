// SPDX-License-Identifier: AGPL-3.0-or-later

package scanunit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type fakeUnits struct {
	states    map[string]string
	restarted []string
}

func (f *fakeUnits) Restart(_ context.Context, unit string) error {
	f.restarted = append(f.restarted, unit)
	f.states[unit] = "active"

	return nil
}

func (f *fakeUnits) State(_ context.Context, unit string) (string, error) {
	return f.states[unit], nil
}

func TestRealtimeConfig(t *testing.T) {
	s := string(RealtimeConfig([]string{"/srv/p2/shop", "/home/blog", "/home/blog", "/home/a b"}, "/var/lib/clamav"))
	for _, want := range []string{
		"LocalSocket /run/clamav/clamd.ctl\n",
		"DatabaseDirectory /var/lib/clamav\n",
		"ConcurrentDatabaseReload no\n",
		"ExcludePath ^/home/blog/\\.cache/\nOnAccessIncludePath /home/blog\nExcludePath ^/srv/p2/shop/\\.cache/\nOnAccessIncludePath /srv/p2/shop\n",
		"OnAccessExcludeUname clamav\n",
		"OnAccessPrevention no\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config lacks %q:\n%s", want, s)
		}
	}

	if strings.Count(s, "OnAccessIncludePath") != 2 || strings.Contains(s, "a b") {
		t.Errorf("homes not checked:\n%s", s)
	}
}

func TestRealtimeSync(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	units := &fakeUnits{states: map[string]string{ClamdUnit: "inactive", ClamonaccUnit: "inactive"}}
	r := &Realtime{Config: filepath.Join(dir, "etc", "clamd.conf"), Ctl: units}

	// No site: the configuration is written, clamonacc stays off.
	if active, err := r.Sync(ctx, nil); err != nil || active || len(units.restarted) != 0 {
		t.Fatalf("no sites: %v %v %v", active, err, units.restarted)
	}

	if active, err := r.Sync(ctx, []string{"/home/blog"}); err != nil || active || strings.Join(units.restarted, ",") != ClamonaccUnit {
		t.Fatalf("first site: %v %v %v", active, err, units.restarted)
	}

	units.restarted = nil

	if active, err := r.Sync(ctx, []string{"/home/blog"}); err != nil || !active || len(units.restarted) != 0 {
		t.Fatalf("unchanged: %v %v %v", active, err, units.restarted)
	}

	// A new site restarts a running clamd for its exclusions, and clamonacc.
	units.states[ClamdUnit] = "active"
	if _, err := r.Sync(ctx, []string{"/home/blog", "/home/shop"}); err != nil || strings.Join(units.restarted, ",") != ClamdUnit+","+ClamonaccUnit {
		t.Fatalf("new site: %v %v", err, units.restarted)
	}

	if fi, err := os.Stat(r.Config); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("config file: %v %v", fi, err)
	}
}

func TestRealtimePoll(t *testing.T) {
	dir := t.TempDir()
	blog, shop := filepath.Join(dir, "blog"), filepath.Join(dir, "shop")

	for _, h := range []string{blog, shop} {
		if err := os.MkdirAll(filepath.Join(h, "htdocs"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(blog, "htdocs", "x.php"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc/passwd", filepath.Join(shop, "htdocs", "link.php")); err != nil {
		t.Fatal(err)
	}

	logf := filepath.Join(dir, "realtime.log")
	r := &Realtime{Log: logf}
	homes := map[string]string{"blog": blog, "shop": shop}

	// No log yet.
	if out, err := r.Poll(homes, 0); err != nil || out.Cursor != 0 || len(out.Findings) != 0 {
		t.Fatalf("no log: %+v %v", out, err)
	}

	old := "ClamAV update process started\n" + blog + "/htdocs/x.php: Old.Sig FOUND\n"
	if err := os.WriteFile(logf, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	// -1 skips what is there.
	out, err := r.Poll(homes, -1)
	if err != nil || out.Cursor != int64(len(old)) || len(out.Findings) != 0 {
		t.Fatalf("skip: %+v %v", out, err)
	}

	more := blog + "/htdocs/x.php: Php.Webshell-1 FOUND\n" +
		blog + "/htdocs/x.php: Php.Webshell-1 FOUND\n" +
		shop + "/htdocs/link.php: Php.Webshell-1 FOUND\n" +
		"/etc/shadow: Php.Webshell-1 FOUND\n" +
		blog + "/htdocs/gone.php: Php.Webshell-1 FOUND\n" +
		blog + "/htdocs/half"

	f, err := os.OpenFile(logf, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = f.WriteString(more)
	_ = f.Close()

	out, err = r.Poll(homes, out.Cursor)
	if err != nil {
		t.Fatal(err)
	}

	want := agentapi.RealtimeFinding{Site: "blog", ScanFinding: agentapi.ScanFinding{Path: "htdocs/x.php", Signature: "Php.Webshell-1", Size: 3}}
	if len(out.Findings) != 1 || out.Findings[0].Site != want.Site || out.Findings[0].Path != want.Path || out.Findings[0].Size != 3 || len(out.Findings[0].SHA256) != 64 {
		t.Fatalf("findings %+v", out.Findings)
	}

	// The duplicate, the symlink, the path outside and the gone file.
	if out.Dropped != 4 {
		t.Fatalf("dropped %d", out.Dropped)
	}

	// The half line waits.
	if fi, _ := os.Stat(logf); out.Cursor != fi.Size()-int64(len(blog+"/htdocs/half")) {
		t.Fatalf("cursor %d of %d", out.Cursor, fi.Size())
	}

	// An emptied log starts again from the top.
	if err := os.WriteFile(logf, []byte(blog+"/htdocs/x.php: Php.Webshell-2 FOUND\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := r.Poll(homes, 1<<20); err != nil || len(out.Findings) != 1 || out.Findings[0].Signature != "Php.Webshell-2" {
		t.Fatalf("after truncation: %+v %v", out, err)
	}
}

func TestEngineRealtime(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ctl := &fakeEngine{states: map[string]string{}}
	units := &fakeUnits{states: map[string]string{ClamonaccUnit: "active"}}
	e := &Engine{Dir: dir, DatabaseDir: t.TempDir(), Ctl: ctl, Realtime: units, DpkgStatus: filepath.Join(dir, "dpkg-status")}
	on, off := true, false

	if _, err := e.Set(ctx, agentapi.ScanEngineSetRequest{Realtime: &on}); !errors.Is(err, ErrEngineOff) {
		t.Fatalf("real-time without ClamAV: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "state"), []byte("on\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if st, _ := e.Status(ctx); st.Realtime != agentapi.ScanEngineOff || st.RealtimeActive {
		t.Fatalf("off: %+v", st)
	}

	if st, err := e.Set(ctx, agentapi.ScanEngineSetRequest{Realtime: &on}); err != nil || st.Realtime != agentapi.ScanEngineInstalling || st.State != agentapi.ScanEngineOn {
		t.Fatalf("switch on: %+v %v", st, err)
	}

	if ctl.states[EngineRealtimeOn] != "activating" {
		t.Fatalf("unit not started: %v", ctl.states)
	}

	ctl.states[EngineRealtimeOn] = "inactive"

	if err := os.WriteFile(filepath.Join(dir, "realtime"), []byte("on\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if st, _ := e.Status(ctx); st.Realtime != agentapi.ScanEngineOn || !st.RealtimeActive || !e.RealtimeOn() {
		t.Fatalf("on: %+v", st)
	}

	if st, err := e.Set(ctx, agentapi.ScanEngineSetRequest{Realtime: &off}); err != nil || st.Realtime != agentapi.ScanEngineRemoving {
		t.Fatalf("switch off: %+v %v", st, err)
	}
}
