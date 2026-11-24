// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gitsync fetches one branch of a site's repository into a bare
// cache and writes one commit's files into a new release directory
// (ADR-0062). It runs as `hostashi git-sync` inside the site's deploy unit,
// as the site user, never as root.
//
// Git is spoken by go-git; nothing is executed. The file:// and git://
// transports are removed at start (go-git's file transport would run
// git-upload-pack), so only https and ssh remain, and the address is
// checked with agentapi.ParseGitURL before anything connects.
//
// The release is not written by go-git's worktree: the commit's tree is
// walked and every file is created through an os.Root of the release, so
// no entry can leave it. Symbolic links are written only when their target
// stays inside the release. The release gets no .git; the commit is
// returned to the caller.
package gitsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func init() {
	// Only https and ssh: the file transport starts git-upload-pack, git://
	// is unauthenticated, http is unencrypted.
	for _, scheme := range []string{"file", "git", "http"} {
		client.InstallProtocol(scheme, nil)
	}
}

// Limits of one release.
const (
	// MaxFiles bounds the entries of one commit's tree.
	MaxFiles = 200_000
	// MaxFileSize bounds one file; Git LFS is not supported, so large
	// files are rare and usually a mistake.
	MaxFileSize = 512 << 20
	// remoteName is the name of the cache's only remote.
	remoteName = "origin"
	// tokenUser is sent with a token when no user is set; GitHub, GitLab,
	// Gitea and Forgejo accept any user with a token.
	tokenUser = "hostashi"
)

// Auth is how the repository is reached.
type Auth struct {
	// Kind is one of agentapi.GitAuth*.
	Kind string
	// TokenUser and Token for agentapi.GitAuthToken.
	TokenUser string
	Token     string
	// Key is the OpenSSH private key and HostKey the confirmed host key
	// ("type base64") for agentapi.GitAuthSSH.
	Key     []byte
	HostKey string
}

// Options describe one sync.
type Options struct {
	// URL is the repository; it is checked with agentapi.ParseGitURL.
	URL    string
	Branch string
	// Commit, when set, must be the branch head or reachable from the
	// fetched history; empty takes the head.
	Commit string
	Auth   Auth
	// Cache is the bare repository directory; it is created when missing.
	Cache string
	// Release is the new release directory; it must not exist.
	Release string
	// Shared are paths below the release that become links to the same
	// path below SharedDir, which is created as needed.
	Shared    []string
	SharedDir string
	// Depth is the fetch depth, 0 for 1.
	Depth int
	// Progress receives Git's progress lines; nil for none.
	Progress io.Writer

	// allowLocal lets tests use a local transport; fullFetch fetches
	// without depth, as go-git's in-process server has no shallow support.
	allowLocal bool
	fullFetch  bool
}

// Result is the deployed commit.
type Result struct {
	Commit  string    `json:"commit"`
	Author  string    `json:"author"`
	Message string    `json:"message"`
	When    time.Time `json:"when"`
	Files   int       `json:"files"`
}

// ErrHostKey: an SSH repository without a confirmed host key, or one whose
// host key changed.
var ErrHostKey = errors.New("gitsync: the host key of the repository is not the confirmed one")

