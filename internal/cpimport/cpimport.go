// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cpimport runs the file part of the CloudPanel migration
// (ADR-0026 Part 3). The agent records an import and starts
// hostashi-import@<id>.service; that job reads the SSH key as root, opens
// the SFTP session to the old server, then drops to the site's user and
// group and only then writes. It mirrors the old document root into
// <home>/cloudpanel-import; a second run for the same site is the final
// sync. Replacing the web root and the databases come in later steps.
package cpimport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cpsource"
	"github.com/maki-it/hostashi/internal/mariadb"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/statefile"
)

// Import is one import as the agent and the job see it.
type Import = agentapi.CloudPanelImport

// Units starts the import job. resticctl.Units implements it.
type Units interface {
	Reload(ctx context.Context) error
	StartImport(ctx context.Context, id string) error
}

// Errors.
var (
	ErrInvalid  = errors.New("cpimport: invalid request")
	ErrNotReady = errors.New("cpimport: the old server is not set up")
	ErrNoSite   = errors.New("cpimport: no such site on this server")
	ErrBusy     = errors.New("cpimport: an import is in progress")
	ErrNotFound = errors.New("cpimport: no such import")
	// ErrNoDBUser: databases were asked for before the dump user's
	// password was generated.
	ErrNoDBUser   = errors.New("cpimport: generate the database user for the old server first")
	ErrNoDatabase = errors.New("cpimport: not a database of this site")
)

// Fixed paths.
const (
	// UnitDir is where the per-import drop-ins go.
	UnitDir     = "/etc/systemd/system"
	lockFile    = "import.lock"
	keepImports = 50
	maxRecord   = 64 << 10
)

