// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statefile"
)

// TimerDropIn is where the agent writes the schedule window.
const TimerDropIn = "/etc/systemd/system/hostashi-backup.timer.d/window.conf"

var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// Units is the part of resticctl.Units the agent uses.
type Units interface {
	StartRun(ctx context.Context) error
	Reload(ctx context.Context) error
	EnableTimer(ctx context.Context) error
	DisableTimer(ctx context.Context) error
	StartRestore(ctx context.Context, id string) error
}

// Errors of the agent side.
var (
	ErrNotReady   = errors.New("backup: no target or no confirmed host key")
	ErrBadHostKey = errors.New("backup: host key is not a single supported SSH public key")
	ErrRunning    = errors.New("backup: a run is in progress")
)

// Agent keeps the backup configuration for the agent: settings, the
// repository password and SSH key it generates, the pinned host key and
// the timer. All files are root-owned; nothing here talks to the network.
type Agent struct {
	ConfigDir string // default ConfigDir
	StatusDir string // default StatusDir
	DropIn    string // default TimerDropIn
	// StateDir holds the site registry (default AgentStateDir); UnitDir is
	// where restore drop-ins go (default UnitDir).
	StateDir string
	UnitDir  string
	Units    Units
	Host     string // comment of the public key
	// Space checks a restore against the site's disk limits; nil: no check.
	Space SpaceFunc

	mu sync.Mutex
}

func (a *Agent) configDir() string {
	if a.ConfigDir == "" {
		return ConfigDir
	}

	return a.ConfigDir
}

func (a *Agent) statusDir() string {
	if a.StatusDir == "" {
		return StatusDir
	}

	return a.StatusDir
}

func (a *Agent) dropIn() string {
	if a.DropIn == "" {
		return TimerDropIn
	}

	return a.DropIn
}

// Get returns the configuration without secrets.
func (a *Agent) Get() (agentapi.BackupInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.info()
}

func (a *Agent) info() (agentapi.BackupInfo, error) {
	s, err := ReadSettings(a.configDir(), owner())
	if err != nil {
		return agentapi.BackupInfo{}, err
	}

	info := agentapi.BackupInfo{Settings: s}

	if s.Target == nil {
		return info, nil
	}

	if info.PublicKey, err = a.publicKey(); err != nil {
		return agentapi.BackupInfo{}, err
	}

	key, err := a.pinnedHostKey(*s.Target)
	if err != nil {
		return agentapi.BackupInfo{}, err
	}

	if key != nil {
		info.HostKey = ssh.FingerprintSHA256(key)
		info.Ready = true
	}

	return info, nil
}

