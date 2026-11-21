// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dbjob imports and exports databases on external servers
// (ADR-0041 addendum 1). The agent has no network, so it records a job and
// starts hostashi-dbjob@<id>.service. A drop-in for that one instance lets
// the job reach only the server's addresses, maps the server's name to
// them in a private /etc/hosts (the job needs no DNS) and opens the site's
// home for writing. The job runs mariadb or mariadb-dump as the site user
// through package mariadbctl, with a temporary database user that the
// panel created on the external server for this job only.
package dbjob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/statefile"
)

// Units starts a job. resticctl.Units implements it.
type Units interface {
	Reload(ctx context.Context) error
	StartDBJob(ctx context.Context, id string) error
}

// Errors.
var (
	ErrInvalid  = errors.New("dbjob: invalid request")
	ErrNoSite   = errors.New("dbjob: no such site on this server")
	ErrBusy     = errors.New("dbjob: another import or export of this database is running")
	ErrNotFound = errors.New("dbjob: no such job")
)

// Fixed paths and limits.
const (
	// UnitDir is where the per-job drop-ins go.
	UnitDir = "/etc/systemd/system"
	// Unit is the job's unit template.
	Unit = "hostashi-dbjob@"
	// MaxDuration ends a job; the unit's TimeoutStartSec matches it.
	MaxDuration = 6 * time.Hour
	keepJobs    = 100
	maxRecord   = 128 << 10
)

