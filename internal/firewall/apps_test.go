// SPDX-License-Identifier: AGPL-3.0-or-later

package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderApps(t *testing.T) {
	got, err := RenderApps([]AppOwner{{Port: 20002, UID: 1002}, {Port: 20001, UID: 1001}})
	if err != nil {
		t.Fatal(err)
	}

	if want := "add element inet hostashi app_owners { 20001 . 1001, 20002 . 1002 }\n"; !strings.HasSuffix(got, want) {
		t.Errorf("got %q", got)
	}

	if got, err := RenderApps(nil); err != nil || strings.Contains(got, "add element") {
		t.Errorf("empty: %q %v", got, err)
	}

	for name, o := range map[string][]AppOwner{
		"port":  {{Port: 80, UID: 1001}},
		"twice": {{Port: 20001, UID: 1001}, {Port: 20001, UID: 1002}},
		"root":  {{Port: 20001, UID: 0}},
	} {
		if _, err := RenderApps(o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSetApps(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.nft")
	reloads := 0
	fail := false
	m := &Manager{Dir: dir, Base: base, Reload: func(context.Context) error {
		reloads++

		if fail {
			return errors.New("nft: syntax error")
		}

		return nil
	}}
	ctx := context.Background()
	one := []AppOwner{{Port: 20001, UID: 1001}}

	// Off: the file is written, nothing reloads.
	if err := m.SetApps(ctx, one); !errors.Is(err, ErrDisabled) || reloads != 0 {
		t.Fatalf("off: %v, %d reloads", err, reloads)
	}

	if err := os.WriteFile(base, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.SetApps(ctx, one); err != nil || reloads != 0 {
		t.Fatalf("unchanged: %v, %d reloads", err, reloads)
	}

	two := []AppOwner{one[0], {Port: 20002, UID: 1002}}
	if err := m.SetApps(ctx, two); err != nil || reloads != 1 {
		t.Fatalf("changed: %v, %d reloads", err, reloads)
	}

	fail = true

	if err := m.SetApps(ctx, one); err == nil {
		t.Fatal("a failed reload went through")
	}

	b, _ := os.ReadFile(filepath.Join(dir, AppsFile))
	if !strings.Contains(string(b), "20002 . 1002") {
		t.Errorf("the old file was not put back: %s", b)
	}
}
