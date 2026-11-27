// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/phpctl"
)

// valkey checks that Valkey is installed and that the package's own
// service, which would listen on 127.0.0.1:6379 for every local user,
// stays masked (ADR-0045).
func (d *doctorOps) valkey(ctx context.Context) agentapi.DoctorCheck {
	const id = "service.valkey"

	if _, err := os.Stat(orDefault(d.env.ValkeyBinary, "/usr/bin/valkey-server")); err != nil {
		return doctor.New(agentapi.DoctorWarn, id, "valkey.missing", "valkey.missing.hint", nil)
	}

	read := d.env.ValkeyDefault
	if read == nil {
		read = phpctl.ValkeyDefaultState
	}

	active, file, err := read(ctx)

	switch {
	case err != nil:
		return doctor.New(agentapi.DoctorFail, id, "read.error", "", doctor.P{"path": phpctl.ValkeyDefaultUnit, "error": err.Error()})
	case active == "active":
		return doctor.New(agentapi.DoctorFail, id, "valkey.shared", "valkey.shared.hint", doctor.P{"unit": phpctl.ValkeyDefaultUnit})
	case file != "masked":
		return doctor.New(agentapi.DoctorWarn, id, "valkey.unmasked", "valkey.unmasked.hint", doctor.P{"unit": phpctl.ValkeyDefaultUnit})
	}

	return doctor.New(agentapi.DoctorOK, id, "valkey.ok", "", nil)
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
		return doctor.NewSite(agentapi.DoctorFail, id, user, "cache.error", "", doctor.P{"error": err.Error()}), true
	case st.MemoryMB == 0:
		return agentapi.DoctorCheck{}, false
	case st.Paused:
		return doctor.NewSite(agentapi.DoctorOK, id, user, "cache.paused", "", nil), true
	case st.State != "active":
		return doctor.NewSite(agentapi.DoctorFail, id, user, "cache.down", "cache.down.hint", doctor.P{"state": orDefault(st.State, "unknown"), "state@de": orDefault(st.State, "unbekannt"), "site": user}), true
	}

	dir := filepath.Dir(st.Socket)

	fi, err := os.Lstat(dir)
	if err != nil {
		return doctor.NewSite(agentapi.DoctorFail, id, user, "cache.nodir", "", doctor.P{"error": err.Error()}), true
	}

	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 || !ok || sys.Uid != uid {
		return doctor.NewSite(agentapi.DoctorFail, id, user, "cache.open", "cache.open.hint", doctor.P{"path": dir, "mode": fi.Mode().String()}), true
	}

	return doctor.NewSite(agentapi.DoctorOK, id, user, "cache.ok", "", nil), true
}