var (
	importFileRe = regexp.MustCompile(`^import-([0-9a-f]{16})\.json$`)
	// The home lands in a systemd drop-in line; only plain paths qualify.
	unitPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,1000}$`)
)

func importFile(id string) string { return "import-" + id + ".json" }

func owner() uint32 { return uint32(os.Geteuid()) } //nolint:gosec // uids fit in uint32

// Agent is the agent's side: it checks a request, records it and starts
// the job.
type Agent struct {
	Dir      string // import records, root-owned 0700 (StateDir/cloudpanel/imports)
	StateDir string // the site registry
	UnitDir  string // default UnitDir
	Source   *cpsource.Source
	Units    Units

	mu sync.Mutex
}

func (a *Agent) unitDir() string {
	if a.UnitDir == "" {
		return UnitDir
	}

	return a.UnitDir
}

func (a *Agent) dropIn(id string) string {
	return filepath.Join(a.unitDir(), "hostashi-import@"+id+".service.d", "home.conf")
}

// Start records an import and starts its job. Only one import runs at a
// time: they share the one SSH key and the old server's disk.
func (a *Agent) Start(ctx context.Context, req agentapi.CloudPanelImportRequest) (Import, error) {
	if err := req.Validate(); err != nil {
		return Import{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	info, err := a.Source.Get()
	if err != nil {
		return Import{}, err
	}

	if !info.Ready {
		return Import{}, ErrNotReady
	}

	home, err := siteHome(a.StateDir, req.Site, owner())
	if err != nil {
		return Import{}, err
	}

	if err := a.checkDatabases(req); err != nil {
		return Import{}, err
	}

	if err := ensureDir(a.Dir); err != nil {
		return Import{}, err
	}

	all, err := a.imports()
	if err != nil {
		return Import{}, err
	}

	if held(filepath.Join(a.Dir, lockFile)) {
		return Import{}, ErrBusy
	}

	for _, r := range all {
		// A queued import whose job never took the lock is stale after a
		// minute; anything newer still counts as starting.
		if r.State == agentapi.ImportRunning || (r.State == agentapi.ImportQueued && time.Since(r.Created) < time.Minute) {
			return Import{}, ErrBusy
		}
	}

	a.prune(all)

	id := make([]byte, 8)
	_, _ = rand.Read(id)

	r := Import{
		ID: hex.EncodeToString(id), Site: req.Site, RemoteUser: req.RemoteUser, DocRoot: req.DocRoot, Databases: req.Databases,
		Dir: agentapi.CloudPanelImportDir, State: agentapi.ImportQueued, Created: time.Now().UTC(),
	}

	if err := writeRecord(a.Dir, r); err != nil {
		return Import{}, err
	}

	if err := writeUnitFile(a.dropIn(r.ID), dropIn(home.Path)); err != nil {
		return Import{}, err
	}

	if err := a.Units.Reload(ctx); err != nil {
		return Import{}, err
	}

	if err := a.Units.StartImport(ctx, r.ID); err != nil {
		return Import{}, err
	}

	return r, nil
}

// checkDatabases: every target is a database of the site here, and the
// dump user's password exists.
func (a *Agent) checkDatabases(req agentapi.CloudPanelImportRequest) error {
	if len(req.Databases) == 0 {
		return nil
	}

	if _, err := a.Source.DBPassword(); err != nil {
		if errors.Is(err, cpsource.ErrNoDB) {
			return ErrNoDBUser
		}

		return err
	}

	dbs, err := mariadb.ReadDatabases(a.StateDir, owner())
	if err != nil {
		return fmt.Errorf("database registry: %w", err)
	}

	ours := map[string]bool{}

	for _, d := range dbs {
		if d.Site == req.Site {
			ours[d.Name] = true
		}
	}

	for _, d := range req.Databases {
		if !ours[d.To] {
			return fmt.Errorf("%w: %s", ErrNoDatabase, d.To)
		}
	}

	return nil
}

// Get returns one import.
func (a *Agent) Get(id string) (Import, error) {
	if err := (agentapi.CloudPanelImportGetRequest{ID: id}).Validate(); err != nil {
		return Import{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return readRecord(a.Dir, id, owner())
}

// List returns the kept imports newest first, of one site or of all. A
// missing records directory means none.
func (a *Agent) List(site string) ([]Import, error) {
	if err := (agentapi.CloudPanelImportListRequest{Site: site}).Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if _, err := os.Lstat(a.Dir); errors.Is(err, os.ErrNotExist) {
		return []Import{}, nil
	}

	if err := statefile.CheckDir(a.Dir, owner()); err != nil {
		return nil, err
	}

	all, err := a.imports()
	if err != nil {
		return nil, err
	}

	out := make([]Import, 0, len(all))

	for _, r := range all {
		if site == "" || r.Site == site {
			r.SkippedPaths = nil
			out = append(out, r)
		}
	}

	return out, nil
}

// dropIn opens the site's home, and nothing else, for writing to one job.
func dropIn(home string) string {
	return "# Written by the Hostashi agent for one CloudPanel import.\n[Unit]\nRequiresMountsFor=" + home + "\n[Service]\nReadWritePaths=" + home + "\n"
}

// siteHome looks the site up in the registry.
func siteHome(stateDir, site string, ownerUID uint32) (sites.Home, error) {
	homes, err := sites.ReadHomes(stateDir, ownerUID)
	if err != nil {
		return sites.Home{}, fmt.Errorf("site registry: %w", err)
	}

	h, ok := homes[site]
	if !ok {
		return sites.Home{}, ErrNoSite
	}

	if !unitPathRe.MatchString(h.Path) || strings.Contains(h.Path, "..") || h.UID == 0 || h.GID == 0 {
		return sites.Home{}, fmt.Errorf("cpimport: home %q cannot be imported into", h.Path)
	}

	return h, nil
}

func ensureDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("cpimport: %w", err)
	}

	return statefile.CheckDir(dir, owner())
}

// imports reads every record, newest first.
func (a *Agent) imports() ([]Import, error) {
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return nil, fmt.Errorf("cpimport: %w", err)
	}

	var out []Import

	for _, e := range entries {
		m := importFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		r, err := readRecord(a.Dir, m[1], owner())
		if err != nil {
			// A record the job could not finish writing; it no longer
			// counts as running.
			continue
		}

		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })

	return out, nil
}

// prune removes the drop-ins of earlier imports and the records beyond
// keepImports. Errors only leave files behind.
func (a *Agent) prune(all []Import) {
	for i, r := range all {
		_ = os.RemoveAll(filepath.Dir(a.dropIn(r.ID)))

		if i >= keepImports-1 {
			_ = os.Remove(filepath.Join(a.Dir, importFile(r.ID)))
		}
	}
}

func readRecord(dir, id string, ownerUID uint32) (Import, error) {
	raw, err := statefile.Read(dir, importFile(id), ownerUID, maxRecord)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return Import{}, ErrNotFound
	case err != nil:
		return Import{}, err
	}

	var r Import

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&r); err != nil || r.ID != id {
		return Import{}, fmt.Errorf("cpimport: import record %s is damaged", id)
	}

	return r, nil
}

func writeRecord(dir string, r Import) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}

	return statefile.Write(dir, importFile(r.ID), append(b, '\n'))
}

// writeUnitFile writes a world-readable systemd file atomically.
func writeUnitFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cpimport: unit drop-in: %w", err)
	}

	tmp := path + ".tmp"
	_ = os.Remove(tmp)

	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil { //nolint:gosec // systemd files are world-readable like the packaged ones
		return fmt.Errorf("cpimport: unit drop-in: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("cpimport: unit drop-in: %w", err)
	}

	return nil
}

func lock(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cpimport: lock file: %w", err)
	}

	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)

		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}

		return nil, fmt.Errorf("cpimport: lock: %w", err)
	}

	return func() { _ = unix.Close(fd) }, nil
}

func held(path string) bool {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer unix.Close(fd)

	if err := unix.Flock(fd, unix.LOCK_SH|unix.LOCK_NB); err != nil {
		return errors.Is(err, unix.EWOULDBLOCK)
	}

	_ = unix.Flock(fd, unix.LOCK_UN)

	return false
}
