// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sites is the root-side owner of site accounts: it knows which sites
// and storage pools exist, creates and removes the Linux user and the home
// directory, and keeps the registry that the caller can never influence.
//
// The registry lives in a root-owned directory outside anything the web
// process can write. uid, gid and pool of a site come from here (spike S4).
package sites

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// StateFile is the registry file name inside the state directory.
const StateFile = "sites.json"

const stateVersion = 1

const maxStateSize = 4 << 20

// Pool is a storage location for site homes (ADR-0016). Pools are root-owned
// state, never request input.
type Pool struct {
	Name string `json:"name"`
	Path string `json:"path"` // absolute and clean, no symlinks in the chain
	// ID is the content of the marker file .hostashi-pool in the pool root.
	// Empty disables the check (only the default pool on the system volume).
	ID string `json:"id,omitempty"`
	// RequireMount demands that Path is the root of a mount point, so a missing
	// volume cannot silently put sites on the system disk.
	RequireMount bool `json:"requireMount,omitempty"`
}

// Site is root-owned state: uid and gid come from here, never from a caller.
type Site struct {
	User string `json:"user"`
	UID  uint32 `json:"uid"`
	GID  uint32 `json:"gid"`
	Pool string `json:"pool"`
}

type state struct {
	Version int    `json:"version"`
	Pools   []Pool `json:"pools"`
	Sites   []Site `json:"sites"`
	// Default is the pool of a new site that names none. Written only when
	// it is not agentapi.DefaultPool, so an older agent can still read a
	// registry whose default was never changed.
	Default string `json:"default,omitempty"`
}

// Registry is the persistent list of pools and sites. It is not safe for
// concurrent use; Manager serializes access.
type Registry struct {
	path  string
	pools map[string]Pool
	sites map[string]Site
	def   string // the default pool, always registered
}

// Errors of the registry.
var (
	ErrStateUnsafe  = errors.New("sites: state directory or file is not safe")
	ErrStateInvalid = errors.New("sites: state file is invalid")
)

// poolPathRe is the character set of a pool path: it ends up in unit
// files and drop-ins (sites.UnitIsolation), so nothing that systemd would
// read as a separator, specifier or escape.
var poolPathRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

// DefaultPools is what a fresh registry starts with: one pool on the system
// volume. The installer or a later admin command adds real pools.
func DefaultPools() []Pool {
	return []Pool{{Name: agentapi.DefaultPool, Path: "/home"}}
}

// OpenRegistry loads dir/sites.json. dir must be owned by ownerUID (root in
// production) and closed to group and others. A missing file starts a fresh
// registry with DefaultPools and writes it.
func OpenRegistry(dir string, ownerUID uint32) (*Registry, error) {
	if err := checkStateDir(dir, ownerUID); err != nil {
		return nil, err
	}

	reg := &Registry{
		path:  filepath.Join(dir, StateFile),
		pools: map[string]Pool{},
		sites: map[string]Site{},
		def:   agentapi.DefaultPool,
	}

	raw, err := readStateFile(reg.path, ownerUID)

	switch {
	case errors.Is(err, os.ErrNotExist):
		for _, p := range DefaultPools() {
			reg.pools[p.Name] = p
		}

		if err := reg.save(); err != nil {
			return nil, err
		}

		return reg, nil
	case err != nil:
		return nil, err
	}

	if err := reg.load(raw); err != nil {
		return nil, err
	}

	return reg, nil
}

// Home is where a registered site lives and whose its files are.
type Home struct {
	Path     string
	UID, GID uint32
}

// ReadHomes returns the home of every registered site, keyed by user,
// without writing anything: for readers outside the agent such as the
// backup run. A missing registry means no sites.
func ReadHomes(dir string, ownerUID uint32) (map[string]Home, error) {
	if err := checkStateDir(dir, ownerUID); err != nil {
		return nil, err
	}

	reg := &Registry{path: filepath.Join(dir, StateFile), pools: map[string]Pool{}, sites: map[string]Site{}}

	raw, err := readStateFile(reg.path, ownerUID)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return map[string]Home{}, nil
	case err != nil:
		return nil, err
	}

	if err := reg.load(raw); err != nil {
		return nil, err
	}

	out := make(map[string]Home, len(reg.sites))

	for _, site := range reg.sites {
		p, ok := reg.pools[site.Pool]
		if !ok {
			return nil, fmt.Errorf("site %q: unknown pool %q", site.User, site.Pool)
		}

		out[site.User] = Home{Path: p.Path + "/" + site.User, UID: site.UID, GID: site.GID}
	}

	return out, nil
}

