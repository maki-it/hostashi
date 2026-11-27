// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeStore struct {
	people []store.NotifyRecipient
	grants map[int64][]string
}

func (f *fakeStore) NotificationRecipients(context.Context) ([]store.NotifyRecipient, error) {
	return f.people, nil
}

func (f *fakeStore) HasSiteGrant(_ context.Context, id int64, site string) (bool, error) {
	for _, s := range f.grants[id] {
		if s == site {
			return true, nil
		}
	}

	return false, nil
}

// fakeOutbox deduplicates by key like the real one, without a window.
type fakeOutbox struct {
	sent []mail.Message
	envs []mail.Envelope
	seen map[string]bool
	off  bool
}

func (f *fakeOutbox) Enqueue(_ context.Context, m mail.Message, e mail.Envelope) (bool, error) {
	if f.off {
		return false, mail.ErrNotConfigured
	}

	if f.seen == nil {
		f.seen = map[string]bool{}
	}

	if f.seen[e.DedupKey] {
		return false, nil
	}

	f.seen[e.DedupKey] = true
	f.sent = append(f.sent, m)
	f.envs = append(f.envs, e)

	return true, nil
}

func (f *fakeOutbox) to() []string {
	out := []string{}
	for _, m := range f.sent {
		out = append(out, m.To)
	}

	return out
}

func newNotifier(t *testing.T) (*Notifier, *fakeStore, *fakeOutbox) {
	t.Helper()

	st := &fakeStore{
		people: []store.NotifyRecipient{
			{UserID: 1, Username: "kim", Role: "admin", Email: "kim@example.com", Prefs: map[string]bool{}},
			{UserID: 2, Username: "ada", Role: "admin", Email: "ada@example.com", Prefs: map[string]bool{"backups": false}},
			{UserID: 3, Username: "mona", Role: "manager", Email: "mona@example.com", Prefs: map[string]bool{"certificates": true, "backups": true}},
			{UserID: 4, Username: "uli", Role: "user", Email: "uli@example.com", Prefs: map[string]bool{"certificates": true}},
			{UserID: 5, Username: "max", Role: "user", Email: "max@example.com", Prefs: map[string]bool{}},
		},
		grants: map[int64][]string{4: {"shop"}, 5: {"shop"}},
	}
	ob := &fakeOutbox{}

	return &Notifier{
		St: st, Outbox: ob, Host: "panel.example.com",
		Link: func(f string) string { return "https://panel.example.com/#/" + f },
		Log:  slog.New(slog.DiscardHandler),
	}, st, ob
}

func TestEnabledDefaults(t *testing.T) {
	for _, c := range []struct {
		role  string
		prefs map[string]bool
		cat   Category
		want  bool
	}{
		{"admin", nil, Backups, true},
		{"admin", map[string]bool{"backups": false}, Backups, false},
		{"manager", nil, Certificates, false},
		{"manager", map[string]bool{"certificates": true}, Certificates, true},
		{"manager", map[string]bool{"backups": true}, Backups, false}, // server settings: admins only
		{"user", map[string]bool{"doctor": true}, Doctor, false},
		{"user", map[string]bool{"php": true}, PHP, true},
		{"admin", nil, Services, true},
		{"user", map[string]bool{"services": true}, Services, true},
		{"manager", nil, Services, false},
		{"admin", nil, Deploys, false}, // opt-in for everyone (ADR-0062)
		{"user", map[string]bool{"deploys": true}, Deploys, true},
		{"admin", nil, Category("other"), false},
	} {
		if got := Enabled(c.role, c.prefs, c.cat); got != c.want {
			t.Errorf("%s %v %s: %v", c.role, c.prefs, c.cat, got)
		}
	}
}