// Sync fetches the branch into the cache and writes the commit's files into
// the release. On an error the release may be partly written; the caller
// removes it.
func Sync(ctx context.Context, o Options) (Result, error) {
	if err := o.check(); err != nil {
		return Result{}, err
	}

	auth, err := o.Auth.method(o.URL)
	if err != nil {
		return Result{}, err
	}

	repo, err := openCache(o.Cache, o.URL)
	if err != nil {
		return Result{}, err
	}

	depth := o.Depth
	if depth <= 0 {
		depth = 1
	}

	if o.fullFetch {
		depth = 0
	}

	ref := plumbing.NewBranchReferenceName(o.Branch)
	remoteRef := plumbing.NewRemoteReferenceName(remoteName, o.Branch)

	err = repo.FetchContext(ctx, &git.FetchOptions{
		RemoteName: remoteName,
		RefSpecs:   []config.RefSpec{config.RefSpec("+" + ref.String() + ":" + remoteRef.String())},
		Depth:      depth,
		Auth:       auth,
		Progress:   o.Progress,
		Tags:       git.NoTags,
		Force:      true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return Result{}, fmt.Errorf("gitsync: fetch %s: %w", o.Branch, err)
	}

	head, err := repo.Reference(remoteRef, true)
	if err != nil {
		return Result{}, fmt.Errorf("gitsync: branch %s: %w", o.Branch, err)
	}

	hash := head.Hash()
	if o.Commit != "" {
		hash = plumbing.NewHash(o.Commit)
	}

	commit, err := repo.CommitObject(hash)
	if err != nil {
		return Result{}, fmt.Errorf("gitsync: commit %s: %w", hash, err)
	}

	n, err := writeRelease(ctx, repo.Storer, commit, o.Release)
	if err != nil {
		return Result{}, err
	}

	if err := linkShared(o.Release, o.SharedDir, o.Shared); err != nil {
		return Result{}, err
	}

	msg, _, _ := strings.Cut(strings.TrimSpace(commit.Message), "\n")

	return Result{
		Commit: commit.Hash.String(), Author: commit.Author.Name, Message: truncate(msg, 1024),
		When: commit.Author.When.UTC(), Files: n,
	}, nil
}

func (o Options) check() error {
	if !o.allowLocal {
		if _, err := agentapi.ParseGitURL(o.URL); err != nil {
			return fmt.Errorf("gitsync: %w", err)
		}
	}

	if err := agentapi.ValidateGitBranch(o.Branch); err != nil {
		return fmt.Errorf("gitsync: %w", err)
	}

	if o.Commit != "" && !isHash(o.Commit) {
		return fmt.Errorf("gitsync: commit %q is not a full SHA-1", o.Commit)
	}

	if err := agentapi.ValidateGitShared(o.Shared); err != nil {
		return fmt.Errorf("gitsync: %w", err)
	}

	if !path.IsAbs(o.Cache) || !path.IsAbs(o.Release) || len(o.Shared) > 0 && !path.IsAbs(o.SharedDir) {
		return errors.New("gitsync: cache, release and shared directory must be absolute")
	}

	return nil
}

func isHash(s string) bool {
	if len(s) != 40 {
		return false
	}

	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}

	return true
}

// method builds the go-git authentication. SSH checks the server's key
// against the confirmed one only; there is no known_hosts and no agent.
func (a Auth) method(url string) (transport.AuthMethod, error) {
	switch a.Kind {
	case agentapi.GitAuthNone, "":
		return nil, nil
	case agentapi.GitAuthToken:
		user := a.TokenUser
		if user == "" {
			user = tokenUser
		}

		return &githttp.BasicAuth{Username: user, Password: a.Token}, nil
	case agentapi.GitAuthSSH:
		rem, err := agentapi.ParseGitURL(url)
		if err != nil {
			return nil, fmt.Errorf("gitsync: %w", err)
		}

		if a.HostKey == "" {
			return nil, ErrHostKey
		}

		hk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(a.HostKey))
		if err != nil {
			return nil, fmt.Errorf("gitsync: confirmed host key: %w", err)
		}

		keys, err := gitssh.NewPublicKeys(rem.User, a.Key, "")
		if err != nil {
			return nil, fmt.Errorf("gitsync: deploy key: %w", err)
		}

		fixed := ssh.FixedHostKey(hk)
		keys.HostKeyCallback = func(host string, remote net.Addr, key ssh.PublicKey) error {
			if err := fixed(host, remote, key); err != nil {
				return fmt.Errorf("%w (%s)", ErrHostKey, ssh.FingerprintSHA256(key))
			}

			return nil
		}
		keys.HostKeyAlgorithms = []string{hk.Type()}

		return keys, nil
	default:
		return nil, fmt.Errorf("gitsync: unknown auth %q", a.Kind)
	}
}

