// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statefile"
)

// restart builds a new manager on the same state directory, as an agent
// start does, and turns on dumps again.
func (e *dumpEnv) restart(t *testing.T) *Manager {
	t.Helper()

	m := newManager(t, e.m.reg.dir, e.db)
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())

	m.EnableDumps(Dumps{Tools: e.tools, HomeOf: func(site string) (SiteHome, bool) {
		return SiteHome{Home: e.home, UID: uid, GID: gid}, site == "shop"
	}})

	return m
}

func TestJobsSurviveRestart(t *testing.T) {
	e := newDumpEnv(t)

	j, err := e.m.StartExport("shop_wp", "done.sql.gz")
	if err != nil {
		t.Fatal(err)
	}

	done := e.finish(t, j)
	if done.State != agentapi.JobDone {
		t.Fatalf("%+v", done)
	}

	got, err := e.restart(t).Job(j.ID)
	if err != nil || got.State != agentapi.JobDone || got.Path != "done.sql.gz" || !got.Finished.Equal(done.Finished) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestRunningJobFailsAfterRestart(t *testing.T) {
	e := newDumpEnv(t)
	e.tools.block = make(chan struct{})

	if err := os.WriteFile(filepath.Join(e.home, "a.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}

	j, err := e.m.StartImport("shop_wp", "a.sql", false)
	if err != nil {
		t.Fatal(err)
	}

	// The agent stops while the import runs.
	m := e.restart(t)

	got, err := m.Job(j.ID)
	if err != nil || got.State != agentapi.JobFailed || got.Error != ErrTextInterrupted || got.Finished.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}

	// The database is free again for a new job.
	if err := os.WriteFile(filepath.Join(e.home, "b.sql"), []byte("SELECT 2;"), 0o600); err != nil {
		t.Fatal(err)
	}

	close(e.tools.block)
	e.finish(t, j)

	j2, err := m.StartImport("shop_wp", "b.sql", false)
	if err != nil {
		t.Fatal(err)
	}

	m.wait(j2.ID)
}

func TestBrokenJobsFileStartsEmpty(t *testing.T) {
	for name, content := range map[string]string{
		"not json":      "{",
		"wrong version": `{"version":99,"jobs":[]}`,
		"bad id":        `{"version":1,"jobs":[{"id":"../x","state":"running"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newDumpEnv(t)

			if err := statefile.Write(e.m.reg.dir, JobsFile, []byte(content)); err != nil {
				t.Fatal(err)
			}

			m := e.restart(t)

			m.mu.Lock()
			n := len(m.jobs)
			m.mu.Unlock()

			if n != 0 {
				t.Fatalf("%d jobs", n)
			}

			// The file is valid again.
			if _, err := statefile.Read(e.m.reg.dir, JobsFile, uint32(os.Getuid()), maxJobsFileSize); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKeptJobsArePruned(t *testing.T) {
	e := newDumpEnv(t)

	e.m.mu.Lock()
	for i := range maxJobs + 20 {
		id := randomHex(8)
		e.m.jobs[id] = &agentapi.DBJob{ID: id, Kind: "export", Database: "shop_wp", State: agentapi.JobDone, Started: e.m.now(), Finished: e.m.now().Add(-1e9 * time.Duration(i))}
	}

	e.m.saveJobs()
	e.m.mu.Unlock()

	m := e.restart(t)

	m.mu.Lock()
	n := len(m.jobs)
	m.mu.Unlock()

	if n >= maxJobs {
		t.Fatalf("%d jobs kept", n)
	}
}
