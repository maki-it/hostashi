// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mariadbctl"
)

// Errors of import and export.
var (
	ErrBusy        = errors.New("mariadb: another import or export of this database is running")
	ErrNoDumps     = errors.New("mariadb: import and export are not available")
	ErrFileUnsafe  = errors.New("mariadb: dump file is missing, not a regular file of the site user, or outside the home")
	ErrFileExists  = errors.New("mariadb: the export file exists already")
	ErrJobNotFound = errors.New("mariadb: unknown job")
)

// Tools runs the client programs. Package mariadbctl is the real one.
type Tools interface {
	Import(ctx context.Context, r mariadbctl.Run, in io.Reader) error
	Export(ctx context.Context, r mariadbctl.Run, out io.Writer) error
}

// RealTools calls package mariadbctl.
type RealTools struct{}

// Import runs mariadb.
func (RealTools) Import(ctx context.Context, r mariadbctl.Run, in io.Reader) error {
	return mariadbctl.Import(ctx, r, in)
}

// Export runs mariadb-dump.
func (RealTools) Export(ctx context.Context, r mariadbctl.Run, out io.Writer) error {
	return mariadbctl.Export(ctx, r, out)
}

// SiteHome is where a site's dump files live and whose they are.
type SiteHome struct {
	Home     string
	UID, GID uint32
}

// Dumps configures import and export.
type Dumps struct {
	Tools  Tools
	HomeOf func(site string) (SiteHome, bool)
	Socket string // passed to the client, empty for its default
	// Ctx bounds every job; the agent passes its run context.
	Ctx context.Context //nolint:containedctx // jobs outlive the call that starts them
	// MaxDuration ends a job that runs longer.
	MaxDuration time.Duration
}

const (
	maxJobs     = 100
	jobUserPfx  = "hostashi_job_"
	defaultJobT = 6 * time.Hour
)

var jobUserRe = regexp.MustCompile(`^hostashi_job_[0-9a-f]{16}$`)

