// SPDX-License-Identifier: AGPL-3.0-or-later

package deployunit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// The switch and the prune work in the site's home, which the site user
// can change at any time: every path goes through an os.Root of the home,
// so no link leads the agent out of it.

const switchTmp = ".hostashi-switch"

// releaseTarget is the link target of the deploy directory dir for a
// release, relative to dir's parent: the home stays movable.
func releaseTarget(dir, release string) string {
	return strings.Repeat("../", strings.Count(dir, "/")) + agentapi.DeployReleasesDir + "/" + release
}

// liveRelease is the release the deploy directory links to; "" when it is
// no link to a release.
func liveRelease(root *os.Root, dir string) string {
	target, err := root.Readlink(dir)
	if err != nil {
		return ""
	}

	prefix := strings.Repeat("../", strings.Count(dir, "/")) + agentapi.DeployReleasesDir + "/"

	r, ok := strings.CutPrefix(target, prefix)
	if !ok || agentapi.ValidateRelease(r) != nil {
		return ""
	}

	return r
}

// Switch makes a release live (site.deploy.switch): the deploy directory
// becomes a link to it, in one rename. On the first switch the directory's
// own files become the release InitialRelease, so a rollback can bring
// them back.
func (m *Manager) Switch(_ context.Context, req agentapi.DeploySwitchRequest) (agentapi.DeploySwitchResult, error) {
	if err := req.Validate(); err != nil {
		return agentapi.DeploySwitchResult{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.DeploySwitchResult{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	root, err := os.OpenRoot(acct.Home)
	if err != nil {
		return agentapi.DeploySwitchResult{}, fmt.Errorf("deployunit: %w", err)
	}
	defer root.Close()

	var out agentapi.DeploySwitchResult

	fi, err := root.Lstat(req.Dir)

	// The initial release is made below from the directory itself.
	if err != nil || !fi.IsDir() || req.Release != agentapi.InitialRelease {
		if ri, err := root.Lstat(agentapi.DeployReleasesDir + "/" + req.Release); err != nil || !ri.IsDir() {
			return out, fmt.Errorf("%w: %s", ErrRelease, req.Release)
		}
	}

	switch {
	case errors.Is(err, fs.ErrNotExist):
		if parent := path.Dir(req.Dir); parent != "." {
			if pi, err := root.Lstat(parent); err != nil || !pi.IsDir() {
				return out, fmt.Errorf("%w: %s is missing", ErrDeployDir, parent)
			}
		}
	case err != nil:
		return out, fmt.Errorf("deployunit: %w", err)
	case fi.Mode()&fs.ModeSymlink != 0:
		out.Previous = liveRelease(root, req.Dir)
	case fi.IsDir():
		// The first deploy: the directory's files are kept as a release.
		initial := agentapi.DeployReleasesDir + "/" + agentapi.InitialRelease
		if _, err := root.Lstat(initial); !errors.Is(err, fs.ErrNotExist) {
			return out, fmt.Errorf("%w: %s exists already", ErrDeployDir, agentapi.InitialRelease)
		}

		if err := root.Rename(req.Dir, initial); err != nil {
			return out, fmt.Errorf("deployunit: %w", err)
		}

		out.Previous = agentapi.InitialRelease
	default:
		return out, fmt.Errorf("%w: %s is no directory", ErrDeployDir, req.Dir)
	}

	tmp := req.Dir + switchTmp
	_ = root.Remove(tmp)

	if err := root.Symlink(releaseTarget(req.Dir, req.Release), tmp); err != nil {
		return out, fmt.Errorf("deployunit: %w", err)
	}

	// The link is the site user's, like everything else in the home.
	if err := root.Lchown(tmp, int(acct.UID), int(acct.GID)); err != nil {
		_ = root.Remove(tmp)

		return out, fmt.Errorf("deployunit: %w", err)
	}

	if err := root.Rename(tmp, req.Dir); err != nil {
		_ = root.Remove(tmp)

		return out, fmt.Errorf("deployunit: %w", err)
	}

	return out, nil
}

// Prune deletes the releases of a site that are neither in Keep nor live,
// with their results (site.deploy.prune). While a deploy runs it deletes
// nothing: its new release is not live yet.
func (m *Manager) Prune(ctx context.Context, req agentapi.DeployPruneRequest) (agentapi.DeployReleases, error) {
	if err := req.Validate(); err != nil {
		return agentapi.DeployReleases{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.DeployReleases{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	busy, err := m.running(ctx, req.Site)
	if err != nil {
		return agentapi.DeployReleases{}, err
	}

	root, err := os.OpenRoot(acct.Home)
	if err != nil {
		return agentapi.DeployReleases{}, fmt.Errorf("deployunit: %w", err)
	}
	defer root.Close()

	out := agentapi.DeployReleases{Releases: []string{}, Live: liveRelease(root, req.Dir)}

	all, err := releases(root)
	if err != nil {
		return out, err
	}

	for _, r := range all {
		if busy || r == out.Live || slices.Contains(req.Keep, r) {
			out.Releases = append(out.Releases, r)

			continue
		}

		if err := root.RemoveAll(agentapi.DeployReleasesDir + "/" + r); err != nil {
			return out, fmt.Errorf("deployunit: release %s: %w", r, err)
		}

		_ = root.Remove(agentapi.DeployResultsDir + "/" + r + ".json")
		m.log().InfoContext(ctx, "deploy release deleted", "site", req.Site, "release", r)
	}

	return out, nil
}

// releases lists the release directories; other names are left alone.
func releases(root *os.Root) ([]string, error) {
	f, err := root.Open(agentapi.DeployReleasesDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("deployunit: %w", err)
	}

	defer f.Close()

	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("deployunit: %w", err)
	}

	var out []string

	for _, e := range entries {
		if e.IsDir() && agentapi.ValidateRelease(e.Name()) == nil {
			out = append(out, e.Name())
		}
	}

	slices.Sort(out)

	return out, nil
}
