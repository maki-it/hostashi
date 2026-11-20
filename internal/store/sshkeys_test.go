// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"
)

func TestSSHKeys(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	a, err := s.InsertSSHKey(ctx, SSHKey{SiteID: site.ID, Label: "laptop", Access: "sftp", Public: "ssh-ed25519 AAAA", Fingerprint: "SHA256:a"}, 2, now)
	if err != nil || a.ID == 0 || a.Access != "sftp" || !a.CreatedAt.Equal(now) {
		t.Fatalf("insert %+v %v", a, err)
	}

	if _, err := s.InsertSSHKey(ctx, SSHKey{SiteID: site.ID, Label: "again", Access: "shell", Public: "ssh-ed25519 AAAA", Fingerprint: "SHA256:a"}, 2, now); !errors.Is(err, ErrSSHKeyExists) {
		t.Fatalf("duplicate: %v", err)
	}

	if _, err := s.InsertSSHKey(ctx, SSHKey{SiteID: site.ID, Label: "x", Access: "root", Public: "ssh-ed25519 CCCC", Fingerprint: "SHA256:c"}, 2, now); err == nil {
		t.Fatal("access outside the CHECK accepted")
	}

	b, err := s.InsertSSHKey(ctx, SSHKey{SiteID: site.ID, Label: "ci", Access: "shell", Public: "ssh-ed25519 BBBB", Fingerprint: "SHA256:b"}, 2, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.InsertSSHKey(ctx, SSHKey{SiteID: site.ID, Label: "x", Access: "sftp", Public: "ssh-ed25519 DDDD", Fingerprint: "SHA256:d"}, 2, now); !errors.Is(err, ErrTooMany) {
		t.Fatalf("limit: %v", err)
	}

	changed := a
	changed.Label, changed.Access = "desktop", "shell"

	old, err := s.UpdateSSHKey(ctx, changed, now.Add(time.Minute))
	if err != nil || old.Label != "laptop" || old.Access != "sftp" {
		t.Fatalf("update %+v %v", old, err)
	}

	if got, _ := s.SSHKey(ctx, site.ID, a.ID); got.Label != "desktop" || got.Access != "shell" || got.Public != "ssh-ed25519 AAAA" {
		t.Fatalf("after update %+v", got)
	}

	if err := s.RestoreSSHKey(ctx, old); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SSHKey(ctx, site.ID, a.ID); got.Label != "laptop" || got.Access != "sftp" {
		t.Fatalf("restored %+v", got)
	}

	gone, err := s.DeleteSSHKey(ctx, site.ID, b.ID)
	if err != nil || gone.ID != b.ID {
		t.Fatalf("delete %+v %v", gone, err)
	}

	if _, err := s.SSHKey(ctx, site.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted key: %v", err)
	}

	if err := s.RestoreSSHKey(ctx, gone); err != nil {
		t.Fatal(err)
	}

	keys, err := s.SSHKeys(ctx, site.ID)
	if err != nil || len(keys) != 2 || keys[0].ID != a.ID || keys[1].ID != b.ID {
		t.Fatalf("list %+v %v", keys, err)
	}

	if _, err := s.UpdateSSHKey(ctx, SSHKey{ID: 99, SiteID: site.ID, Label: "x", Access: "sftp"}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: %v", err)
	}
}
