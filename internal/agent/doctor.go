// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/certs"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/phpctl"
	"github.com/maki-it/hostashi/internal/web"
)

// Fail2banSocket is the fail2ban server's socket (ADR-0021).
const Fail2banSocket = "/run/fail2ban/fail2ban.sock"

// DoctorEnv is what doctor.run reads besides the managers. The zero value
// reads the server's real paths.
type DoctorEnv struct {
	System         doctor.System
	PanelSocket    string   // default PanelSocket
	Fail2banSocket string   // default Fail2banSocket
	Disk           []string // file systems to check besides the pools; default / and the state directories
	// Hardening reads the files of the hardening baseline (ADR-0033). The
	// zero value reads the server and asks systemd through phpctl.
	Hardening doctor.Hardening
	Now       func() time.Time
}

type doctorOps struct {
	env      DoctorEnv
	stateDir string
	h        *siteOps // nil without the site operations
}

func registerDoctor(s *agentrpc.Server, env DoctorEnv, stateDir string, h *siteOps) {
	d := &doctorOps{env: env, stateDir: stateDir, h: h}

	agentrpc.Handle(s, agentapi.Doctor, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.DoctorRequest) (agentapi.DoctorReport, error) {
		return d.run(ctx), nil
	})
}

func (d *doctorOps) now() time.Time {
	if d.env.Now == nil {
		return time.Now()
	}

	return d.env.Now()
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// run never fails as a whole: a check that cannot run reports that.
func (d *doctorOps) run(ctx context.Context) agentapi.DoctorReport {
	sys := d.env.System
	out := []agentapi.DoctorCheck{sys.OS()}

	var pools []string

	if d.h != nil {
		for _, p := range d.h.m.Pools() {
			pools = append(pools, p.Path)
		}
	}

	disk := d.env.Disk
	if disk == nil {
		disk = []string{"/", "/var/lib/hostashi", orDefault(d.stateDir, DefaultStateDir)}
	}

	out = append(out, sys.Disk(append(disk, pools...))...)

	for _, p := range pools {
		if d.h.quota == nil {
			break
		}

		ok, err := d.h.quota.Available(p)
		out = append(out, doctor.Quota(p, ok, err))
	}

	out = append(out, sys.Proc())
	for _, p := range pools {
		out = append(out, doctor.PoolMode(p))
	}

	out = append(out, doctor.Panel(ctx, orDefault(d.env.PanelSocket, PanelSocket)))
	out = append(out, d.nginx(ctx))
	out = append(out, d.mariadb(ctx))
	out = append(out, doctor.Socket(ctx, "service.fail2ban", "fail2ban", orDefault(d.env.Fail2banSocket, Fail2banSocket),
		"Logins are not protected against brute force. Check: systemctl status fail2ban."))

	hard := d.env.Hardening
	if hard.Active == nil {
		hard.Active = phpctl.UnitActive
	}

	if hard.Now == nil {
		hard.Now = d.now
	}

	out = append(out, hard.Firewall(ctx), hard.Updates(), hard.Reboot(), hard.PHP(), hard.SSH(), hard.Sysctl())

	if d.h != nil {
		out = append(out, d.sites()...)
	}

	return agentapi.DoctorReport{Checks: out}
}

func (d *doctorOps) nginx(ctx context.Context) agentapi.DoctorCheck {
	const id = "service.nginx"

	if d.h == nil || d.h.web == nil {
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorFail, Summary: "nginx is not set up", Hint: "Run the installer again."}
	}

	h, err := d.h.web.Health(ctx)
	if errors.Is(err, web.ErrBusy) {
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorWarn, Summary: "the configuration is being changed right now", Hint: "Run hostashi doctor again in a moment."}
	}

	if err != nil {
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorFail, Summary: "cannot read the web state: " + err.Error()}
	}

	return nginxCheck(h)
}