// openCache opens the bare cache, or creates it, and points its remote at
// url.
func openCache(dir, url string) (*git.Repository, error) {
	repo, err := git.PlainOpen(dir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("gitsync: cache: %w", err)
		}

		repo, err = git.PlainInit(dir, true)
	}

	if err != nil {
		return nil, fmt.Errorf("gitsync: cache %s: %w", dir, err)
	}

	cfg, err := repo.Config()
	if err != nil {
		return nil, fmt.Errorf("gitsync: cache config: %w", err)
	}

	if r, ok := cfg.Remotes[remoteName]; !ok || len(r.URLs) != 1 || r.URLs[0] != url {
		cfg.Remotes[remoteName] = &config.RemoteConfig{Name: remoteName, URLs: []string{url}}
		if err := repo.SetConfig(cfg); err != nil {
			return nil, fmt.Errorf("gitsync: cache config: %w", err)
		}
	}

	return repo, nil
}

// writeRelease creates dir and writes the commit's tree into it through an
// os.Root.
func writeRelease(ctx context.Context, st storer.EncodedObjectStorer, c *object.Commit, dir string) (int, error) {
	if err := os.Mkdir(dir, 0o755); err != nil {
		return 0, fmt.Errorf("gitsync: release: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, fmt.Errorf("gitsync: release: %w", err)
	}
	defer root.Close()

	tree, err := c.Tree()
	if err != nil {
		return 0, fmt.Errorf("gitsync: tree: %w", err)
	}

	n := 0
	walker := object.NewTreeWalker(tree, true, nil)

	defer walker.Close()

	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}

		name, entry, err := walker.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return n, fmt.Errorf("gitsync: tree: %w", err)
		}

		if n++; n > MaxFiles {
			return n, fmt.Errorf("gitsync: more than %d entries", MaxFiles)
		}

		if err := checkEntryName(name); err != nil {
			return n, err
		}

		if err := writeEntry(root, st, name, entry); err != nil {
			return n, err
		}
	}

	return n, nil
}

// checkEntryName refuses names Git itself refuses on checkout and anything
// that is not a clean relative path.
func checkEntryName(name string) error {
	if name == "" || path.Clean(name) != name || path.IsAbs(name) || strings.ContainsRune(name, 0) || strings.ContainsRune(name, '\\') {
		return fmt.Errorf("gitsync: unsafe path %q in the repository", name)
	}

	for _, seg := range strings.Split(name, "/") {
		if seg == ".." || strings.EqualFold(seg, ".git") {
			return fmt.Errorf("gitsync: unsafe path %q in the repository", name)
		}
	}

	return nil
}

func writeEntry(root *os.Root, st storer.EncodedObjectStorer, name string, e object.TreeEntry) error {
	switch e.Mode {
	case filemode.Dir:
		if err := root.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("gitsync: %s: %w", name, err)
		}

		return nil
	case filemode.Submodule:
		// Submodules come later (ADR-0062); the folder stays empty.
		if err := root.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("gitsync: %s: %w", name, err)
		}

		return nil
	case filemode.Regular, filemode.Deprecated, filemode.Executable:
		return writeFile(root, st, name, e)
	case filemode.Symlink:
		return writeLink(root, st, name, e)
	case filemode.Empty:
		return fmt.Errorf("gitsync: %s: entry without mode", name)
	default:
		return fmt.Errorf("gitsync: %s: unsupported mode %s", name, e.Mode)
	}
}

func writeFile(root *os.Root, st storer.EncodedObjectStorer, name string, e object.TreeEntry) error {
	blob, err := object.GetBlob(st, e.Hash)
	if err != nil {
		return fmt.Errorf("gitsync: %s: %w", name, err)
	}

	if blob.Size > MaxFileSize {
		return fmt.Errorf("gitsync: %s is larger than %d MiB (Git LFS is not supported)", name, MaxFileSize>>20)
	}

	mode := os.FileMode(0o644)
	if e.Mode == filemode.Executable {
		mode = 0o755
	}

	r, err := blob.Reader()
	if err != nil {
		return fmt.Errorf("gitsync: %s: %w", name, err)
	}
	defer r.Close()

	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("gitsync: %s: %w", name, err)
	}

	if _, err := io.Copy(f, io.LimitReader(r, MaxFileSize+1)); err != nil {
		_ = f.Close()

		return fmt.Errorf("gitsync: %s: %w", name, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("gitsync: %s: %w", name, err)
	}

	return nil
}