func checkStateDir(dir string, ownerUID uint32) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("%w: directory must be an absolute clean path", ErrStateUnsafe)
	}

	fd, err := unix.Openat2(unix.AT_FDCWD, dir, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return fmt.Errorf("%w: open directory: %w", ErrStateUnsafe, err)
	}

	defer unix.Close(fd)

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("%w: stat directory: %w", ErrStateUnsafe, err)
	}

	if st.Uid != ownerUID || st.Mode&0o077 != 0 {
		return fmt.Errorf("%w: directory must be owned by uid %d with mode 0700", ErrStateUnsafe, ownerUID)
	}

	return nil
}

func readStateFile(path string, ownerUID uint32) ([]byte, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}

		return nil, fmt.Errorf("%w: open state file: %w", ErrStateUnsafe, err)
	}

	f := os.NewFile(uintptr(fd), path)
	defer f.Close()

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, fmt.Errorf("%w: stat state file: %w", ErrStateUnsafe, err)
	}

	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != ownerUID || st.Mode&0o077 != 0 {
		return nil, fmt.Errorf("%w: state file must be a regular file owned by uid %d with mode 0600", ErrStateUnsafe, ownerUID)
	}

	raw, err := io.ReadAll(io.LimitReader(f, maxStateSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %w", ErrStateUnsafe, err)
	}

	if len(raw) > maxStateSize {
		return nil, fmt.Errorf("%w: file larger than %d bytes", ErrStateInvalid, maxStateSize)
	}

	return raw, nil
}

func (r *Registry) load(raw []byte) error {
	var s state

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&s); err != nil || dec.More() {
		return fmt.Errorf("%w: not a single valid document", ErrStateInvalid)
	}

	if s.Version != stateVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrStateInvalid, s.Version)
	}

	for _, p := range s.Pools {
		if err := validatePool(p); err != nil {
			return fmt.Errorf("%w: %w", ErrStateInvalid, err)
		}

		if _, dup := r.pools[p.Name]; dup {
			return fmt.Errorf("%w: duplicate pool %q", ErrStateInvalid, p.Name)
		}

		r.pools[p.Name] = p
	}

	uids := map[uint32]string{}

	for _, site := range s.Sites {
		if err := validateSite(site); err != nil {
			return fmt.Errorf("%w: %w", ErrStateInvalid, err)
		}

		if _, dup := r.sites[site.User]; dup {
			return fmt.Errorf("%w: duplicate site %q", ErrStateInvalid, site.User)
		}

		if other, dup := uids[site.UID]; dup {
			return fmt.Errorf("%w: sites %q and %q share uid %d", ErrStateInvalid, other, site.User, site.UID)
		}

		if _, ok := r.pools[site.Pool]; !ok {
			return fmt.Errorf("%w: site %q uses unknown pool %q", ErrStateInvalid, site.User, site.Pool)
		}

		uids[site.UID] = site.User
		r.sites[site.User] = site
	}

	// Without the field the default is the pool called "default", as
	// before addendum 2, even if no such pool exists (then a site must
	// name its pool). A default set explicitly must be registered.
	r.def = agentapi.DefaultPool
	if s.Default != "" {
		if _, ok := r.pools[s.Default]; !ok {
			return fmt.Errorf("%w: default pool %q is not registered", ErrStateInvalid, s.Default)
		}

		r.def = s.Default
	}

	return nil
}

func validatePool(p Pool) error {
	if err := agentapi.ValidatePoolName(p.Name); err != nil {
		return err
	}

	if !filepath.IsAbs(p.Path) || filepath.Clean(p.Path) != p.Path || p.Path == "/" || !poolPathRe.MatchString(p.Path) {
		return fmt.Errorf("pool %q: path must be absolute, clean, not / and only letters, digits and _ . / -", p.Name)
	}

	if strings.ContainsAny(p.ID, "\r\n\x00") || len(p.ID) > 128 {
		return fmt.Errorf("pool %q: invalid id", p.Name)
	}

	return nil
}

func validateSite(s Site) error {
	if err := agentapi.ValidateSiteUser(s.User); err != nil {
		return err
	}

	if s.UID == 0 || s.GID == 0 {
		return fmt.Errorf("site %q: uid and gid must not be 0", s.User)
	}

	return agentapi.ValidatePoolName(s.Pool)
}

