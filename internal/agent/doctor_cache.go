// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/phpctl"
)

// valkey checks that Valkey is installed and that the package's own
// service, which would listen on 127.0.0.1:6379 for every local user,
// stays masked (ADR-0045).
func (d *doctorOps) valkey(ctx context.Context) agentapi.DoctorCheck {
	const id = "service.valkey"

	if _, err := os.Stat(orDefault(d.env.ValkeyBinary, "/usr/bin/valkey-server")); err != nil {
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorWarn, Summary: "Valkey is not installed", Hint: "Sites cannot have an object cache. Run the installer again."}
	}

	read := d.env.ValkeyDefault
	if read == nil {
		read = phpctl.ValkeyDefaultState
	}

	active, file, err := read(ctx)

	switch {
	case err != nil:
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorFail, Summary: "cannot read " + phpctl.ValkeyDefaultUnit + ": " + err.Error()}
	case active == "active":
		return agentapi.DoctorCheck{
			ID: id, Status: agentapi.DoctorFail, Summary: "the shared " + phpctl.ValkeyDefaultUnit + " runs: every local user reaches it on 127.0.0.1:6379",
			Hint: "Run: systemctl disable --now " + phpctl.ValkeyDefaultUnit + " && systemctl mask " + phpctl.ValkeyDefaultUnit,
		}
	case file != "masked":
		return agentapi.DoctorCheck{
			ID: id, Status: agentapi.DoctorWarn, Summary: phpctl.ValkeyDefaultUnit + " is not masked, a package update could start it",
			Hint: "Run the installer again, or: systemctl mask " + phpctl.ValkeyDefaultUnit,
		}
	}

	return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorOK, Summary: "Valkey is installed, the shared service is masked"}
}

// cache checks a site's object cache: the unit runs (unless the site is
// disabled) and its socket directory belongs to the site user alone. A site
// without a cache has no check.
func (d *doctorOps) cache(ctx context.Context, user string, uid uint32) (agentapi.DoctorCheck, bool) {
	const id = "site.cache"

	if d.h.objc == nil {
		return agentapi.DoctorCheck{}, false
	}

	st, err := d.h.objc.Status(ctx, user)

	switch {
	case err != nil:
		return agentapi.DoctorCheck{ID: id, Site: user, Status: agentapi.DoctorFail, Summary: "cannot read the object cache: " + err.Error()}, true
	case st.MemoryMB == 0:
		return agentapi.DoctorCheck{}, false
	case st.Paused:
		return agentapi.DoctorCheck{ID: id, Site: user, Status: agentapi.DoctorOK, Summary: "the object cache is stopped while the site is disabled"}, true
	case st.State != "active":
		return agentapi.DoctorCheck{
			ID: id, Site: user, Status: agentapi.DoctorFail, Summary: "the object cache does not run (" + orDefault(st.State, "unknown") + ")",
			Hint: "Check: journalctl -u hostashi-valkey@" + user + ".service. Turning the cache off and on again rewrites it.",
		}, true
	}

	dir := filepath.Dir(st.Socket)

	fi, err := os.Lstat(dir)
	if err != nil {
		return agentapi.DoctorCheck{ID: id, Site: user, Status: agentapi.DoctorFail, Summary: "the cache's socket directory is missing: " + err.Error()}, true
	}

	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 || !ok || sys.Uid != uid {
		return agentapi.DoctorCheck{
			ID: id, Site: user, Status: agentapi.DoctorFail, Summary: dir + " is reachable by other users (" + fi.Mode().String() + ")",
			Hint: "Restart the cache: systemd creates the directory with mode 0700 for the site user.",
		}, true
	}

	return agentapi.DoctorCheck{ID: id, Site: user, Status: agentapi.DoctorOK, Summary: "the object cache runs, only the site reaches it"}, true
}
