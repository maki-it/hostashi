// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
)

func TestUserPreferences(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()

	u, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	p, err := s.UserPreferences(ctx, u.ID)
	if err != nil || p != (Preferences{ColorScheme: "system", Theme: "default", Language: "auto"}) {
		t.Fatalf("default %+v %v", p, err)
	}

	if err := s.SetUserPreferences(ctx, u.ID, Preferences{ColorScheme: "dark", Theme: "default", Language: "auto"}); err != nil {
		t.Fatal(err)
	}

	if p, _ := s.UserPreferences(ctx, u.ID); p.ColorScheme != "dark" {
		t.Fatalf("stored %+v", p)
	}

	if err := s.SetUserPreferences(ctx, u.ID, Preferences{ColorScheme: "blue", Theme: "default", Language: "auto"}); err == nil {
		t.Fatal("unknown color scheme accepted")
	}

	if err := s.SetUserPreferences(ctx, u.ID, Preferences{ColorScheme: "light", Theme: "konsole", Language: "auto"}); err == nil {
		t.Fatal("unknown theme accepted")
	}

	if err := s.SetUserPreferences(ctx, u.ID, Preferences{ColorScheme: "light", Theme: "default", Language: "de"}); err != nil {
		t.Fatal(err)
	}

	if p, _ := s.UserPreferences(ctx, u.ID); p.Language != "de" {
		t.Fatalf("language %+v", p)
	}

	if err := s.SetUserPreferences(ctx, u.ID, Preferences{ColorScheme: "light", Theme: "default", Language: "fr"}); err == nil {
		t.Fatal("unknown language accepted")
	}

	if err := s.SetUserPreferences(ctx, u.ID+1, Preferences{ColorScheme: "light", Theme: "default", Language: "auto"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}

	if _, err := s.UserPreferences(ctx, u.ID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
}
