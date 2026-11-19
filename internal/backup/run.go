// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/resticctl"
)

// Kinds of backup items; each is a restic tag kind:<kind>.
const (
	KindFiles = "files"
	KindDB    = "db"
	KindPanel = "panel"
)

// Item is one snapshot of a run.
type Item struct {
	Kind     string
	Site     string   // files and db
	Database string   // db
	Paths    []string // files and panel
}

// Tags are the restic tags of the item. Together with the host they form
// the group that the retention policy keeps.
func (i Item) Tags() []string {
	switch i.Kind {
	case KindFiles:
		return []string{"site:" + i.Site, "kind:files"}
	case KindDB:
		return []string{"site:" + i.Site, "kind:db", "db:" + i.Database}
	default:
		return []string{"kind:panel"}
	}
}

// Site is what a run needs to know about one site.
type Site struct {
	Name      string
	Home      string
	Databases []string
}

// PanelPaths are the panel's state in a backup: its environment file, the
// copy of the panel database the panel writes with VACUUM INTO (never the
// live file), and the agent's registries, certificates and generations.
// Left out on purpose: ConfigDir with the backup secrets, and the panel's
// encryption key /etc/hostashi/secret.key, which stays apart from the data
// it protects; both are exported by the administrator instead.
var PanelPaths = []string{"/etc/hostashi/hostashi.env", "/var/lib/hostashi/backup", "/var/lib/hostashi-agent"}

// Plan returns the items of a run: per site its files, then each of its
// databases, sorted by site and database; the panel last.
func Plan(sites []Site, panelPaths []string) []Item {
	sorted := append([]Site{}, sites...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var items []Item

	for _, s := range sorted {
		items = append(items, Item{Kind: KindFiles, Site: s.Name, Paths: []string{s.Home}})

		dbs := append([]string{}, s.Databases...)
		sort.Strings(dbs)

		for _, d := range dbs {
			items = append(items, Item{Kind: KindDB, Site: s.Name, Database: d})
		}
	}

	if len(panelPaths) > 0 {
		items = append(items, Item{Kind: KindPanel, Paths: append([]string{}, panelPaths...)})
	}

	return items
}

// Repo is the part of resticctl.Repo a run uses.
type Repo interface {
	Init(ctx context.Context) error
	Unlock(ctx context.Context) error
	Snapshots(ctx context.Context, tags []string) ([]resticctl.Snapshot, error)
	Backup(ctx context.Context, tags []string, paths ...string) (resticctl.Summary, error)
	BackupDatabase(ctx context.Context, tags []string, database string) (resticctl.Summary, error)
	Forget(ctx context.Context, k resticctl.Keep) error
	Prune(ctx context.Context) error
	Check(ctx context.Context, part, total int) error
}

// ItemResult is the outcome of one item.
type ItemResult = agentapi.BackupItemResult

// Status is the result of the last run, kept in StatusDir.
type Status struct {
	Version  int          `json:"version"`
	Started  time.Time    `json:"started"`
	Finished time.Time    `json:"finished"`
	OK       bool         `json:"ok"`
	Error    string       `json:"error,omitempty"` // run-level failure
	Items    []ItemResult `json:"items"`

	// Maintenance, carried from run to run.
	LastPrune     time.Time `json:"lastPrune,omitzero"`
	LastCheck     time.Time `json:"lastCheck,omitzero"`
	LastReadCheck time.Time `json:"lastReadCheck,omitzero"`
	ReadPart      int       `json:"readPart,omitempty"` // part read by the last data check
	MaintenanceOK bool      `json:"maintenanceOk"`
	Maintenance   string    `json:"maintenance,omitempty"` // last maintenance error
	// Initialized: this run created the repository.
	Initialized bool `json:"initialized,omitempty"`

	// Snapshots is the repository's list at the end of the run, kept in a
	// file of its own; nil if it could not be read.
	Snapshots []SnapshotEntry `json:"-"`
}

// SnapshotEntry is one snapshot of this server, with its tags decoded.
type SnapshotEntry struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Site     string    `json:"site,omitempty"`
	Database string    `json:"database,omitempty"`
}

// entries decodes the tags Item.Tags writes; snapshots with other tags
// (made by hand) are left out.
func entries(snaps []resticctl.Snapshot) []SnapshotEntry {
	out := []SnapshotEntry{}

	for _, s := range snaps {
		e := SnapshotEntry{ID: s.ID, Time: s.Time.UTC()}

		for _, t := range s.Tags {
			k, v, _ := strings.Cut(t, ":")

			switch k {
			case "kind":
				e.Kind = v
			case "site":
				e.Site = v
			case "db":
				e.Database = v
			}
		}

		valid := (e.Kind == KindPanel && e.Site == "" && e.Database == "") ||
			(e.Kind == KindFiles && e.Site != "" && e.Database == "") ||
			(e.Kind == KindDB && e.Site != "" && e.Database != "")
		if valid {
			out = append(out, e)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })

	return out
}

// StatusVersion is the current format of the status file.
const StatusVersion = 1

// Maintenance intervals. A little slack keeps a daily run that starts a
// few minutes earlier than last week from skipping a week.
const (
	pruneEvery     = 7*24*time.Hour - 6*time.Hour
	checkEvery     = 7*24*time.Hour - 6*time.Hour
	readCheckEvery = 30*24*time.Hour - 6*time.Hour
)

// Runner runs one backup.
type Runner struct {
	Repo     Repo
	Settings Settings
	Prev     Status // the status of the previous run, zero if none
	Now      func() time.Time
	Log      *slog.Logger
	// Lstat is os.Lstat; tests replace it.
	Lstat func(string) (os.FileInfo, error)
}