// Set replaces the settings. With a target it creates the repository
// password and the SSH key on first use (never replacing them: the
// password opens every existing backup). A new host or port drops the
// pinned host key. The timer follows: on only when ready.
func (a *Agent) Set(ctx context.Context, s Settings) (agentapi.BackupInfo, error) {
	if err := s.Validate(); err != nil {
		return agentapi.BackupInfo{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := ensureDir(a.configDir()); err != nil {
		return agentapi.BackupInfo{}, err
	}

	old, err := ReadSettings(a.configDir(), owner())
	if err != nil {
		return agentapi.BackupInfo{}, err
	}

	if s.Target != nil {
		if err := a.ensureSecrets(); err != nil {
			return agentapi.BackupInfo{}, err
		}
	}

	if old.Target != nil && (s.Target == nil || s.Target.KnownHostsName() != old.Target.KnownHostsName()) {
		if err := os.Remove(filepath.Join(a.configDir(), KnownHostsFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return agentapi.BackupInfo{}, fmt.Errorf("backup: drop host key: %w", err)
		}
	}

	if err := WriteSettings(a.configDir(), s); err != nil {
		return agentapi.BackupInfo{}, err
	}

	return a.applyTimer(ctx)
}

// SetHostKey pins the target's host key. line is one public key in
// authorized_keys form; options, several keys and weak types are refused.
func (a *Agent) SetHostKey(ctx context.Context, line string) (agentapi.BackupInfo, error) {
	key, err := ParseHostKey(line)
	if err != nil {
		return agentapi.BackupInfo{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	s, err := ReadSettings(a.configDir(), owner())
	if err != nil {
		return agentapi.BackupInfo{}, err
	}

	if s.Target == nil {
		return agentapi.BackupInfo{}, ErrNotReady
	}

	known := knownhosts.Line([]string{s.Target.KnownHostsName()}, key) + "\n"
	if err := statefile.Write(a.configDir(), KnownHostsFile, []byte(known)); err != nil {
		return agentapi.BackupInfo{}, err
	}

	return a.applyTimer(ctx)
}

// ParseHostKey accepts exactly one SSH public key of a supported type:
// Ed25519, ECDSA, or RSA with at least 3072 bits.
func ParseHostKey(line string) (ssh.PublicKey, error) {
	line = strings.TrimSpace(line)
	if strings.ContainsAny(line, "\n\r") {
		return nil, ErrBadHostKey
	}

	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || len(options) > 0 || len(rest) > 0 {
		return nil, ErrBadHostKey
	}

	switch key.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return key, nil
	case ssh.KeyAlgoRSA:
		ck, ok := key.(ssh.CryptoPublicKey)
		if !ok {
			return nil, ErrBadHostKey
		}

		if rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey); ok && rk.N.BitLen() >= 3072 {
			return key, nil
		}
	}

	return nil, ErrBadHostKey
}

// StartRun starts a run in its own unit.
func (a *Agent) StartRun(ctx context.Context) error {
	info, err := a.Get()
	if err != nil {
		return err
	}

	if !info.Ready {
		return ErrNotReady
	}

	if Running(a.statusDir()) {
		return ErrRunning
	}

	return a.Units.StartRun(ctx)
}

// Status returns the result of the last run.
func (a *Agent) Status() (agentapi.BackupStatus, error) {
	st, err := ReadStatus(a.statusDir(), owner())
	if err != nil {
		return agentapi.BackupStatus{}, err
	}

	items := st.Items
	if items == nil {
		items = []ItemResult{}
	}

	return agentapi.BackupStatus{
		Running: Running(a.statusDir()), Started: st.Started, Finished: st.Finished, OK: st.OK, Error: st.Error,
		Items: items, LastPrune: st.LastPrune, LastCheck: st.LastCheck, LastReadCheck: st.LastReadCheck,
		MaintenanceOK: st.MaintenanceOK, Maintenance: st.Maintenance,
	}, nil
}

// Snapshots returns the snapshots of a site (or of the panel for "") as
// the last run found them, newest first.
func (a *Agent) Snapshots(site string) ([]agentapi.BackupSnapshot, error) {
	all, err := ReadSnapshots(a.statusDir(), owner())
	if err != nil {
		return nil, err
	}

	out := []agentapi.BackupSnapshot{}

	for _, s := range all {
		if s.Site == site {
			out = append(out, agentapi.BackupSnapshot{ID: s.ID, Time: s.Time, Kind: s.Kind, Database: s.Database, Bytes: s.Bytes, Files: s.Files})
		}
	}

	return out, nil
}

// SnapshotSites summarises the snapshot list of the last run by site
// name, deleted sites included (ADR-0074). Panel snapshots are left out.
func (a *Agent) SnapshotSites() ([]agentapi.BackupSiteSummary, error) {
	all, err := ReadSnapshots(a.statusDir(), owner())
	if err != nil {
		return nil, err
	}

	live, err := Sites(a.stateDir(), owner())
	if err != nil {
		return nil, err
	}

	exists := map[string]bool{}
	for _, s := range live {
		exists[s.Name] = true
	}

	by := map[string]*agentapi.BackupSiteSummary{}

	for _, s := range all {
		if s.Site == "" {
			continue
		}

		e := by[s.Site]
		if e == nil {
			e = &agentapi.BackupSiteSummary{Site: s.Site, Exists: exists[s.Site], Oldest: s.Time, Newest: s.Time}
			by[s.Site] = e
		}

		e.Snapshots++

		if s.Time.Before(e.Oldest) {
			e.Oldest = s.Time
		}

		if s.Time.After(e.Newest) {
			e.Newest = s.Time
		}
	}

	out := make([]agentapi.BackupSiteSummary, 0, len(by))
	for _, e := range by {
		out = append(out, *e)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Site < out[j].Site })

	return out, nil
}

// applyTimer writes the window drop-in and switches the timer on when a
// target and its host key are set, off otherwise.
func (a *Agent) applyTimer(ctx context.Context) (agentapi.BackupInfo, error) {
	info, err := a.info()
	if err != nil {
		return agentapi.BackupInfo{}, err
	}

	if err := writeDropIn(a.dropIn(), info.Settings); err != nil {
		return agentapi.BackupInfo{}, err
	}

	if a.Units == nil {
		return info, nil
	}

	if err := a.Units.Reload(ctx); err != nil {
		return agentapi.BackupInfo{}, err
	}

	if info.Ready {
		err = a.Units.EnableTimer(ctx)
	} else {
		err = a.Units.DisableTimer(ctx)
	}

	if err != nil {
		return agentapi.BackupInfo{}, err
	}

	return info, nil
}

// TimerWindow renders the drop-in. A daily run fires at the start of the
// window plus a random delay below the window's length. Shorter intervals
// fire every IntervalHours hours from midnight plus a random delay below
// one hour. The packaged timer sets FixedRandomDelay, so the delay is the
// same on every run of a server and the runs stay evenly spaced. systemd
// never starts the service while a run is still active, so a run longer
// than the interval skips the next start instead of overlapping it.
func TimerWindow(s Settings) string {
	const head = "# Written by the Hostashi agent from the backup settings.\n[Timer]\nOnCalendar=\n"

	if s.IntervalHours > 0 && s.IntervalHours < 24 {
		delay := min(s.IntervalHours*60, 60)

		return fmt.Sprintf(head+"OnCalendar=*-*-* 00/%d:00:00\nRandomizedDelaySec=%dmin\n", s.IntervalHours, delay)
	}

	return fmt.Sprintf(head+"OnCalendar=*-*-* %02d:00:00\nRandomizedDelaySec=%dh\n",
		s.WindowStart, s.WindowEnd-s.WindowStart)
}

func writeDropIn(path string, s Settings) error {
	want := []byte(TimerWindow(s))
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, want) {
		return nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("backup: timer drop-in: %w", err)
	}

	tmp := path + ".tmp"
	_ = os.Remove(tmp)

	// systemd unit files are world-readable like the packaged ones.
	if err := os.WriteFile(tmp, want, 0o644); err != nil { //nolint:gosec // see above
		return fmt.Errorf("backup: timer drop-in: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("backup: timer drop-in: %w", err)
	}

	return nil
}

// owner is the uid that owns the files: root on a server, the test user
// in tests.
func owner() uint32 { return uint32(os.Geteuid()) } //nolint:gosec // uids fit in uint32

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("backup: config directory: %w", err)
	}

	return statefile.CheckDir(dir, owner())
}

