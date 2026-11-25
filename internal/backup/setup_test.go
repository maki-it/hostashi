// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

type fakeUnits struct{ calls []string }

func (f *fakeUnits) StartRun(context.Context) error { f.calls = append(f.calls, "start"); return nil }

func (f *fakeUnits) Reload(context.Context) error { f.calls = append(f.calls, "reload"); return nil }

func (f *fakeUnits) EnableTimer(context.Context) error {
	f.calls = append(f.calls, "enable")
	return nil
}

func (f *fakeUnits) StartRestore(_ context.Context, id string) error {
	f.calls = append(f.calls, "restore "+id)
	return nil
}

func (f *fakeUnits) DisableTimer(context.Context) error {
	f.calls = append(f.calls, "disable")
	return nil
}

func newAgent(t *testing.T) (*Agent, *fakeUnits) {
	t.Helper()

	dir := t.TempDir()
	status := filepath.Join(dir, "status")

	if err := os.Mkdir(status, 0o700); err != nil {
		t.Fatal(err)
	}

	u := &fakeUnits{}

	return &Agent{
		ConfigDir: filepath.Join(dir, "config"), StatusDir: status,
		DropIn: filepath.Join(dir, "timer.d", "window.conf"), Units: u, Host: "web3.example.com",
	}, u
}