func TestNotifyRecipients(t *testing.T) {
	n, _, ob := newNotifier(t)
	ctx := t.Context()

	// A server-wide event reaches admins who did not switch it off.
	if q, err := n.Notify(ctx, Event{Category: Backups, Key: "b1", Fragment: "backups", Mail: mail.OpsEvent{Template: "backup-failed", Subject: "Backup failed", Lines: []string{"x"}}}); err != nil || q != 1 {
		t.Fatalf("backups: %d %v", q, err)
	}

	if got := strings.Join(ob.to(), ","); got != "kim@example.com" {
		t.Fatalf("backups to %s", got)
	}

	// A site event reaches admins, managers who opted in, and users with
	// a grant who opted in.
	ob.sent = nil

	if q, err := n.Notify(ctx, Event{Category: Certificates, Site: "shop", Key: "c1", Fragment: "sites/shop", Mail: mail.OpsEvent{Template: "cert-expiry", Subject: "s"}}); err != nil || q != 4 {
		t.Fatalf("cert: %d %v", q, err)
	}

	if got := strings.Join(ob.to(), ","); got != "kim@example.com,ada@example.com,mona@example.com,uli@example.com" {
		t.Fatalf("cert to %s", got)
	}

	body := ob.sent[0].Body

	// Another site: the user has no grant.
	ob.sent = nil

	if _, err := n.Notify(ctx, Event{Category: Certificates, Site: "blog", Key: "c2", Mail: mail.OpsEvent{Template: "cert-expiry", Subject: "s"}}); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(ob.to(), ","); strings.Contains(got, "uli") {
		t.Fatalf("no grant: %s", got)
	}

	// The same key is sent once per recipient.
	if q, _ := n.Notify(ctx, Event{Category: Backups, Key: "b1", Mail: mail.OpsEvent{Template: "backup-failed", Subject: "s"}}); q != 0 {
		t.Fatalf("dedup: %d", q)
	}

	if ob.envs[0].DedupKey != "b1:1" || ob.envs[0].Category != mail.Operations {
		t.Fatalf("envelope: %+v", ob.envs[0])
	}

	for _, want := range []string{"Hello kim,", "Details in the panel: https://panel.example.com/#/sites/shop", "notifications about certificates", "Account, Notifications"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}

	ob.off = true

	if q, err := n.Notify(ctx, Event{Category: Backups, Key: "b9", Mail: mail.OpsEvent{Template: "backup-failed", Subject: "s"}}); err != nil || q != 0 {
		t.Fatalf("no relay: %d %v", q, err)
	}

	if _, err := n.Notify(ctx, Event{Category: "nope", Key: "x"}); err == nil {
		t.Fatal("unknown category accepted")
	}
}

