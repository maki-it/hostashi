// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

var pwRe = regexp.MustCompile(`IDENTIFIED BY '[^']*'`)

// redact hides the random passwords so statements can be compared.
func redact(stmts []string) string {
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = pwRe.ReplaceAllString(s, "IDENTIFIED BY '*'")
	}

	return strings.Join(out, "\n")
}

func TestOwnerLoad(t *testing.T) {
	e := newDumpEnv(t)

	dump := "CREATE DEFINER=`shop`@`localhost` PROCEDURE p() SELECT 'DEFINER=`x`@`y`';"
	if err := os.WriteFile(filepath.Join(e.home, "a.sql"), []byte(dump), 0o600); err != nil {
		t.Fatal(err)
	}

	j, err := e.m.StartImport("shop_wp", "a.sql", true)
	if err != nil || !j.AsOwner {
		t.Fatalf("%+v %v", j, err)
	}

	if j = e.finish(t, j); j.State != agentapi.JobDone {
		t.Fatalf("%+v", j)
	}

	if want := "CREATE  PROCEDURE p() SELECT 'DEFINER=`x`@`y`';"; e.tools.imported != want {
		t.Fatalf("imported %q, want %q", e.tools.imported, want)
	}

	run := e.tools.runs[0]
	if !ownerUserRe.MatchString(run.User) || run.Password == "" || run.UID != uint32(os.Getuid()) {
		t.Fatalf("run %+v", run)
	}

	acct := "'" + run.User + "'@'localhost'"
	want := strings.Join([]string{
		"CREATE USER " + acct + " IDENTIFIED BY '*' ACCOUNT LOCK",
		"GRANT ALL PRIVILEGES ON `shop\\_wp`.* TO " + acct,
		"ALTER USER " + acct + " IDENTIFIED BY '*' ACCOUNT UNLOCK",
		"ALTER USER " + acct + " IDENTIFIED BY '*' ACCOUNT LOCK",
	}, "\n")

	if got := redact(e.db.stmts); got != want {
		t.Fatalf("statements:\n%s\nwant:\n%s", got, want)
	}

	// The password of the load is not kept: locking replaces it.
	if strings.Contains(e.db.stmts[3], run.Password) || strings.Contains(e.db.stmts[0], run.Password) {
		t.Fatal("the load's password outlives the load")
	}

	// The owner is kept in the registry and reused.
	if l := e.m.List(); len(l) != 1 || l[0].Owner != run.User {
		t.Fatalf("list %+v", l)
	}

	e.db.stmts = nil

	if j = e.finish(t, must(e.m.StartImport("shop_wp", "a.sql", true))); j.State != agentapi.JobDone {
		t.Fatalf("%+v", j)
	}

	if len(e.db.stmts) != 2 || e.tools.runs[1].User != run.User {
		t.Fatalf("second load: %s", redact(e.db.stmts))
	}

	// Locking again after a restart, and dropping with the database.
	e.db.stmts = nil

	if err := e.m.LockOwners(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := redact(e.db.stmts); got != "ALTER USER IF EXISTS "+acct+" IDENTIFIED BY '*' ACCOUNT LOCK" {
		t.Fatalf("lock: %s", got)
	}

	e.db.stmts = nil

	if err := e.m.Delete(t.Context(), "shop_wp"); err != nil {
		t.Fatal(err)
	}

	if e.db.stmts[0] != "DROP USER IF EXISTS "+acct {
		t.Fatalf("delete: %s", strings.Join(e.db.stmts, "\n"))
	}
}

func TestOwnerLoadFailureLocks(t *testing.T) {
	e := newDumpEnv(t)

	if err := os.WriteFile(filepath.Join(e.home, "a.sql"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	e.tools.err = errors.New("client failed")

	if j := e.finish(t, must(e.m.StartImport("shop_wp", "a.sql", true))); j.State != agentapi.JobFailed {
		t.Fatalf("%+v", j)
	}

	if last := e.db.stmts[len(e.db.stmts)-1]; !strings.HasSuffix(last, "ACCOUNT LOCK") || !strings.HasPrefix(last, "ALTER USER") {
		t.Fatalf("not locked after a failed load: %s", redact(e.db.stmts))
	}
}

func TestOwnerCreateFailureLeavesNoOwner(t *testing.T) {
	e := newDumpEnv(t)

	if err := os.WriteFile(filepath.Join(e.home, "a.sql"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	e.db.failOn, e.db.err = "GRANT", errors.New("boom")

	if j := e.finish(t, must(e.m.StartImport("shop_wp", "a.sql", true))); j.State != agentapi.JobFailed {
		t.Fatalf("%+v", j)
	}

	if l := e.m.List(); l[0].Owner != "" {
		t.Fatalf("owner kept after a failed grant: %+v", l)
	}

	if last := e.db.stmts[len(e.db.stmts)-1]; !strings.HasPrefix(last, "DROP USER IF EXISTS 'hostashi_own_") {
		t.Fatalf("owner not dropped: %s", redact(e.db.stmts))
	}

	if len(e.tools.runs) != 0 {
		t.Fatal("client ran without an owner")
	}
}

func TestRegistryRejectsBadOwner(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`{"version":1,"databases":[{"name":"shop_wp","site":"shop","users":[],"owner":"root"}]}`,
		`{"version":1,"databases":[{"name":"shop_a","site":"shop","users":[],"owner":"hostashi_own_0123456789abcdef"},{"name":"shop_b","site":"shop","users":[],"owner":"hostashi_own_0123456789abcdef"}]}`,
	} {
		r := &registry{dbs: map[string]*Database{}, users: map[string]string{}}
		if err := r.load([]byte(raw)); !errors.Is(err, ErrStateInvalid) {
			t.Errorf("%s: err = %v", raw, err)
		}
	}
}

func must(j agentapi.DBJob, err error) agentapi.DBJob {
	if err != nil {
		panic(err)
	}

	return j
}