func authorized(t *testing.T, pub any) string {
	t.Helper()

	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func TestAgentSetup(t *testing.T) {
	ctx := context.Background()
	a, units := newAgent(t)

	info, err := a.Get()
	if err != nil || info.Settings.Target != nil || info.Ready || info.PublicKey != "" {
		t.Fatalf("fresh: %+v, %v", info, err)
	}

	if err := a.StartRun(ctx); !errors.Is(err, ErrNotReady) {
		t.Fatalf("run without target: %v", err)
	}

	s := DefaultSettings()
	s.Target = storageBox()

	info, err = a.Set(ctx, s)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(info.PublicKey, "ssh-ed25519 ") || !strings.HasSuffix(info.PublicKey, " hostashi-backup@web3.example.com") || info.Ready {
		t.Fatalf("after set: %+v", info)
	}

	pw, err := os.ReadFile(filepath.Join(a.ConfigDir, PasswordFile))
	if err != nil || len(strings.TrimSpace(string(pw))) != 43 {
		t.Fatalf("password %q, %v", pw, err)
	}

	for _, f := range []string{PasswordFile, SSHKeyFile, SettingsFile} {
		st, err := os.Stat(filepath.Join(a.ConfigDir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, st.Mode(), err)
		}
	}

	if got := strings.Join(units.calls, ","); got != "reload,disable" {
		t.Errorf("units after set: %s", got)
	}

	// A second set keeps the password and the key.
	firstKey := info.PublicKey
	s.Retention.Daily = 14

	if info, err = a.Set(ctx, s); err != nil || info.PublicKey != firstKey || info.Settings.Retention.Daily != 14 {
		t.Fatalf("second set: %+v, %v", info, err)
	}

	if pw2, _ := os.ReadFile(filepath.Join(a.ConfigDir, PasswordFile)); string(pw2) != string(pw) {
		t.Fatal("password replaced")
	}

	// Pin the host key: ready, timer on.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	units.calls = nil

	info, err = a.SetHostKey(ctx, authorized(t, pub))
	if err != nil || !info.Ready || !strings.HasPrefix(info.HostKey, "SHA256:") {
		t.Fatalf("host key: %+v, %v", info, err)
	}

	if got := strings.Join(units.calls, ","); got != "reload,enable" {
		t.Errorf("units after host key: %s", got)
	}

	kh, _ := os.ReadFile(filepath.Join(a.ConfigDir, KnownHostsFile))
	if !strings.HasPrefix(string(kh), "[u123456.your-storagebox.de]:23 ssh-ed25519 ") {
		t.Errorf("known_hosts %q", kh)
	}

	units.calls = nil

	if err := a.StartRun(ctx); err != nil || strings.Join(units.calls, ",") != "start" {
		t.Errorf("start run: %v %v", err, units.calls)
	}

	// The same host with other settings keeps the key; another port drops it.
	s.ReadParts = 6
	if info, _ = a.Set(ctx, s); !info.Ready {
		t.Error("host key dropped by a retention change")
	}

	s.Target.Port = 22
	if info, _ = a.Set(ctx, s); info.Ready || info.HostKey != "" {
		t.Errorf("host key kept for another port: %+v", info)
	}

	if _, err := os.Stat(filepath.Join(a.ConfigDir, KnownHostsFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("known_hosts not removed: %v", err)
	}

	drop, _ := os.ReadFile(a.DropIn)
	if string(drop) != TimerWindow(s) || !strings.Contains(string(drop), "OnCalendar=*-*-* 01:00:00\nRandomizedDelaySec=4h\n") {
		t.Errorf("drop-in %q", drop)
	}

	if _, err := a.Set(ctx, Settings{Version: 1}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid settings accepted: %v", err)
	}
}

func TestSetHostKeyWithoutTarget(t *testing.T) {
	a, _ := newAgent(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	if _, err := a.SetHostKey(context.Background(), authorized(t, pub)); !errors.Is(err, ErrNotReady) {
		t.Errorf("got %v", err)
	}
}

func TestParseHostKey(t *testing.T) {
	ed, _, _ := ed25519.GenerateKey(rand.Reader)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rsaBig, _ := rsa.GenerateKey(rand.Reader, 3072)
	rsaSmall, _ := rsa.GenerateKey(rand.Reader, 2048)

	for name, line := range map[string]string{
		"ed25519":      authorized(t, ed),
		"ecdsa":        authorized(t, &ec.PublicKey),
		"rsa 3072":     authorized(t, &rsaBig.PublicKey),
		"with comment": authorized(t, ed) + " root@storagebox",
	} {
		if _, err := ParseHostKey(line); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}

	for name, line := range map[string]string{
		"rsa 2048":    authorized(t, &rsaSmall.PublicKey),
		"options":     `command="x" ` + authorized(t, ed),
		"two keys":    authorized(t, ed) + "\n" + authorized(t, ed),
		"garbage":     "ssh-ed25519 AAAA",
		"known_hosts": "[host]:23 " + authorized(t, ed),
		"empty":       "",
	} {
		if _, err := ParseHostKey(line); !errors.Is(err, ErrBadHostKey) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestRunningLock(t *testing.T) {
	dir := t.TempDir()

	if Running(dir) {
		t.Fatal("running without a lock file")
	}

	unlock, err := lock(filepath.Join(dir, "run.lock"))
	if err != nil {
		t.Fatal(err)
	}

	if !Running(dir) {
		t.Error("lock held, but not running")
	}

	if _, err := lock(filepath.Join(dir, "run.lock")); !errors.Is(err, ErrBusy) {
		t.Errorf("second lock: %v", err)
	}

	unlock()

	if Running(dir) {
		t.Error("running after unlock")
	}
}

func TestAgentStatusAndSnapshots(t *testing.T) {
	a, _ := newAgent(t)

	st, err := a.Status()
	if err != nil || st.OK || st.Running || len(st.Items) != 0 {
		t.Fatalf("no run yet: %+v, %v", st, err)
	}

	if err := WriteSnapshots(a.StatusDir, []SnapshotEntry{
		{ID: "a", Kind: KindFiles, Site: "blog"},
		{ID: "b", Kind: KindDB, Site: "blog", Database: "wp"},
		{ID: "c", Kind: KindFiles, Site: "shop"},
		{ID: "d", Kind: KindPanel},
	}); err != nil {
		t.Fatal(err)
	}

	blog, err := a.Snapshots("blog")
	if err != nil || len(blog) != 2 || blog[1].Database != "wp" {
		t.Errorf("blog: %+v, %v", blog, err)
	}

	if panel, _ := a.Snapshots(""); len(panel) != 1 || panel[0].ID != "d" {
		t.Errorf("panel: %+v", panel)
	}
}

// A pinned key must be found again for every kind of host, or the target
// never becomes ready: DNS names, IP addresses, IPv6 on port 22 and others.
func TestHostKeyPinnedForGenericTargets(t *testing.T) {
	ctx := context.Background()

	for _, tg := range []Target{
		*storageBox(),
		{User: "backup_web3", Host: "backup.example.org", Port: 22, Path: "/srv/backups/web3"},
		{User: "u1", Host: "192.0.2.10", Port: 2222, Path: "hostashi"},
		{User: "u1", Host: "2001:db8::10", Port: 22, Path: "hostashi"},
		{User: "u1", Host: "2001:db8::10", Port: 23, Path: "hostashi"},
	} {
		a, _ := newAgent(t)
		s := DefaultSettings()
		s.Target = &tg

		if _, err := a.Set(ctx, s); err != nil {
			t.Fatalf("%s: set: %v", tg.Host, err)
		}

		pub, _, _ := ed25519.GenerateKey(rand.Reader)

		if _, err := a.SetHostKey(ctx, authorized(t, pub)); err != nil {
			t.Fatalf("%s: host key: %v", tg.Host, err)
		}

		info, err := a.Get()
		if err != nil || !info.Ready {
			t.Errorf("%s port %d: not ready after pinning: %+v, %v", tg.Host, tg.Port, info, err)
		}
	}
}