// EnableDumps turns on import and export.
func (m *Manager) EnableDumps(d Dumps) {
	if d.MaxDuration <= 0 {
		d.MaxDuration = defaultJobT
	}

	if d.Ctx == nil {
		d.Ctx = context.Background()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.dumps = &d
	m.jobs = map[string]*agentapi.DBJob{}
	m.busy = map[string]bool{}
}

// Job returns a copy of a job.
func (m *Manager) Job(id string) (agentapi.DBJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok {
		return agentapi.DBJob{}, ErrJobNotFound
	}

	return *j, nil
}

// wait blocks until the job has ended (tests; callers of the agent poll
// db.job instead).
func (m *Manager) wait(id string) {
	m.mu.Lock()
	ch := m.done[id]
	m.mu.Unlock()

	if ch != nil {
		<-ch
	}
}

// StartImport checks the request, opens the file and starts the job.
func (m *Manager) StartImport(database, rel string) (agentapi.DBJob, error) {
	return m.start("import", database, rel)
}

// StartExport checks the request, creates the file and starts the job.
func (m *Manager) StartExport(database, rel string) (agentapi.DBJob, error) {
	return m.start("export", database, rel)
}

func (m *Manager) start(kind, database, rel string) (agentapi.DBJob, error) {
	if err := (agentapi.DBDumpRequest{Database: database, Path: rel}).Validate(); err != nil {
		return agentapi.DBJob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dumps == nil {
		return agentapi.DBJob{}, ErrNoDumps
	}

	d, ok := m.reg.dbs[database]
	if !ok {
		return agentapi.DBJob{}, ErrNotFound
	}

	if m.busy[database] {
		return agentapi.DBJob{}, ErrBusy
	}

	home, ok := m.dumps.HomeOf(d.Site)
	if !ok {
		return agentapi.DBJob{}, ErrNotFound
	}

	var (
		f   *os.File
		err error
	)

	if kind == "import" {
		f, err = openDump(home, rel)
	} else {
		f, err = createDump(home, rel)
	}

	if err != nil {
		return agentapi.DBJob{}, err
	}

	id := randomHex(8)
	job := &agentapi.DBJob{ID: id, Kind: kind, Database: database, Path: rel, State: agentapi.JobRunning, Started: time.Now().UTC()}

	m.pruneJobs()
	m.jobs[id] = job
	m.busy[database] = true

	if m.done == nil {
		m.done = map[string]chan struct{}{}
	}

	done := make(chan struct{})
	m.done[id] = done

	go func() {
		defer close(done)

		err := m.runJob(kind, database, home, rel, f)

		m.mu.Lock()
		defer m.mu.Unlock()

		delete(m.busy, database)
		delete(m.done, id)

		job.Finished = time.Now().UTC()
		job.State = agentapi.JobDone

		if err != nil {
			job.State = agentapi.JobFailed
			job.Error = jobError(err)
			m.log.Error("mariadb: job failed", "job", id, "kind", kind, "database", database, "err", err)
		}
	}()

	return *job, nil
}

func (m *Manager) runJob(kind, database string, home SiteHome, rel string, f *os.File) (err error) {
	ctx, cancel := context.WithTimeout(m.dumps.Ctx, m.dumps.MaxDuration)
	defer cancel()

	defer f.Close()

	user := jobUserPfx + randomHex(8)
	pw := agentapi.GenerateDBPassword()
	acct := "'" + user + "'@'" + userHost + "'"

	grantOn, err := quoteGrantName(database)
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

	run := mariadbctl.Run{Database: database, UID: home.UID, GID: home.GID, User: user, Password: pw, Socket: m.dumps.Socket}
	gz := strings.HasSuffix(rel, ".gz")

	if kind == "import" {
		var in io.Reader = f

		if gz {
			zr, err := gzip.NewReader(f)
			if err != nil {
				return fmt.Errorf("%w: not a gzip file", ErrFileUnsafe)
			}

			defer zr.Close()

			in = zr
		}

		return m.dumps.Tools.Import(ctx, run, in)
	}

	// Export: a failed or partial file is removed again.
	defer func() {
		if err != nil {
			if rerr := removeDump(home, rel); rerr != nil {
				m.log.Error("mariadb: remove failed export", "path", rel, "err", rerr)
			}
		}
	}()

	var out io.Writer = f

	var zw *gzip.Writer

	if gz {
		zw = gzip.NewWriter(f)
		out = zw
	}

	if err := m.dumps.Tools.Export(ctx, run, out); err != nil {
		return err
	}

	if zw != nil {
		if err := zw.Close(); err != nil {
			return fmt.Errorf("mariadb: finish gzip: %w", err)
		}
	}

	return f.Sync()
}

// jobError is the text a caller sees: fixed, at most a line number and an
// error code from mariadbctl.
func jobError(err error) string {
	switch {
	case errors.Is(err, mariadbctl.ErrFailed):
		return strings.TrimPrefix(err.Error(), "mariadbctl: failed: ")
	case errors.Is(err, ErrFileUnsafe):
		return "not a gzip file"
	case errors.Is(err, ErrUnavailable):
		return "MariaDB is not reachable"
	default:
		return "internal error, see the agent log"
	}
}

func (m *Manager) pruneJobs() {
	if len(m.jobs) < maxJobs {
		return
	}

	ended := make([]*agentapi.DBJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		if j.State != agentapi.JobRunning {
			ended = append(ended, j)
		}
	}

	sort.Slice(ended, func(i, k int) bool { return ended[i].Finished.Before(ended[k].Finished) })

	for _, j := range ended[:max(0, len(m.jobs)-maxJobs+1)] {
		delete(m.jobs, j.ID)
	}
}

// RemoveJobUsers drops temporary users an agent crash left behind. db must
// be able to query; the production *sql.DB can.
func (m *Manager) RemoveJobUsers(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
},
) error {
	rows, err := q.QueryContext(ctx, `SELECT User FROM mysql.user WHERE Host = 'localhost' AND User LIKE 'hostashi\_job\_%'`)
	if err != nil {
		return classify("list job users", err)
	}

	var users []string

	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			_ = rows.Close()

			return classify("list job users", err)
		}

		if jobUserRe.MatchString(u) {
			users = append(users, u)
		}
	}

	_ = rows.Close()

	for _, u := range users {
		if err := m.exec(ctx, "drop job user", "DROP USER IF EXISTS '"+u+"'@'"+userHost+"'"); err != nil {
			return err
		}
	}

	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

