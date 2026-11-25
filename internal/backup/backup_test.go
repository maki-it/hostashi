// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/resticctl"
)

func storageBox() *Target {
	return &Target{User: "u123456-sub1", Host: "u123456.your-storagebox.de", Port: 23, Path: "hostashi/web3"}
}

func TestTargetValidate(t *testing.T) {
	if err := storageBox().Validate(); err != nil {
		t.Fatalf("storage box refused: %v", err)
	}

	if got := storageBox().Repository(); got != "sftp://u123456-sub1@u123456.your-storagebox.de:23/hostashi/web3" {
		t.Errorf("repository %q", got)
	}

	generic := map[string]struct {
		tg   Target
		repo string
		kh   string
	}{
		"linux server, absolute path": {
			Target{User: "backup_web3", Host: "backup.example.org", Port: 22, Path: "/srv/backups/web3"},
			"sftp://backup_web3@backup.example.org:22//srv/backups/web3", "backup.example.org",
		},
		"nas by ipv4": {
			Target{User: "First.Last", Host: "192.0.2.10", Port: 2222, Path: "hostashi"},
			"sftp://First.Last@192.0.2.10:2222/hostashi", "[192.0.2.10]:2222",
		},
		"ipv6": {
			Target{User: "u1", Host: "2001:db8::10", Port: 22, Path: "restic"},
			"sftp://u1@[2001:db8::10]:22/restic", "2001:db8::10",
		},
		"ipv6 other port": {
			Target{User: "u1", Host: "2001:db8::10", Port: 23, Path: "restic"},
			"sftp://u1@[2001:db8::10]:23/restic", "[2001:db8::10]:23",
		},
	}

	for name, c := range generic {
		if err := c.tg.Validate(); err != nil {
			t.Errorf("%s refused: %v", name, err)
			continue
		}

		if got := c.tg.Repository(); got != c.repo {
			t.Errorf("%s: repository %q, want %q", name, got, c.repo)
		}

		if got := c.tg.KnownHostsName(); got != c.kh {
			t.Errorf("%s: known hosts name %q, want %q", name, got, c.kh)
		}
	}

	bad := map[string]func(*Target){
		"empty user":        func(t *Target) { t.User = "" },
		"user with @":       func(t *Target) { t.User = "a@b" },
		"user as option":    func(t *Target) { t.User = "-oProxyCommand" },
		"user with colon":   func(t *Target) { t.User = "a:b" },
		"user with slash":   func(t *Target) { t.User = "a/b" },
		"user with space":   func(t *Target) { t.User = "a b" },
		"loopback host":     func(t *Target) { t.Host = "127.0.0.1" },
		"loopback ipv6":     func(t *Target) { t.Host = "::1" },
		"unspecified host":  func(t *Target) { t.Host = "0.0.0.0" },
		"multicast host":    func(t *Target) { t.Host = "224.0.0.1" },
		"bracketed ipv6":    func(t *Target) { t.Host = "[2001:db8::1]" },
		"ipv6 with zone":    func(t *Target) { t.Host = "fe80::1%eth0" },
		"ipv6 uppercase":    func(t *Target) { t.Host = "2001:DB8::1" },
		"ipv4 mapped ipv6":  func(t *Target) { t.Host = "::ffff:192.0.2.1" },
		"single label host": func(t *Target) { t.Host = "nas" },
		"uppercase host":    func(t *Target) { t.Host = "U1.Example.com" },
		"host with port":    func(t *Target) { t.Host = "u1.example.com:22" },
		"port zero":         func(t *Target) { t.Port = 0 },
		"port too large":    func(t *Target) { t.Port = 65536 },
		"double slash path": func(t *Target) { t.Path = "//etc" },
		"dotdot":            func(t *Target) { t.Path = "a/../../etc" },
		"dot segment":       func(t *Target) { t.Path = "a/./b" },
		"trailing slash":    func(t *Target) { t.Path = "a/" },
		"space in path":     func(t *Target) { t.Path = "a b" },
		"empty path":        func(t *Target) { t.Path = "" },
		"too many levels":   func(t *Target) { t.Path = "a/b/c/d/e/f/g/h/i" },
		"semicolon in path": func(t *Target) { t.Path = "a;b" },
	}

	for name, mutate := range bad {
		tg := storageBox()
		mutate(tg)

		if err := tg.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	d := DefaultSettings()
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults refused: %v", err)
	}

	if d.Retention != (Retention{Daily: 7, Weekly: 4, Monthly: 6}) || d.IntervalHours != 24 || d.WindowStart != 1 || d.WindowEnd != 5 || d.ReadParts != 12 {
		t.Errorf("defaults changed: %+v", d)
	}

	bad := map[string]func(*Settings){
		"no daily":         func(s *Settings) { s.Retention.Daily = 0 },
		"daily too many":   func(s *Settings) { s.Retention.Daily = agentapi.MaxBackupDaily + 1 },
		"weekly negative":  func(s *Settings) { s.Retention.Weekly = -1 },
		"monthly too many": func(s *Settings) { s.Retention.Monthly = agentapi.MaxBackupMonthly + 1 },
		"hourly negative":  func(s *Settings) { s.Retention.Hourly = -1 },
		"hourly too many":  func(s *Settings) { s.Retention.Hourly = agentapi.MaxBackupHourly + 1 },
		"yearly negative":  func(s *Settings) { s.Retention.Yearly = -1 },
		"yearly too many":  func(s *Settings) { s.Retention.Yearly = agentapi.MaxBackupYearly + 1 },
		"interval zero":    func(s *Settings) { s.IntervalHours = 0 },
		"interval 5":       func(s *Settings) { s.IntervalHours = 5 },
		"interval 48":      func(s *Settings) { s.IntervalHours = 48 },
		"interval minus":   func(s *Settings) { s.IntervalHours = -1 },
		"window reversed":  func(s *Settings) { s.WindowStart, s.WindowEnd = 5, 1 },
		"window empty":     func(s *Settings) { s.WindowStart, s.WindowEnd = 3, 3 },
		"window 25":        func(s *Settings) { s.WindowEnd = 25 },
		"read parts":       func(s *Settings) { s.ReadParts = -1 },
		"version":          func(s *Settings) { s.Version = 2 },
		"bad target":       func(s *Settings) { s.Target = &Target{User: "x"} },
	}

	for name, mutate := range bad {
		s := DefaultSettings()
		mutate(&s)

		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	ok := DefaultSettings()
	ok.Target, ok.Retention, ok.ReadParts, ok.WindowStart, ok.WindowEnd = storageBox(), Retention{Daily: 1}, 0, 0, 24

	if err := ok.Validate(); err != nil {
		t.Errorf("minimal settings refused: %v", err)
	}
}

func TestSettingsHourlyAndYearly(t *testing.T) {
	for _, h := range agentapi.BackupIntervals {
		s := DefaultSettings()
		s.IntervalHours = h
		s.Retention = Retention{Hourly: agentapi.MaxBackupHourly, Daily: 1, Yearly: agentapi.MaxBackupYearly}

		if err := s.Validate(); err != nil {
			t.Errorf("interval %d refused: %v", h, err)
		}
	}

	// Settings from before the hourly schedule run daily and keep no hourly
	// or yearly snapshots.
	var old Settings
	if err := json.Unmarshal([]byte(`{"version":1,"retention":{"daily":7,"weekly":4,"monthly":6},"windowStart":1,"windowEnd":5,"readParts":12}`), &old); err != nil {
		t.Fatal(err)
	}

	if old != DefaultSettings() {
		t.Errorf("old settings decoded as %+v", old)
	}

	var set Settings
	if err := json.Unmarshal([]byte(`{"version":1,"retention":{"hourly":48,"daily":7,"yearly":2},"intervalHours":1,"windowStart":1,"windowEnd":5}`), &set); err != nil {
		t.Fatal(err)
	}

	if set.IntervalHours != 1 || set.Retention.Hourly != 48 || set.Retention.Yearly != 2 {
		t.Errorf("decoded %+v", set)
	}

	for _, raw := range []string{
		`{"version":1,"retention":{"daily":7},"every":"1h"}`,
		`{"version":1,"retention":{"daily":7,"minutely":5}}`,
		`{"version":1,"intervalHours":"1"}`,
	} {
		var s Settings
		if err := json.Unmarshal([]byte(raw), &s); err == nil {
			t.Errorf("%s decoded as %+v", raw, s)
		}
	}
}

func TestBackupOverdue(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	done := func(ago time.Duration) agentapi.BackupStatus { return agentapi.BackupStatus{Finished: now.Add(-ago)} }

	cases := []struct {
		name     string
		interval int
		ready    bool
		st       agentapi.BackupStatus
		want     bool
	}{
		{"daily within 26 hours", 24, true, done(26 * time.Hour), false},
		{"daily after 26 hours", 24, true, done(26*time.Hour + time.Minute), true},
		{"hourly within 3 hours", 1, true, done(3 * time.Hour), false},
		{"hourly after 3 hours", 1, true, done(3*time.Hour + time.Second), true},
		{"every 6 hours after 8", 6, true, done(9 * time.Hour), true},
		{"not set up", 1, false, done(48 * time.Hour), false},
		{"running", 1, true, agentapi.BackupStatus{Running: true, Finished: now.Add(-48 * time.Hour)}, false},
		{"never finished", 1, true, agentapi.BackupStatus{}, false},
		{"invalid interval counts as daily", 0, true, done(25 * time.Hour), false},
	}

	for _, c := range cases {
		s := DefaultSettings()
		s.IntervalHours = c.interval

		if got := agentapi.BackupOverdue(s, c.ready, c.st, now); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestTimerWindow(t *testing.T) {
	cases := map[int]string{
		24: "OnCalendar=*-*-* 01:00:00\nRandomizedDelaySec=4h\n",
		12: "OnCalendar=*-*-* 00/12:00:00\nRandomizedDelaySec=60min\n",
		6:  "OnCalendar=*-*-* 00/6:00:00\nRandomizedDelaySec=60min\n",
		1:  "OnCalendar=*-*-* 00/1:00:00\nRandomizedDelaySec=60min\n",
	}

	for h, want := range cases {
		s := DefaultSettings()
		s.IntervalHours = h

		got := TimerWindow(s)
		if !strings.HasPrefix(got, "# Written by the Hostashi agent") || !strings.Contains(got, "[Timer]\nOnCalendar=\n"+want) {
			t.Errorf("interval %d: %q", h, got)
		}
	}

	// The window only shapes the daily run.
	s := DefaultSettings()
	s.IntervalHours, s.WindowStart, s.WindowEnd = 2, 20, 22

	if got := TimerWindow(s); strings.Contains(got, "20:00") || strings.Contains(got, "RandomizedDelaySec=2h") {
		t.Errorf("window applied to an interval: %q", got)
	}
}

func TestResticAcceptsEveryValidTarget(t *testing.T) {
	for _, tg := range []Target{
		*storageBox(),
		{User: "u1", Host: "backup.example.org", Port: 65535, Path: "a"},
		{User: "0", Host: "a.de", Port: 1, Path: "a_b/c-d/e.f"},
		{User: "backup_web3", Host: "nas.example.lan", Port: 22, Path: "/srv/backups/web3"},
		{User: "First.Last", Host: "192.0.2.10", Port: 2222, Path: "hostashi"},
		{User: "u1", Host: "2001:db8::10", Port: 22, Path: "/srv/restic"},
	} {
		s := DefaultSettings()
		s.Target = &tg

		r, err := NewRepo(s, ConfigDir, CacheDir, "web3")
		if err != nil {
			t.Fatal(err)
		}

		r.Binary = "/nonexistent/restic"

		// A validation error would be ErrInvalid; anything else means the
		// arguments passed and only the binary is missing.
		if err := r.Unlock(context.Background()); errors.Is(err, resticctl.ErrInvalid) {
			t.Errorf("%+v: resticctl refused %q: %v", tg, tg.Repository(), err)
		}
	}
}

func TestPlan(t *testing.T) {
	items := Plan([]Site{
		{Name: "shop", Home: "/home/shop", Databases: []string{"shop_wp", "shop_b"}},
		{Name: "blog", Home: "/home/blog"},
	}, PanelPaths)

	got := []string{}
	for _, it := range items {
		got = append(got, strings.Join(it.Tags(), ","))
	}

	want := []string{
		"site:blog,kind:files",
		"site:shop,kind:files",
		"site:shop,kind:db,db:shop_b",
		"site:shop,kind:db,db:shop_wp",
		"kind:panel",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan\n got %v\nwant %v", got, want)
	}

	if !reflect.DeepEqual(items[0].Paths, []string{"/home/blog"}) {
		t.Errorf("paths %v", items[0].Paths)
	}
}

type fakeRepo struct {
	calls     []string
	failItem  string // tag string of an item to fail
	partial   string
	failOpen  error
	failCheck error
	snaps     []resticctl.Snapshot
}

func (f *fakeRepo) Init(context.Context) error {
	f.calls = append(f.calls, "init")
	f.failOpen = nil

	return nil
}

func (f *fakeRepo) Snapshots(context.Context, []string) ([]resticctl.Snapshot, error) {
	f.calls = append(f.calls, "snapshots")

	return f.snaps, nil
}

func (f *fakeRepo) Unlock(context.Context) error {
	f.calls = append(f.calls, "unlock")
	return f.failOpen
}

func (f *fakeRepo) save(tags []string, what string) (resticctl.Summary, error) {
	key := strings.Join(tags, ",")
	f.calls = append(f.calls, what+" "+key)

	switch key {
	case f.failItem:
		return resticctl.Summary{}, errors.New("upload failed")
	case f.partial:
		return resticctl.Summary{SnapshotID: strings.Repeat("b", 64)}, resticctl.ErrPartial
	}

	return resticctl.Summary{SnapshotID: strings.Repeat("a", 64), DataAdded: 10, TotalFiles: 2, TotalBytes: 30}, nil
}

func (f *fakeRepo) Backup(_ context.Context, tags []string, paths ...string) (resticctl.Summary, error) {
	return f.save(tags, "backup "+strings.Join(paths, "+"))
}

func (f *fakeRepo) BackupDatabase(_ context.Context, tags []string, db string) (resticctl.Summary, error) {
	return f.save(tags, "dump "+db)
}

func (f *fakeRepo) BackupRemoteDatabase(_ context.Context, tags []string, db string, cfg dbserver.Config) (resticctl.Summary, error) {
	return f.save(tags, "remote-dump "+cfg.Host+" "+db)
}

func (f *fakeRepo) Forget(_ context.Context, k resticctl.Keep) error {
	f.calls = append(f.calls, "forget")

	if k != (resticctl.Keep{Daily: 7, Weekly: 4, Monthly: 6}) {
		return errors.New("wrong policy")
	}

	return nil
}

func (f *fakeRepo) Prune(context.Context) error {
	f.calls = append(f.calls, "prune")
	return nil
}

func (f *fakeRepo) Check(_ context.Context, part, total int) error {
	f.calls = append(f.calls, "check "+strconv.Itoa(part)+"/"+strconv.Itoa(total))
	return f.failCheck
}

type dirInfo struct{ fs.FileInfo }

func (dirInfo) IsDir() bool { return true }

func (dirInfo) Mode() fs.FileMode { return fs.ModeDir | 0o755 }

type fileInfo struct{ fs.FileInfo }

func (fileInfo) IsDir() bool { return false }

func (fileInfo) Mode() fs.FileMode { return 0o644 }

type linkInfo struct{ fs.FileInfo }

func (linkInfo) IsDir() bool { return false }

func (linkInfo) Mode() fs.FileMode { return fs.ModeSymlink | 0o777 }

// fakeFS knows directories and plain files; everything else is missing.
func fakeFS(dirs, files []string, links ...string) func(string) (os.FileInfo, error) {
	return func(p string) (os.FileInfo, error) {
		for _, l := range links {
			if l == p {
				return linkInfo{}, nil
			}
		}

		for _, d := range dirs {
			if d == p {
				return dirInfo{}, nil
			}
		}

		for _, f := range files {
			if f == p {
				return fileInfo{}, nil
			}
		}

		return nil, os.ErrNotExist
	}
}

func settingsWithTarget() Settings {
	s := DefaultSettings()
	s.Target = storageBox()

	return s
}

func TestRunAllGood(t *testing.T) {
	now := time.Date(2026, 11, 19, 2, 30, 0, 0, time.UTC)
	repo := &fakeRepo{}
	r := &Runner{
		Repo: repo, Settings: settingsWithTarget(), Now: func() time.Time { return now },
		Lstat: fakeFS([]string{"/home/blog", "/home/shop", "/var/lib/hostashi-agent"}, []string{"/etc/hostashi/hostashi.env"}),
	}

	st := r.Run(context.Background(), Plan([]Site{
		{Name: "blog", Home: "/home/blog"},
		{Name: "shop", Home: "/home/shop", Databases: []string{"shop"}},
	}, PanelPaths))

	if !st.OK || st.Error != "" || len(st.Items) != 4 || !st.MaintenanceOK {
		t.Fatalf("status %+v", st)
	}

	want := []string{
		"unlock",
		"backup /home/blog site:blog,kind:files",
		"backup /home/shop site:shop,kind:files",
		"dump shop site:shop,kind:db,db:shop",
		// /var/lib/hostashi/backup does not exist yet: left out.
		"backup /etc/hostashi/hostashi.env+/var/lib/hostashi-agent kind:panel",
		"forget",
		"prune",
		"check 1/12",
		"snapshots",
	}

	if !reflect.DeepEqual(repo.calls, want) {
		t.Errorf("calls\n got %q\nwant %q", repo.calls, want)
	}

	if st.LastPrune != now || st.LastReadCheck != now || st.LastCheck != now || st.ReadPart != 1 {
		t.Errorf("maintenance times %+v", st)
	}

	// A day later nothing is due; a week later prune and the structure
	// check; a month later the next data part.
	for _, c := range []struct {
		after time.Duration
		want  []string
	}{
		{24 * time.Hour, []string{"snapshots"}},
		{7 * 24 * time.Hour, []string{"prune", "check 0/0", "snapshots"}},
		{30 * 24 * time.Hour, []string{"prune", "check 2/12", "snapshots"}},
	} {
		repo2 := &fakeRepo{}
		later := now.Add(c.after)
		r2 := &Runner{Repo: repo2, Settings: settingsWithTarget(), Prev: st, Now: func() time.Time { return later }, Lstat: r.Lstat}
		r2.Run(context.Background(), nil)

		got := repo2.calls[2:] // after unlock and forget
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("after %v: got %q, want %q", c.after, got, c.want)
		}
	}
}

func TestRunFailures(t *testing.T) {
	repo := &fakeRepo{failItem: "site:shop,kind:db,db:shop", partial: "site:blog,kind:files", failCheck: errors.New("pack broken")}
	r := &Runner{
		Repo: repo, Settings: settingsWithTarget(),
		// /home/gone is missing, /home/link is a symlink (not a directory).
		Lstat: fakeFS([]string{"/home/blog", "/home/shop"}, nil, "/home/link", "/var/lib/hostashi-agent"),
	}

	st := r.Run(context.Background(), Plan([]Site{
		{Name: "blog", Home: "/home/blog"},
		{Name: "gone", Home: "/home/gone"},
		{Name: "link", Home: "/home/link"},
		{Name: "shop", Home: "/home/shop", Databases: []string{"shop"}},
	}, PanelPaths))

	if st.OK {
		t.Fatal("run with failed items reported ok")
	}

	byKey := map[string]ItemResult{}
	for _, it := range st.Items {
		byKey[it.Site+"/"+it.Kind] = it
	}

	if it := byKey["blog/files"]; !it.Partial || it.Error != "" || it.SnapshotID == "" {
		t.Errorf("partial item %+v", it)
	}

	if it := byKey["gone/files"]; !strings.Contains(it.Error, "/home/gone") {
		t.Errorf("missing home %+v", it)
	}

	if it := byKey["link/files"]; !strings.Contains(it.Error, "not a directory") {
		t.Errorf("symlinked home %+v", it)
	}

	if it := byKey["shop/db"]; it.Error != "upload failed" {
		t.Errorf("failed dump %+v", it)
	}

	if it := byKey["/panel"]; !strings.Contains(it.Error, "no panel state") {
		t.Errorf("panel state behind a symlink was used: %+v", it)
	}

	if it := byKey["shop/files"]; it.Error != "" || it.SnapshotID == "" {
		t.Errorf("good item after a failed one %+v", it)
	}

	for _, c := range repo.calls {
		if strings.Contains(c, "/home/gone") || strings.Contains(c, "/home/link") {
			t.Errorf("restic called for a missing home: %q", c)
		}
	}

	if st.MaintenanceOK || !strings.Contains(st.Maintenance, "pack broken") || !st.LastReadCheck.IsZero() {
		t.Errorf("failed check not recorded: %+v", st)
	}
}

func TestRunWithoutTargetOrRepository(t *testing.T) {
	st := (&Runner{Settings: DefaultSettings()}).Run(context.Background(), nil)
	if st.OK || !strings.Contains(st.Error, "no backup target") {
		t.Errorf("no target: %+v", st)
	}

	// The first run creates the repository.
	repo := &fakeRepo{failOpen: resticctl.ErrNoRepository}
	st = (&Runner{Repo: repo, Settings: settingsWithTarget(), Lstat: fakeFS([]string{"/home/a"}, nil)}).Run(context.Background(), Plan([]Site{{Name: "a", Home: "/home/a"}}, nil))

	if !st.OK || !st.Initialized || repo.calls[1] != "init" {
		t.Errorf("first run: %+v, calls %v", st, repo.calls)
	}

	// A wrong password never leads to a new repository.
	repo = &fakeRepo{failOpen: resticctl.ErrWrongPassword}
	st = (&Runner{Repo: repo, Settings: settingsWithTarget()}).Run(context.Background(), Plan([]Site{{Name: "a", Home: "/home/a"}}, nil))

	if st.OK || !strings.Contains(st.Error, "wrong repository password") || len(repo.calls) != 1 {
		t.Errorf("wrong password: %+v, calls %v", st, repo.calls)
	}
}

func TestEntries(t *testing.T) {
	t1 := time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)
	got := entries([]resticctl.Snapshot{
		{ID: "a", Time: t1, Tags: []string{"site:blog", "kind:files"}, Summary: &resticctl.SnapshotSummary{TotalFilesProcessed: 7, TotalBytesProcessed: 4096}},
		{ID: "b", Time: t1.Add(time.Hour), Tags: []string{"kind:db", "site:blog", "db:wp"}},
		{ID: "c", Time: t1, Tags: []string{"kind:panel"}},
		{ID: "d", Time: t1, Tags: []string{"manual"}},
		{ID: "e", Time: t1, Tags: []string{"kind:db", "site:blog"}},
	})

	want := []SnapshotEntry{
		{ID: "b", Time: t1.Add(time.Hour), Kind: KindDB, Site: "blog", Database: "wp"},
		{ID: "a", Time: t1, Kind: KindFiles, Site: "blog"},
		{ID: "c", Time: t1, Kind: KindPanel},
	}

	want[1].Bytes, want[1].Files = 4096, 7

	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("entries %+v", got)
	}
}

func TestRunStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	repo := &fakeRepo{}
	st := (&Runner{Repo: repo, Settings: settingsWithTarget(), Lstat: fakeFS([]string{"/home/a"}, nil)}).Run(ctx, Plan([]Site{{Name: "a", Home: "/home/a"}}, nil))

	if st.OK || len(st.Items) != 0 {
		t.Errorf("canceled run: %+v", st)
	}
}

func TestSettingsAndStatusFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	uid := uint32(os.Getuid())

	s, err := ReadSettings(dir, uid)
	if err != nil || s.Target != nil || s.Retention.Daily != 7 {
		t.Fatalf("missing settings: %+v, %v", s, err)
	}

	s.Target = storageBox()
	if err := WriteSettings(dir, s); err != nil {
		t.Fatal(err)
	}

	back, err := ReadSettings(dir, uid)
	if err != nil || !reflect.DeepEqual(back, s) {
		t.Fatalf("settings round trip: %+v, %v", back, err)
	}

	bad := s
	bad.Retention.Daily = 0

	if err := WriteSettings(dir, bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid settings written: %v", err)
	}

	if err := os.WriteFile(dir+"/"+SettingsFile, []byte(`{"version":1,"extra":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadSettings(dir, uid); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown field accepted: %v", err)
	}

	st, err := ReadStatus(dir, uid)
	if err != nil || st.Version != 0 {
		t.Fatalf("missing status: %+v, %v", st, err)
	}

	now := time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)
	want := Status{Version: StatusVersion, Started: now, Finished: now, OK: true, Items: []ItemResult{{Kind: KindPanel, SnapshotID: "x"}}, LastPrune: now, MaintenanceOK: true}

	if err := WriteStatus(dir, want); err != nil {
		t.Fatal(err)
	}

	got, err := ReadStatus(dir, uid)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("status round trip:\n got %+v\nwant %+v (%v)", got, want, err)
	}
}
