// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestParseFstab(t *testing.T) {
	const fstab = `# /etc/fstab
UUID=981550e6-f35f-430a-9ab5-f9dc658b67b6 / ext4 errors=remount-ro 0 1
LABEL=web /home ext4 defaults,discard,noatime 0 2
# LABEL=old /old ext4 defaults 0 2
/dev/sdb /srv/vol1 ext4 nosuid,nodev,nofail,prjquota 0 2
PARTUUID=abc none swap sw,noauto 0 0
tmpfs /tmp tmpfs defaults 0 0
proc /proc proc hidepid=invisible 0 0
server:/export /mnt/nfs nfs defaults 0 0
/dev/sdc1 /srv/bind none bind 0 0
LABEL=my\040disk /srv/my\040disk ext4 defaults 0 2
LABEL= /empty ext4 defaults 0 2
short
`

	got := ParseFstab(strings.NewReader(fstab))
	want := []FstabEntry{
		{Line: 2, Source: "UUID=981550e6-f35f-430a-9ab5-f9dc658b67b6", Target: "/", Wait: true},
		{Line: 3, Source: "LABEL=web", Target: "/home", Wait: true},
		{Line: 5, Source: "/dev/sdb", Target: "/srv/vol1", Wait: false},
		{Line: 6, Source: "PARTUUID=abc", Target: "none", Wait: false},
		{Line: 11, Source: `LABEL=my\040disk`, Target: "/srv/my disk", Wait: true},
	}

	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDevicePaths(t *testing.T) {
	for src, want := range map[string]string{
		"/dev/sdb1":            "/d/sdb1",
		"/dev/mapper/vg-root":  "/d/mapper/vg-root",
		"LABEL=web":            "/d/disk/by-label/web",
		`LABEL=my\040disk`:     `/d/disk/by-label/my\x20disk`,
		"LABEL=a/b":            `/d/disk/by-label/a\x2fb`,
		`LABEL="quoted"`:       "/d/disk/by-label/quoted",
		"UUID=ABCD-1234":       "/d/disk/by-uuid/ABCD-1234",
		"PARTUUID=0a1b":        "/d/disk/by-partuuid/0a1b",
		"PARTLABEL=EFI system": `/d/disk/by-partlabel/EFI\x20system`,
	} {
		if got := devicePaths("/d", src); got[0] != want {
			t.Errorf("%s: %v, want %s first", src, got, want)
		}
	}

	if got := devicePaths("/d", "UUID=ABCD-1234"); len(got) != 2 || got[1] != "/d/disk/by-uuid/abcd-1234" {
		t.Errorf("upper-case UUID: %v", got)
	}
}

// fakeDev builds a /dev with the given links (files are enough for Stat).
func fakeDev(t *testing.T, names ...string) string {
	t.Helper()

	dev := t.TempDir()

	for _, n := range names {
		p := filepath.Join(dev, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dev
}

// TestFstabDevices: the incident on a server where a volume labelled web
// was formatted again (now without a label), but /etc/fstab still mounted
// LABEL=web on /home without nofail, so every login waited 90 seconds.
func TestFstabDevices(t *testing.T) {
	dev := fakeDev(t, "sda1", "sdb", "disk/by-uuid/981550e6-f35f-430a-9ab5-f9dc658b67b6", `disk/by-label/my\x20disk`)

	const root = "UUID=981550E6-F35F-430A-9AB5-F9DC658B67B6 / ext4 defaults 0 1\n"

	cases := []struct {
		name, fstab, status, text string
	}{
		{"all present", root + "/dev/sdb /srv/vol1 ext4 nosuid,nodev,nofail 0 2\nLABEL=my\\040disk /srv/d ext4 defaults 0 2\n", agentapi.DoctorOK, "every device"},
		{"stale label without nofail", root + "LABEL=web /home ext4 defaults,discard,noatime 0 2\n", agentapi.DoctorFail, "LABEL=web for /home (line 2)"},
		{"stale with nofail", root + "/dev/sdc1 /srv/vol2 ext4 nosuid,nodev,nofail 0 2\n", agentapi.DoctorWarn, "/dev/sdc1 for /srv/vol2"},
		{"commented out", root + "# LABEL=web /home ext4 defaults 0 2\n", agentapi.DoctorOK, "every device"},
		{"fail lists all", root + "LABEL=web /home ext4 defaults 0 2\nUUID=gone none swap sw,nofail 0 0\n", agentapi.DoctorFail, "UUID=gone for none"},
	}

	for _, c := range cases {
		got := System{Fstab: write(t, c.fstab), DevDir: dev}.FstabDevices()
		if got.ID != "system.fstab" || got.Status != c.status || !strings.Contains(got.Summary, c.text) {
			t.Errorf("%s: %+v, want %s with %q", c.name, got, c.status, c.text)
		}

		if c.status != agentapi.DoctorOK && !strings.Contains(got.Hint, "daemon-reload") {
			t.Errorf("%s: hint %q", c.name, got.Hint)
		}
	}

	if got := (System{Fstab: filepath.Join(dev, "missing"), DevDir: dev}).FstabDevices(); got.Status != agentapi.DoctorOK {
		t.Errorf("no fstab: %+v", got)
	}

	if os.Getuid() != 0 {
		unreadable := write(t, root)
		if err := os.Chmod(unreadable, 0); err != nil {
			t.Fatal(err)
		}

		if got := (System{Fstab: unreadable, DevDir: dev}).FstabDevices(); got.Status != agentapi.DoctorWarn {
			t.Errorf("unreadable fstab: %+v", got)
		}
	}
}