// writeLink writes a symbolic link whose target, read from where the link
// is, stays inside the release.
func writeLink(root *os.Root, st storer.EncodedObjectStorer, name string, e object.TreeEntry) error {
	blob, err := object.GetBlob(st, e.Hash)
	if err != nil {
		return fmt.Errorf("gitsync: %s: %w", name, err)
	}

	if blob.Size > 4096 {
		return fmt.Errorf("gitsync: link %s: target too long", name)
	}

	r, err := blob.Reader()
	if err != nil {
		return fmt.Errorf("gitsync: %s: %w", name, err)
	}

	raw, err := io.ReadAll(r)
	_ = r.Close()

	if err != nil {
		return fmt.Errorf("gitsync: %s: %w", name, err)
	}

	target := string(raw)
	if !linkInside(name, target) {
		return fmt.Errorf("gitsync: link %s points out of the release (%q)", name, target)
	}

	if err := root.Symlink(target, name); err != nil {
		return fmt.Errorf("gitsync: %s: %w", name, err)
	}

	return nil
}

// linkInside reports whether a relative link target, resolved from the
// link's folder, stays inside the release and avoids .git.
func linkInside(name, target string) bool {
	if target == "" || path.IsAbs(target) || strings.ContainsRune(target, 0) {
		return false
	}

	p := path.Join(path.Dir(name), target)
	if p == ".." || strings.HasPrefix(p, "../") {
		return false
	}

	for _, seg := range strings.Split(p, "/") {
		if strings.EqualFold(seg, ".git") {
			return false
		}
	}

	return true
}

// linkShared makes each shared path a link into sharedDir. Whatever the
// commit has at that path is moved into sharedDir the first time, so a
// repository's default .env or uploads folder becomes the starting point;
// later deploys drop the commit's copy.
func linkShared(release, sharedDir string, shared []string) error {
	if len(shared) == 0 {
		return nil
	}

	if err := os.MkdirAll(sharedDir, 0o750); err != nil {
		return fmt.Errorf("gitsync: shared: %w", err)
	}

	rel, err := os.OpenRoot(release)
	if err != nil {
		return fmt.Errorf("gitsync: shared: %w", err)
	}
	defer rel.Close()

	sh, err := os.OpenRoot(sharedDir)
	if err != nil {
		return fmt.Errorf("gitsync: shared: %w", err)
	}
	defer sh.Close()

	for _, p := range shared {
		if err := linkOne(rel, sh, release, sharedDir, p); err != nil {
			return err
		}
	}

	return nil
}

func linkOne(rel, sh *os.Root, release, sharedDir, p string) error {
	if dir := path.Dir(p); dir != "." {
		if err := rel.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("gitsync: shared %s: %w", p, err)
		}

		if err := sh.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("gitsync: shared %s: %w", p, err)
		}
	}

	_, inShared := sh.Lstat(p)
	fi, inRelease := rel.Lstat(p)

	switch {
	case inRelease == nil && errors.Is(inShared, fs.ErrNotExist) && fi.Mode()&fs.ModeSymlink == 0:
		// First deploy with this path: keep the commit's version.
		if err := os.Rename(path.Join(release, p), path.Join(sharedDir, p)); err != nil {
			return fmt.Errorf("gitsync: shared %s: %w", p, err)
		}
	case inRelease == nil:
		if err := rel.RemoveAll(p); err != nil {
			return fmt.Errorf("gitsync: shared %s: %w", p, err)
		}
	}

	// The link is absolute: the release moves under its own name only.
	if err := rel.Symlink(path.Join(sharedDir, p), p); err != nil {
		return fmt.Errorf("gitsync: shared %s: %w", p, err)
	}

	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}

	return s
}