func TestWatcherBackups(t *testing.T) {
	n, _, ob := newNotifier(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	status := agentapi.BackupStatus{OK: true, Finished: now.Add(-time.Hour)}
	w := &Watcher{
		Notifier: n, Now: func() time.Time { return now }, Log: n.Log,
		Backups: func(context.Context) (agentapi.BackupStatus, error) { return status, nil },
	}

	w.Check(t.Context())

	if len(ob.sent) != 0 {
		t.Fatalf("ok run reported: %+v", ob.sent)
	}

	status = agentapi.BackupStatus{Finished: now.Add(-time.Hour), Error: "repository locked\nmore", Items: []agentapi.BackupItemResult{
		{Kind: "files", Site: "shop", Partial: true},
		{Kind: "db", Site: "shop", Database: "shop_wp", Error: "mariadb-dump: access denied"},
		{Kind: "panel"},
	}}

	w.Check(t.Context())
	w.Check(t.Context())

	if len(ob.sent) != 1 {
		t.Fatalf("sent %d", len(ob.sent))
	}

	body := ob.sent[0].Body
	for _, want := range []string{"Error: repository locked", "- files of shop (some files could not be read)", "- database shop_wp: mariadb-dump: access denied", "#/backups"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}

	if strings.Contains(body, "more") {
		t.Errorf("second error line in body:\n%s", body)
	}

	// A run that is still going, or one from days ago, is not reported.
	status.Finished = now.Add(-72 * time.Hour)
	ob.seen = nil

	w.Check(t.Context())

	status.Finished, status.Running = now.Add(-time.Minute), true

	w.Check(t.Context())

	if len(ob.sent) != 1 {
		t.Fatalf("sent %d", len(ob.sent))
	}
}

func TestWatcherBackupOverdue(t *testing.T) {
	n, _, ob := newNotifier(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	status := agentapi.BackupStatus{OK: true, Finished: now.Add(-25 * time.Hour)}
	info := agentapi.BackupInfo{Settings: agentapi.DefaultBackupSettings(), Ready: true}
	w := &Watcher{
		Notifier: n, Now: func() time.Time { return now }, Log: n.Log,
		Backups:    func(context.Context) (agentapi.BackupStatus, error) { return status, nil },
		BackupInfo: func(context.Context) (agentapi.BackupInfo, error) { return info, nil },
	}

	// Daily: 25 hours is within the 26.
	w.Check(t.Context())

	if len(ob.sent) != 0 {
		t.Fatalf("daily run reported after 25 hours: %+v", ob.sent)
	}

	// Hourly: 4 hours without a run is missing, and reported once.
	info.Settings.IntervalHours = 1
	status.Finished = now.Add(-4 * time.Hour)

	w.Check(t.Context())
	w.Check(t.Context())

	if len(ob.sent) != 1 || ob.sent[0].Subject != "Backup missing" {
		t.Fatalf("sent %+v", ob.sent)
	}

	for _, want := range []string{"scheduled to run every hour", "hostashi-backup.timer", "#/backups"} {
		if !strings.Contains(ob.sent[0].Body, want) {
			t.Errorf("body lacks %q:\n%s", want, ob.sent[0].Body)
		}
	}

	// Not while a run is going, nor when backups are not set up.
	ob.seen = nil
	status.Running = true

	w.Check(t.Context())

	status.Running, info.Ready = false, false

	w.Check(t.Context())

	if len(ob.sent) != 1 {
		t.Fatalf("sent %d", len(ob.sent))
	}
}

func TestWatcherCerts(t *testing.T) {
	n, _, ob := newNotifier(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	certs := []Cert{
		{Site: "shop", Name: "shop", NotAfter: now.Add(60 * 24 * time.Hour)},
		{Site: "blog", Name: "blog", NotAfter: now.Add(10 * 24 * time.Hour), Error: "acme: 429 rate limited\ndetail", FailedAt: now.Add(-time.Hour)},
		{Name: "panel.example.com", NotAfter: now.Add(2 * 24 * time.Hour)},
	}
	w := &Watcher{
		Notifier: n, Now: func() time.Time { return now }, Log: n.Log,
		Certs: func(context.Context) ([]Cert, error) { return certs, nil },
	}

	w.Check(t.Context())

	subjects := []string{}

	for _, m := range ob.sent {
		if m.To == "kim@example.com" {
			subjects = append(subjects, m.Subject)
		}
	}

	want := "Certificate renewal failed: blog|Certificate expires soon: blog|Certificate expires soon: panel.example.com"
	if got := strings.Join(subjects, "|"); got != want {
		t.Fatalf("subjects:\n%s\nwant\n%s", got, want)
	}

	// The panel's certificate goes to admins only.
	for _, m := range ob.sent {
		if strings.Contains(m.Subject, "panel.example.com") && m.To == "mona@example.com" {
			t.Fatal("manager got the panel certificate")
		}
	}

	// Ten days later the blog certificate crosses the 3-day mark once.
	before := len(ob.sent)
	now = now.Add(8 * 24 * time.Hour)
	certs = certs[1:2]
	certs[0].Error = ""

	w.Check(t.Context())
	w.Check(t.Context())

	if got := len(ob.sent) - before; got != 3 { // kim, ada, mona; blog has no grants
		t.Fatalf("3-day mails: %d", got)
	}

	now = now.Add(3 * 24 * time.Hour)

	w.Check(t.Context())

	if got := ob.sent[len(ob.sent)-1].Subject; len(ob.sent)-before != 3 {
		t.Fatalf("expired sent again: %s", got)
	}
}

func TestWatcherDoctor(t *testing.T) {
	n, _, ob := newNotifier(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	runs := 0
	rep := agentapi.DoctorReport{Checks: []agentapi.DoctorCheck{
		{ID: "system.disk", Status: agentapi.DoctorFail, Summary: "/: 2 GB free of 38 GB (5 %)", Hint: "Free space now."},
		{ID: "system.disk", Status: agentapi.DoctorWarn, Summary: "/srv: 12 GB free of 100 GB (12 %)"},
		{ID: "service.nginx", Status: agentapi.DoctorFail, Summary: "nginx is not running"},
		{ID: "system.updates", Status: agentapi.DoctorWarn, Summary: "3 security updates"},
		{ID: "site.disk", Site: "shop", Status: agentapi.DoctorWarn, Summary: "files: 9.2 GB of 10 GB (92 %)", Hint: "Filling up."},
		{ID: "site.disk", Site: "blog", Status: agentapi.DoctorWarn, Summary: "files: 8.5 GB of 10 GB (85 %)"},
		{ID: "site.database", Site: "shop", Status: agentapi.DoctorFail, Summary: "databases: 25.0 GiB of 2.0 GiB (1250 %)", Hint: "Clean up."},
		{ID: "site.database", Site: "blog", Status: agentapi.DoctorWarn, Summary: "databases: 1.7 GiB of 2.0 GiB (85 %)"},
		{ID: "site.php", Site: "shop", Status: agentapi.DoctorWarn, Summary: "PHP 8.1 gets no security fixes since 2025-12-31", Hint: "Move the site."},
		{ID: "site.home", Site: "shop", Status: agentapi.DoctorFail, Summary: "home is world-readable"},
		{ID: "hostashi.update", Status: agentapi.DoctorWarn, Summary: "hostashi 0.2.0 is available (installed: 0.1.0)", Hint: "Install it with: sudo apt upgrade."},
	}}
	w := &Watcher{
		Notifier: n, Now: func() time.Time { return now }, Log: n.Log,
		Doctor: func(context.Context) (agentapi.DoctorReport, error) { runs++; return rep, nil },
	}

	w.Check(t.Context())
	w.Check(t.Context()) // within the hour: not run again

	if runs != 1 {
		t.Fatalf("runs %d", runs)
	}

	byTo := map[string][]string{}
	for _, m := range ob.sent {
		byTo[m.To] = append(byTo[m.To], m.Subject)
	}

	// kim is an admin with every default; mona (manager) only opted into
	// certificates and backups; uli (user, grant on shop) only certificates.
	want := "The server's disk is almost full|The site shop is almost full|The databases of the site shop passed 90 % of their warning threshold|Old PHP version on the site shop|A new Hostashi version is available|Server checks found errors"
	if got := strings.Join(byTo["kim@example.com"], "|"); got != want {
		t.Fatalf("kim:\n%s\nwant\n%s", got, want)
	}

	if len(byTo["mona@example.com"]) != 0 || len(byTo["uli@example.com"]) != 0 {
		t.Fatalf("opted out got mail: %v", byTo)
	}

	var digest string

	for _, m := range ob.sent {
		if m.To == "kim@example.com" && m.Subject == "Server checks found errors" {
			digest = m.Body
		}
	}

	for _, want := range []string{"- nginx is not running", "- site shop: home is world-readable", "#/health"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}

	if strings.Contains(digest, "databases") {
		t.Errorf("database check in the digest:\n%s", digest)
	}

	for _, not := range []string{"security updates", "PHP 8.1", "38 GB"} {
		if strings.Contains(digest, not) {
			t.Errorf("digest has %q:\n%s", not, digest)
		}
	}

	// An hour later nothing new goes out: the same keys.
	before := len(ob.sent)
	now = now.Add(time.Hour)

	w.Check(t.Context())

	if runs != 2 || len(ob.sent) != before {
		t.Fatalf("runs %d, sent %d more", runs, len(ob.sent)-before)
	}
}