// save writes the registry atomically: temp file, fsync, rename, fsync of the
// directory. A crash leaves either the old or the new file.
func (r *Registry) save() error {
	s := state{Version: stateVersion, Pools: []Pool{}, Sites: []Site{}}

	if r.def != agentapi.DefaultPool {
		s.Default = r.def
	}

	for _, p := range r.pools {
		s.Pools = append(s.Pools, p)
	}

	for _, site := range r.sites {
		s.Sites = append(s.Sites, site)
	}

	sort.Slice(s.Pools, func(i, j int) bool { return s.Pools[i].Name < s.Pools[j].Name })
	sort.Slice(s.Sites, func(i, j int) bool { return s.Sites[i].User < s.Sites[j].User })

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("sites: encode state: %w", err)
	}

	dir := filepath.Dir(r.path)
	tmp := filepath.Join(dir, "."+StateFile+".tmp")

	// A stale temp file from a crash is replaced, never reused or followed.
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("sites: write state: %w", err)
	}

	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)

		return fmt.Errorf("sites: write state: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)

		return fmt.Errorf("sites: sync state: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("sites: close state: %w", err)
	}

	if err := os.Rename(tmp, r.path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("sites: replace state: %w", err)
	}

	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("sites: open state directory: %w", err)
	}

	defer d.Close()

	if err := d.Sync(); err != nil {
		return fmt.Errorf("sites: sync state directory: %w", err)
	}

	return nil
}

// Pool returns the registered pool.
func (r *Registry) Pool(name string) (Pool, bool) {
	p, ok := r.pools[name]

	return p, ok
}

// DefaultPool is the name of the pool a new site lands in when the request
// names none.
func (r *Registry) DefaultPool() string { return r.def }

// Pools returns all registered pools sorted by name.
func (r *Registry) Pools() []Pool {
	out := make([]Pool, 0, len(r.pools))
	for _, p := range r.pools {
		out = append(out, p)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// Site returns the registered site.
func (r *Registry) Site(user string) (Site, bool) {
	s, ok := r.sites[user]

	return s, ok
}

// List returns all sites sorted by user.
func (r *Registry) List() []Site {
	out := make([]Site, 0, len(r.sites))
	for _, s := range r.sites {
		out = append(out, s)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].User < out[j].User })

	return out
}

// add records a site and persists it. On a write failure the in-memory state
// is reverted, so memory and disk never disagree.
func (r *Registry) add(s Site) error {
	for _, other := range r.sites {
		if other.UID == s.UID {
			return fmt.Errorf("sites: uid %d already registered for %q", s.UID, other.User)
		}
	}

	r.sites[s.User] = s

	if err := r.save(); err != nil {
		delete(r.sites, s.User)

		return err
	}

	return nil
}

func (r *Registry) remove(user string) error {
	old, ok := r.sites[user]
	if !ok {
		return nil
	}

	delete(r.sites, user)

	if err := r.save(); err != nil {
		r.sites[user] = old

		return err
	}

	return nil
}

// addPool records a new pool and persists it; on a write failure memory is
// reverted.
func (r *Registry) addPool(p Pool) error {
	if err := validatePool(p); err != nil {
		return fmt.Errorf("%w: %w", ErrStateInvalid, err)
	}

	if _, ok := r.pools[p.Name]; ok {
		return fmt.Errorf("sites: pool %q already registered", p.Name)
	}

	r.pools[p.Name] = p

	if err := r.save(); err != nil {
		delete(r.pools, p.Name)

		return err
	}

	return nil
}

// removePool drops a pool no site uses and persists it.
func (r *Registry) removePool(name string) error {
	old, ok := r.pools[name]
	if !ok {
		return nil
	}

	for _, s := range r.sites {
		if s.Pool == name {
			return fmt.Errorf("sites: pool %q is used by site %q", name, s.User)
		}
	}

	delete(r.pools, name)

	if err := r.save(); err != nil {
		r.pools[name] = old

		return err
	}

	return nil
}

// renamePool gives a pool a new name and moves its sites and, if it is the
// default, the default along. The path, the marker and the files stay. On a
// write failure memory is reverted.
func (r *Registry) renamePool(name, newName string) (Pool, error) {
	p, ok := r.pools[name]
	if !ok {
		return Pool{}, ErrNotFound
	}

	if _, taken := r.pools[newName]; taken {
		return Pool{}, ErrPoolExists
	}

	oldPools, oldSites, oldDef := r.pools, r.sites, r.def

	pools := make(map[string]Pool, len(r.pools))
	for k, v := range r.pools {
		pools[k] = v
	}

	delete(pools, name)

	p.Name = newName
	pools[newName] = p

	sites := make(map[string]Site, len(r.sites))

	for k, v := range r.sites {
		if v.Pool == name {
			v.Pool = newName
		}

		sites[k] = v
	}

	r.pools, r.sites = pools, sites
	if r.def == name {
		r.def = newName
	}

	if err := r.save(); err != nil {
		r.pools, r.sites, r.def = oldPools, oldSites, oldDef

		return Pool{}, err
	}

	return p, nil
}

// setDefault makes a registered pool the default and persists it.
func (r *Registry) setDefault(name string) (Pool, error) {
	p, ok := r.pools[name]
	if !ok {
		return Pool{}, ErrNotFound
	}

	if r.def == name {
		return p, nil
	}

	old := r.def
	r.def = name

	if err := r.save(); err != nil {
		r.def = old

		return Pool{}, err
	}

	return p, nil
}