func nginxCheck(h web.Health) agentapi.DoctorCheck {
	const id = "service.nginx"

	switch {
	case h.Current == 0:
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorWarn, Summary: "no configuration has been applied yet", Hint: "Run hostashi web sync, or save a site in the panel."}
	case h.LiveErr != nil:
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorFail, Summary: "nginx does not answer: " + h.LiveErr.Error(), Hint: "Check: systemctl status nginx, journalctl -u nginx."}
	case h.Live != h.Current:
		return agentapi.DoctorCheck{
			ID: id, Status: agentapi.DoctorFail,
			Summary: fmt.Sprintf("nginx serves generation %d, the current one is %d", h.Live, h.Current),
			Hint:    "nginx did not load the current configuration. Run hostashi web sync.",
		}
	case len(h.Drift) > 0:
		return agentapi.DoctorCheck{
			ID: id, Status: agentapi.DoctorWarn,
			Summary: "files of the configuration were changed by hand: " + strings.Join(h.Drift, ", "),
			Hint:    "The next change replaces them. Use the panel's nginx snippets for your own settings.",
		}
	case !h.Confirmed:
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorWarn, Summary: "generation " + strconv.Itoa(h.Current) + " was never confirmed", Hint: "Run hostashi web sync."}
	}

	return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorOK, Summary: "nginx serves generation " + strconv.Itoa(h.Live)}
}

func (d *doctorOps) mariadb(ctx context.Context) agentapi.DoctorCheck {
	const id = "service.mariadb"

	if d.h == nil || d.h.db == nil {
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorWarn, Summary: "MariaDB is not set up", Hint: "Sites cannot have databases. Run the installer again."}
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := d.h.db.Ping(ctx); err != nil {
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorFail, Summary: "MariaDB does not answer: " + err.Error(), Hint: "Check: systemctl status mariadb."}
	}

	return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorOK, Summary: "MariaDB is running"}
}

// sites checks each site's disk use, its slice and its certificate.
func (d *doctorOps) sites() []agentapi.DoctorCheck {
	var out []agentapi.DoctorCheck

	count := func(n uint64) string { return strconv.FormatUint(n, 10) }

	for _, s := range d.h.m.List() {
		home, ok := d.h.m.Home(s.User)
		if !ok {
			out = append(out, agentapi.DoctorCheck{ID: "site.home", Site: s.User, Status: agentapi.DoctorFail, Summary: "the site's pool is not registered"})

			continue
		}

		if d.h.quota != nil {
			if avail, err := d.h.quota.Available(home); err == nil && avail {
				if q, err := d.h.readQuota(home, s.UID); err != nil {
					out = append(out, agentapi.DoctorCheck{ID: "site.disk", Site: s.User, Status: agentapi.DoctorFail, Summary: "cannot read the disk use: " + err.Error()})
				} else {
					out = append(out, doctor.Usage(s.User, "disk space", q.Bytes, q.BytesLimit, doctor.Bytes))
					out = append(out, doctor.Usage(s.User, "files", q.Inodes, q.InodesLimit, count))
				}
			}
		}

		if d.h.slices != nil {
			out = append(out, d.slice(s.User))
		}

		if d.h.certs != nil {
			if c, ok := d.cert(s.User); ok {
				out = append(out, c)
			}
		}
	}

	return out
}

func (d *doctorOps) slice(user string) agentapi.DoctorCheck {
	const id = "site.limits"

	_, ok, err := d.h.slices.Get(user)

	switch {
	case err != nil:
		return agentapi.DoctorCheck{ID: id, Site: user, Status: agentapi.DoctorFail, Summary: "cannot read the slice: " + err.Error()}
	case !ok:
		return agentapi.DoctorCheck{
			ID: id, Site: user, Status: agentapi.DoctorWarn, Summary: "no CPU, memory and process limits",
			Hint: "Save the site's resource limits in the panel.",
		}
	}

	return agentapi.DoctorCheck{ID: id, Site: user, Status: agentapi.DoctorOK, Summary: "CPU, memory and process limits are set"}
}

// cert reports false for a site without a certificate.
func (d *doctorOps) cert(user string) (agentapi.DoctorCheck, bool) {
	info, err := d.h.certs.Live(user)
	if errors.Is(err, certs.ErrNotFound) {
		return agentapi.DoctorCheck{}, false
	}

	if err != nil {
		return agentapi.DoctorCheck{ID: "site.tls", Site: user, Status: agentapi.DoctorFail, Summary: "cannot read the certificate: " + err.Error()}, true
	}

	return doctor.Cert(user, info.NotAfter, d.now()), true
}
