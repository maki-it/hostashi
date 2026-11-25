// SPDX-License-Identifier: AGPL-3.0-or-later

package notify_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/notify"
	"github.com/maki-it/hostashi/internal/store"
)

// TestEveryCategoryStorable guards against a new category that the
// notification_prefs CHECK does not allow: saving it failed with a
// constraint error and the API answered 500 (deploys, malware).
func TestEveryCategoryStorable(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = st.Close() })

	u, err := st.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range notify.Categories {
		for _, on := range []bool{true, false} {
			if err := st.SetNotificationPrefs(ctx, u.ID, map[string]bool{string(c): on}); err != nil {
				t.Fatalf("store %s=%v: %v", c, on, err)
			}

			got, err := st.NotificationPrefs(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}

			if v, ok := got[string(c)]; !ok || v != on {
				t.Fatalf("%s: got %v (present %v), want %v", c, v, ok, on)
			}
		}
	}

	// An unknown category is still refused by the database.
	if err := st.SetNotificationPrefs(ctx, u.ID, map[string]bool{"nope": true}); err == nil {
		t.Fatal("unknown category stored")
	}
}
