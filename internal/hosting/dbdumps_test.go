// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestDatabaseDumps(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"shop_wp", "shop_two"} {
		if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: name}); err != nil {
			t.Fatal(err)
		}
	}

	imp, err := s.ImportDatabase(ctx, 1, "shop_wp", "dumps/old.sql.gz")
	if err != nil || imp.Kind != "import" || imp.Database != "shop_wp" || imp.State != agentapi.JobRunning {
		t.Fatalf("import %+v %v", imp, err)
	}

	exp, err := s.ExportDatabase(ctx, 1, "shop_two", "backup.sql")
	if err != nil || exp.Kind != "export" || exp.ID == imp.ID {
		t.Fatalf("export %+v %v", exp, err)
	}

	if j, err := s.DatabaseJob(ctx, "shop_wp", imp.ID); err != nil || j.ID != imp.ID {
		t.Fatalf("job %+v %v", j, err)
	}

	// A job is only found through its own database.
	if _, err := s.DatabaseJob(ctx, "shop_two", imp.ID); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("job of another database: %v", err)
	}

	for _, id := range []string{"00000000000000ff", "../x", ""} {
		if _, err := s.DatabaseJob(ctx, "shop_wp", id); !errors.Is(err, ErrJobNotFound) {
			t.Errorf("job %q: %v", id, err)
		}
	}

	if _, err := s.DatabaseJob(ctx, "nope", imp.ID); !errors.Is(err, ErrDatabaseNotFound) {
		t.Fatalf("unknown database: %v", err)
	}

	// The path is checked before the agent is asked.
	for _, p := range []string{"", "../etc/passwd.sql", "/home/shop/x.sql", "x.txt", "a/../b.sql", ".hidden/../x.sql"} {
		if _, err := s.ImportDatabase(ctx, 1, "shop_wp", p); !errors.Is(err, ErrInvalid) {
			t.Errorf("path %q: %v", p, err)
		}
	}

	if _, err := s.ExportDatabase(ctx, 1, "nope", "x.sql"); !errors.Is(err, ErrDatabaseNotFound) {
		t.Fatalf("unknown database: %v", err)
	}

	if len(ag.jobs) != 2 {
		t.Fatalf("agent got %d jobs, want 2", len(ag.jobs))
	}
}