var (
	jobFileRe = regexp.MustCompile(`^job-([0-9a-f]{16})\.json$`)
	// The home lands in a systemd drop-in line; only plain paths qualify.
	unitPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,1000}$`)
	jobIDRe    = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// Record is a job as the agent and the unit keep it. Request holds the
// connection and the temporary password until the job has read it; the
// job then rewrites the record without it.
type Record struct {
	agentapi.DBJob

	Site    string                       `json:"site"`
	Request *agentapi.DBRemoteJobRequest `json:"request,omitempty"`
}

func jobFile(id string) string   { return "job-" + id + ".json" }
func hostsFile(id string) string { return "hosts-" + id }

func owner() uint32 { return uint32(os.Geteuid()) } //nolint:gosec // uids fit in uint32

// Agent is the agent's side: it checks a request, records it and starts
// the job.
type Agent struct {
	Dir      string // job records, root-owned 0700 (StateDir/dbjobs)
	StateDir string // the site registry
	UnitDir  string // default UnitDir
	Units    Units
	// Now is time.Now; tests set it.
	Now func() time.Time

	mu sync.Mutex
}

func (a *Agent) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}

	return time.Now().UTC()
}

func (a *Agent) unitDir() string {
	if a.UnitDir == "" {
		return UnitDir
	}

	return a.UnitDir
}

func (a *Agent) dropIn(id string) string {
	return filepath.Join(a.unitDir(), Unit+id+".service.d", "job.conf")
}

// Start records a job and starts its unit.
func (a *Agent) Start(ctx context.Context, req agentapi.DBRemoteJobRequest) (agentapi.DBJob, error) {
	if err := req.Validate(); err != nil {
		return agentapi.DBJob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	addrs, _ := req.Addresses() // checked by Validate

	a.mu.Lock()
	defer a.mu.Unlock()

	home, err := siteHome(a.StateDir, req.Site, owner())
	if err != nil {
		return agentapi.DBJob{}, err
	}

	if err := ensureDir(a.Dir); err != nil {
		return agentapi.DBJob{}, err
	}

	all, err := a.records()
	if err != nil {
		return agentapi.DBJob{}, err
	}

	for _, r := range all {
		if r.Database == req.Database && a.view(r).State == agentapi.JobRunning {
			return agentapi.DBJob{}, ErrBusy
		}
	}

	a.prune(all)

	id := make([]byte, 8)
	_, _ = rand.Read(id)

	r := Record{
		DBJob: agentapi.DBJob{
			ID: hex.EncodeToString(id), Kind: req.Kind, Database: req.Database, Path: req.Path,
			State: agentapi.JobRunning, Started: a.now(),
		},
		Site:    req.Site,
		Request: &req,
	}

	if err := writeRecord(a.Dir, r); err != nil {
		return agentapi.DBJob{}, err
	}

	// The client reads /etc/hosts as the site user: world-readable like the
	// real one. The directory stays 0700; the bind mount needs no path.
	if err := writeUnitFile(filepath.Join(a.Dir, hostsFile(r.ID)), hosts(req.Host, addrs)); err != nil {
		return agentapi.DBJob{}, err
	}

	if err := writeUnitFile(a.dropIn(r.ID), dropIn(home.Path, filepath.Join(a.Dir, hostsFile(r.ID)), addrs)); err != nil {
		return agentapi.DBJob{}, err
	}

	if err := a.Units.Reload(ctx); err != nil {
		return agentapi.DBJob{}, err
	}

	if err := a.Units.StartDBJob(ctx, r.ID); err != nil {
		return agentapi.DBJob{}, err
	}

	return r.DBJob, nil
}

// Get returns one job. A job that is still running long after its unit's
// time limit has ended without writing its result.
func (a *Agent) Get(id string) (agentapi.DBJob, error) {
	if !jobIDRe.MatchString(id) {
		return agentapi.DBJob{}, fmt.Errorf("%w: id", ErrInvalid)
	}

	r, err := readRecord(a.Dir, id, owner())
	if err != nil {
		return agentapi.DBJob{}, err
	}

	return a.view(r).DBJob, nil
}

func (a *Agent) view(r Record) Record {
	if r.State == agentapi.JobRunning && a.now().Sub(r.Started) > MaxDuration+10*time.Minute {
		r.State, r.Error = agentapi.JobFailed, "stopped"
	}

	r.Request = nil

	return r
}

// dropIn limits one job to the server's addresses, gives it the private
// hosts file and opens the site's home for writing.
func dropIn(home, hostsPath string, addrs []netip.Addr) string {
	list := make([]string, 0, len(addrs))
	for _, ad := range addrs {
		list = append(list, ad.String())
	}

	return "# Written by the Hostashi agent for one database import or export.\n" +
		"[Unit]\nRequiresMountsFor=" + home + "\n[Service]\n" +
		"ReadWritePaths=" + home + "\n" +
		"IPAddressAllow=" + strings.Join(list, " ") + "\n" +
		"BindReadOnlyPaths=" + hostsPath + ":/etc/hosts\n"
}

// hosts maps the server's name to the resolved addresses. For an IP
// address as host there is nothing to map.
func hosts(host string, addrs []netip.Addr) string {
	var b strings.Builder

	b.WriteString("# Written by the Hostashi agent for one database job.\n127.0.0.1 localhost\n::1 localhost\n")

	if _, err := netip.ParseAddr(host); err == nil {
		return b.String()
	}

	for _, ad := range addrs {
		b.WriteString(ad.String() + " " + host + "\n")
	}

	return b.String()
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
		return sites.Home{}, fmt.Errorf("dbjob: home %q cannot be used", h.Path)
	}

	return h, nil
}

func ensureDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("dbjob: %w", err)
	}

	return statefile.CheckDir(dir, owner())
}

// records reads every record, newest first. A damaged record is skipped.
func (a *Agent) records() ([]Record, error) {
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return nil, fmt.Errorf("dbjob: %w", err)
	}

	var out []Record

	for _, e := range entries {
		m := jobFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		r, err := readRecord(a.Dir, m[1], owner())
		if err != nil {
			continue
		}

		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })

	return out, nil
}

// prune removes the drop-ins and hosts files of ended jobs and the records
// beyond keepJobs. Errors only leave files behind.
func (a *Agent) prune(all []Record) {
	for i, r := range all {
		if a.view(r).State != agentapi.JobRunning {
			_ = os.RemoveAll(filepath.Dir(a.dropIn(r.ID)))
			_ = os.Remove(filepath.Join(a.Dir, hostsFile(r.ID)))
		}

		if i >= keepJobs-1 {
			_ = os.Remove(filepath.Join(a.Dir, jobFile(r.ID)))
		}
	}
}

func readRecord(dir, id string, ownerUID uint32) (Record, error) {
	raw, err := statefile.Read(dir, jobFile(id), ownerUID, maxRecord)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return Record{}, ErrNotFound
	case err != nil:
		return Record{}, err
	}

	var r Record

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&r); err != nil || r.ID != id {
		return Record{}, fmt.Errorf("dbjob: job record %s is damaged", id)
	}

	return r, nil
}

func writeRecord(dir string, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}

	return statefile.Write(dir, jobFile(r.ID), append(b, '\n'))
}

// writeUnitFile writes a world-readable systemd file atomically.
func writeUnitFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("dbjob: unit drop-in: %w", err)
	}

	tmp := path + ".tmp"
	_ = os.Remove(tmp)

	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil { //nolint:gosec // systemd files are world-readable like the packaged ones
		return fmt.Errorf("dbjob: unit drop-in: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("dbjob: unit drop-in: %w", err)
	}

	return nil
}