// Run backs up all items, applies the retention policy and does the
// maintenance that is due. It always returns a status; OK is true only if
// every item and the retention step succeeded. A failed item does not stop
// the others.
func (r *Runner) Run(ctx context.Context, items []Item) Status {
	now, lstat, log := r.Now, r.Lstat, r.Log
	if now == nil {
		now = time.Now
	}

	if lstat == nil {
		lstat = os.Lstat
	}

	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	st := newStatus(r.Prev, now())

	finish := func(err error) Status {
		if err != nil {
			st.Error = err.Error()
		}

		st.OK = st.Error == ""

		for _, it := range st.Items {
			if it.Error != "" {
				st.OK = false
			}
		}

		st.Finished = now().UTC()

		return st
	}

	if err := r.Settings.Validate(); err != nil {
		return finish(err)
	}

	if r.Settings.Target == nil {
		return finish(errors.New("no backup target is set up"))
	}

	err := r.Repo.Unlock(ctx)
	if errors.Is(err, resticctl.ErrNoRepository) {
		// The first run after the target was set up creates the repository
		// with the password the agent generated.
		if err = r.Repo.Init(ctx); err == nil {
			st.Initialized = true
		}
	}

	if err != nil {
		return finish(fmt.Errorf("open repository: %w", err))
	}

	for _, it := range items {
		if ctx.Err() != nil {
			return finish(ctx.Err())
		}

		st.Items = append(st.Items, r.item(ctx, it, lstat, log))
	}

	if err := r.Repo.Forget(ctx, keep(r.Settings.Retention)); err != nil {
		return finish(fmt.Errorf("retention: %w", err))
	}

	r.maintain(ctx, &st, now)

	if snaps, err := r.Repo.Snapshots(ctx, nil); err != nil {
		log.Error("list snapshots", "err", err)
	} else {
		st.Snapshots = entries(snaps)
	}

	return finish(nil)
}

// newStatus starts the status of a run, carrying the maintenance times of
// the previous one.
func newStatus(prev Status, started time.Time) Status {
	return Status{
		Version: StatusVersion, Started: started.UTC(), Items: []ItemResult{},
		LastPrune: prev.LastPrune, LastCheck: prev.LastCheck, LastReadCheck: prev.LastReadCheck,
		ReadPart: prev.ReadPart, MaintenanceOK: true,
	}
}

func (r *Runner) item(ctx context.Context, it Item, lstat func(string) (os.FileInfo, error), log *slog.Logger) ItemResult {
	res := ItemResult{Kind: it.Kind, Site: it.Site, Database: it.Database}

	var (
		sum resticctl.Summary
		err error
	)

	switch it.Kind {
	case KindFiles:
		// restic 0.18 exits 0 when a top-level path is missing (fixed in
		// 0.19.0), so a missing or replaced home is caught here. A symlink
		// would be stored as a link and hide that the site is gone.
		if err = checkDir(it.Paths[0], lstat); err == nil {
			sum, err = r.Repo.Backup(ctx, it.Tags(), it.Paths...)
		}
	case KindDB:
		if err = agentapi.ValidateDBName(it.Database); err == nil {
			sum, err = r.Repo.BackupDatabase(ctx, it.Tags(), it.Database)
		}
	case KindPanel:
		// The panel copy appears only once the panel has written it, so
		// missing panel paths are left out rather than failing the item.
		var present []string

		for _, p := range it.Paths {
			if fi, err := lstat(p); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
				present = append(present, p)
			}
		}

		if len(present) == 0 {
			err = errors.New("no panel state found")
		} else {
			sum, err = r.Repo.Backup(ctx, it.Tags(), present...)
		}
	default:
		err = fmt.Errorf("unknown kind %q", it.Kind)
	}

	if errors.Is(err, resticctl.ErrPartial) {
		res.Partial, err = true, nil
	}

	if err != nil {
		res.Error = err.Error()
		log.Error("backup item failed", "kind", it.Kind, "site", it.Site, "database", it.Database, "err", err)

		return res
	}

	res.SnapshotID, res.DataAdded, res.Files, res.Bytes = sum.SnapshotID, sum.DataAdded, sum.TotalFiles, sum.TotalBytes

	return res
}

func checkDir(p string, lstat func(string) (os.FileInfo, error)) error {
	fi, err := lstat(p)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("%s: not a directory", p)
	}

	return nil
}

// maintain runs prune, check and the data check when they are due. Their
// failures are recorded but do not make the run's backups fail.
func (r *Runner) maintain(ctx context.Context, st *Status, now func() time.Time) {
	fail := func(step string, err error) {
		st.MaintenanceOK = false
		st.Maintenance = step + ": " + err.Error()
	}

	if t := now(); t.Sub(st.LastPrune) >= pruneEvery {
		if err := r.Repo.Prune(ctx); err != nil {
			fail("prune", err)
		} else {
			st.LastPrune = t.UTC()
		}
	}

	parts := r.Settings.ReadParts

	if t := now(); parts > 0 && t.Sub(st.LastReadCheck) >= readCheckEvery {
		part := st.ReadPart%parts + 1
		if err := r.Repo.Check(ctx, part, parts); err != nil {
			fail("check", err)
		} else {
			st.LastReadCheck, st.LastCheck, st.ReadPart = t.UTC(), t.UTC(), part
		}

		return
	}

	if t := now(); t.Sub(st.LastCheck) >= checkEvery {
		if err := r.Repo.Check(ctx, 0, 0); err != nil {
			fail("check", err)
		} else {
			st.LastCheck = t.UTC()
		}
	}
}