// ensureSecrets creates the repository password and the SSH key if they
// are missing. Existing ones are never replaced.
func (a *Agent) ensureSecrets() error {
	dir := a.configDir()
	uid := owner()

	if _, err := statefile.Read(dir, PasswordFile, uid, 4096); errors.Is(err, os.ErrNotExist) {
		buf := make([]byte, 32)
		_, _ = rand.Read(buf)

		if err := statefile.Write(dir, PasswordFile, []byte(base64.RawURLEncoding.EncodeToString(buf)+"\n")); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if _, err := statefile.Read(dir, SSHKeyFile, uid, 8192); errors.Is(err, os.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("backup: ssh key: %w", err)
		}

		block, err := ssh.MarshalPrivateKey(priv, "hostashi-backup")
		if err != nil {
			return fmt.Errorf("backup: ssh key: %w", err)
		}

		if err := statefile.Write(dir, SSHKeyFile, pem.EncodeToMemory(block)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	return nil
}

func (a *Agent) publicKey() (string, error) {
	raw, err := statefile.Read(a.configDir(), SSHKeyFile, owner(), 8192)
	if err != nil {
		return "", fmt.Errorf("backup: ssh key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("backup: ssh key: %w", err)
	}

	comment := "hostashi-backup"
	if hostRe.MatchString(a.Host) {
		comment += "@" + a.Host
	}

	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " " + comment, nil
}

// pinnedHostKey returns the pinned key for the target, nil if none.
func (a *Agent) pinnedHostKey(t Target) (ssh.PublicKey, error) {
	raw, err := statefile.Read(a.configDir(), KnownHostsFile, owner(), agentapi.MaxHostKey+512)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	_, hosts, key, _, _, err := ssh.ParseKnownHosts(raw)
	if err != nil || len(hosts) != 1 || hosts[0] != t.KnownHostsName() {
		return nil, nil //nolint:nilerr // a file for another host is no pinned key
	}

	return key, nil
}

// Running reports whether a run holds the lock in statusDir.
func Running(statusDir string) bool { return held(filepath.Join(statusDir, runLock)) }

// held reports whether someone holds the lock file at path.
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

// PanelKeyFile is the panel's encryption key, kept out of the backups.
const PanelKeyFile = "/etc/hostashi/secret.key"

// Export is what an administrator must keep outside the server to read
// the backups after losing it (ADR-0008, recommendation 4).
type Export struct {
	Repository string
	Password   string
	PanelKey   string // base64 of PanelKeyFile, empty if it cannot be read
}

// ExportKeys reads the secrets for hostashi backup key show. It runs as
// root on the server only; no operation of the agent returns them.
func ExportKeys(configDir, panelKey string) (Export, error) {
	s, err := ReadSettings(configDir, owner())
	if err != nil {
		return Export{}, err
	}

	if s.Target == nil {
		return Export{}, ErrNotReady
	}

	pw, err := statefile.Read(configDir, PasswordFile, owner(), 4096)
	if err != nil {
		return Export{}, fmt.Errorf("backup: password: %w", err)
	}

	e := Export{Repository: s.Target.Repository(), Password: strings.TrimSpace(string(pw))}

	if raw, err := os.ReadFile(panelKey); err == nil {
		e.PanelKey = base64.StdEncoding.EncodeToString(raw)
	}

	return e, nil
}
