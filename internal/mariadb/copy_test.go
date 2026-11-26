// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"errors"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func newCopyEnv(t *testing.T) *dumpEnv {
	t.Helper()

	e := newDumpEnv(t)

	if _, err := e.m.Create(t.Context(), "shop", "shop_stage"); err != nil {
		t.Fatal(err)
	}

	e.db.stmts = nil

	return e
}

func TestCopyDatabase(t *testing.T) {
	e := newCopyEnv(t)

	j, err := e.m.StartCopy("shop_wp", "shop_stage")
	if err != nil {
		t.Fatal(err)
	}

	if j.Kind != "copy" || j.Source != "shop_wp" || j.Database != "shop_stage" {
		t.Fatalf("job %+v", j)
	}

	j = e.finish(t, j)
	if j.State != agentapi.JobDone {
		t.Fatalf("job %+v", j)
	}

	if e.tools.imported != "-- dump of shop_wp\n" {
		t.Fatalf("imported %q", e.tools.imported)
	}

	if len(e.tools.runs) != 2 {
		t.Fatalf("runs %+v", e.tools.runs)
	}

	read, write := e.tools.runs[0], e.tools.runs[1]
	if read.Database != "shop_wp" || !strings.HasPrefix(read.User, jobUserPfx) {
		t.Errorf("export run %+v", read)
	}

	if write.Database != "shop_stage" || !strings.HasPrefix(write.User, ownerUserPfx) {
		t.Errorf("import run %+v", write)
	}

	all := strings.Join(e.db.stmts, "\n")
	for _, want := range []string{"GRANT ALL PRIVILEGES ON `shop\\_wp`.* TO 'hostashi_job_", "ACCOUNT UNLOCK", "ACCOUNT LOCK", "DROP USER IF EXISTS 'hostashi_job_"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}

	if strings.Contains(all, "ON `shop\\_stage`.* TO 'hostashi_job_") {
		t.Error("job user got rights on the target")
	}
}

func TestCopyDatabaseFailure(t *testing.T) {
	e := newCopyEnv(t)
	e.tools.err = &failedError{errors.New("mariadbctl: failed: server error 1146 at line 3 of the dump")}

	j, err := e.m.StartCopy("shop_wp", "shop_stage")
	if err != nil {
		t.Fatal(err)
	}

	j = e.finish(t, j)
	if j.State != agentapi.JobFailed || j.Error != "server error 1146 at line 3 of the dump" {
		t.Fatalf("job %+v", j)
	}

	if last := e.db.stmts[len(e.db.stmts)-1]; !strings.HasPrefix(last, "DROP USER IF EXISTS 'hostashi_job_") {
		t.Fatalf("job user not dropped last: %q", last)
	}
}

func TestCopyDatabaseRefuses(t *testing.T) {
	e := newCopyEnv(t)

	if _, err := e.m.StartCopy("shop_wp", "shop_wp"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same database: %v", err)
	}

	if _, err := e.m.StartCopy("shop_wp", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown target: %v", err)
	}

	e.tools.block = make(chan struct{})

	j, err := e.m.StartCopy("shop_wp", "shop_stage")
	if err != nil {
		t.Fatal(err)
	}

	// Both databases are busy while the copy runs.
	if _, err := e.m.StartImport("shop_wp", "a.sql", false); !errors.Is(err, ErrBusy) {
		t.Fatalf("import of the source: %v", err)
	}

	if _, err := e.m.StartCopy("shop_stage", "shop_wp"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second copy: %v", err)
	}

	close(e.tools.block)
	e.finish(t, j)
}
