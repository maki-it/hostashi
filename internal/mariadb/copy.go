// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mariadbctl"
)

// StartCopy starts copying database src into database dst (ADR-0070).
// mariadb-dump reads src as src's site user with a temporary account;
// mariadb loads the stream into dst as dst's site user with dst's owner
// account, DEFINER clauses stripped (ADR-0030). Nothing touches the disk.
func (m *Manager) StartCopy(src, dst string) (agentapi.DBJob, error) {
	if err := (agentapi.DBCopyRequest{Source: src, Target: dst}).Validate(); err != nil {
		return agentapi.DBJob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dumps == nil {
		return agentapi.DBJob{}, ErrNoDumps
	}

	sd, ok := m.reg.dbs[src]
	if !ok {
		return agentapi.DBJob{}, ErrNotFound
	}

	dd, ok := m.reg.dbs[dst]
	if !ok {
		return agentapi.DBJob{}, ErrNotFound
	}

	if m.busy[src] || m.busy[dst] {
		return agentapi.DBJob{}, ErrBusy
	}

	sh, ok := m.dumps.HomeOf(sd.Site)
	if !ok {
		return agentapi.DBJob{}, ErrNotFound
	}

	dh, ok := m.dumps.HomeOf(dd.Site)
	if !ok {
		return agentapi.DBJob{}, ErrNotFound
	}

	id := randomHex(8)
	job := &agentapi.DBJob{ID: id, Kind: "copy", Database: dst, Source: src, State: agentapi.JobRunning, Started: time.Now().UTC()}

	m.pruneJobs()
	m.jobs[id] = job
	m.busy[src], m.busy[dst] = true, true
	m.saveJobs()

	if m.done == nil {
		m.done = map[string]chan struct{}{}
	}

	done := make(chan struct{})
	m.done[id] = done

	go func() {
		defer close(done)

		err := m.runCopy(src, dst, sh, dh)

		m.mu.Lock()
		defer m.mu.Unlock()

		delete(m.busy, src)
		delete(m.busy, dst)
		delete(m.done, id)

		job.Finished = time.Now().UTC()
		job.State = agentapi.JobDone

		if err != nil {
			job.State = agentapi.JobFailed
			job.Error = jobError(err)
			m.log.Error("mariadb: copy failed", "job", id, "source", src, "target", dst, "err", err)
		}

		m.saveJobs()
	}()

	return *job, nil
}

func (m *Manager) runCopy(src, dst string, sh, dh SiteHome) error {
	ctx, cancel := context.WithTimeout(m.dumps.Ctx, m.dumps.MaxDuration)
	defer cancel()

	// Read side: a temporary account with rights on src only.
	user := jobUserPfx + randomHex(8)
	pw := agentapi.GenerateDBPassword()
	acct := "'" + user + "'@'" + userHost + "'"

	grantOn, err := quoteGrantName(src)
	if err != nil {
		return err
	}

	if err := m.exec(ctx, "create job user", "CREATE USER "+acct+" IDENTIFIED BY '"+pw+"'"); err != nil {
		return err
	}

	defer m.undo(ctx, "DROP USER IF EXISTS "+acct)

	if err := m.exec(ctx, "grant job user", "GRANT ALL PRIVILEGES ON "+grantOn+".* TO "+acct); err != nil {
		return err
	}

	// Write side: dst's owner, so views and routines get a definer that
	// stays (ADR-0030).
	owner, err := m.ensureOwner(ctx, dst)
	if err != nil {
		return err
	}

	opw, relock, err := m.unlockOwner(ctx, owner)
	if err != nil {
		return err
	}

	defer relock()

	read := mariadbctl.Run{Database: src, UID: sh.UID, GID: sh.GID, User: user, Password: pw, Socket: m.dumps.Socket}
	write := mariadbctl.Run{Database: dst, UID: dh.UID, GID: dh.GID, User: owner, Password: opw, Socket: m.dumps.Socket}

	return copyStream(ctx, m.dumps.Tools, read, write)
}

// copyStream pipes the export into the import. Whichever side fails first
// stops the other; the export's error wins, it is the cause.
func copyStream(ctx context.Context, tools Tools, read, write mariadbctl.Run) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pr, pw := io.Pipe()
	exported := make(chan error, 1)

	go func() {
		err := tools.Export(ctx, read, pw)
		if err != nil {
			cancel()
		}

		_ = pw.CloseWithError(err)

		exported <- err
	}()

	ierr := tools.Import(ctx, write, StripDefiners(pr))
	if ierr != nil {
		cancel()
	}

	_ = pr.CloseWithError(errors.New("mariadb: import ended"))

	if eerr := <-exported; eerr != nil {
		return eerr
	}

	return ierr
}