const noEscape = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV

// openHome opens the site home and checks that the site user owns it.
func openHome(h SiteHome) (int, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, h.Home, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return -1, fmt.Errorf("%w: open home: %w", ErrFileUnsafe, err)
	}

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Uid != h.UID {
		_ = unix.Close(fd)

		return -1, fmt.Errorf("%w: home not owned by the site user", ErrFileUnsafe)
	}

	return fd, nil
}

// openDump opens rel below the home for reading: a regular file of the site
// user, reached without symlinks and without leaving the home.
func openDump(h SiteHome, rel string) (*os.File, error) {
	hfd, err := openHome(h)
	if err != nil {
		return nil, err
	}

	defer unix.Close(hfd)

	fd, err := unix.Openat2(hfd, rel, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK, Resolve: noEscape})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFileUnsafe, err)
	}

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != h.UID {
		_ = unix.Close(fd)

		return nil, ErrFileUnsafe
	}

	// Back to blocking reads; O_NONBLOCK only kept a FIFO from hanging the open.
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("mariadb: %w", err)
	}

	return os.NewFile(uintptr(fd), rel), nil
}

// removeDump deletes a failed export again, below the home and without
// following symlinks.
func removeDump(h SiteHome, rel string) error {
	hfd, err := openHome(h)
	if err != nil {
		return err
	}

	defer unix.Close(hfd)

	dirfd := hfd

	if dir := path.Dir(rel); dir != "." {
		dirfd, err = unix.Openat2(hfd, dir, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: noEscape})
		if err != nil {
			return fmt.Errorf("%w: %w", ErrFileUnsafe, err)
		}

		defer unix.Close(dirfd)
	}

	return unix.Unlinkat(dirfd, path.Base(rel), 0)
}

// createDump creates rel below the home, never replacing a file, and gives
// it to the site user with mode 0600. The directory must exist.
func createDump(h SiteHome, rel string) (*os.File, error) {
	hfd, err := openHome(h)
	if err != nil {
		return nil, err
	}

	defer unix.Close(hfd)

	dirfd := hfd

	if dir := path.Dir(rel); dir != "." {
		dirfd, err = unix.Openat2(hfd, dir, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: noEscape})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrFileUnsafe, err)
		}

		defer unix.Close(dirfd)

		var st unix.Stat_t
		if err := unix.Fstat(dirfd, &st); err != nil || st.Uid != h.UID {
			return nil, fmt.Errorf("%w: directory not owned by the site user", ErrFileUnsafe)
		}
	}

	fd, err := unix.Openat2(dirfd, path.Base(rel), &unix.OpenHow{
		Flags: unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC, Mode: 0o600, Resolve: noEscape,
	})
	if errors.Is(err, unix.EEXIST) {
		return nil, ErrFileExists
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFileUnsafe, err)
	}

	if err := unix.Fchown(fd, int(h.UID), int(h.GID)); err != nil {
		_ = unix.Close(fd)
		_ = unix.Unlinkat(dirfd, path.Base(rel), 0)

		return nil, fmt.Errorf("mariadb: chown export file: %w", err)
	}

	return os.NewFile(uintptr(fd), rel), nil
}
