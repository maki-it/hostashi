// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/certs"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/f2bctl"
	"github.com/maki-it/hostashi/internal/firewall"
	"github.com/maki-it/hostashi/internal/nodeinstall"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/phpctl"
	"github.com/maki-it/hostashi/internal/updates"
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
	DBToolSocket   string   // default DBToolSocket
	Disk           []string // file systems to check besides the pools; default / and the state directories
	// Hardening reads the files of the hardening baseline (ADR-0033). The
	// zero value reads the server and asks systemd through phpctl.
	Hardening doctor.Hardening
	Now       func() time.Time
	// ValkeyDefault reads the state of the Valkey package's own service
	// (ADR-0045); nil asks systemd through phpctl.
	ValkeyDefault func(ctx context.Context) (active, file string, err error)
	// ValkeyBinary is where the installer put valkey-server; default
	// /usr/bin/valkey-server.
	ValkeyBinary string
	// Updates are the apt and dpkg files of the update check (ADR-0050);
	// the zero value reads the server's.
	Updates updates.Paths
}

type doctorOps struct {
	env      DoctorEnv
	stateDir string
	h        *siteOps          // nil without the site operations
	fw       *firewall.Manager // nil without the panel's firewall rules
	dbTool   bool              // the web database tool is installed (ADR-0038)
	node     *nodeinstall.Manager
	f2b      *f2bctl.Controller // nil: the jails are not checked
}

func registerDoctor(s *agentrpc.Server, env DoctorEnv, stateDir string, h *siteOps, fw *firewall.Manager, node *nodeinstall.Manager, f2b *f2bctl.Controller, dbTool bool) {
	d := &doctorOps{env: env, stateDir: stateDir, h: h, fw: fw, node: node, f2b: f2b, dbTool: dbTool}

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

	// Only usable pools get the disk, quota and mode checks: below a
	// missing volume they would report the root disk.
	var (
		pools     []string
		poolCheck []agentapi.DoctorCheck
	)

	if d.h != nil {
		for _, p := range d.h.m.Pools() {
			ps := doctor.PoolState{Name: p.Name, Path: p.Path, Volume: p.RequireMount, Unavailable: d.h.m.CheckPool(p.Name)}
			if p.RequireMount {
				ps.InFstab = d.h.m.PoolInFstab(p.Path)
			}

			poolCheck = append(poolCheck, sys.PoolVolume(ps))

			if ps.Unavailable == nil {
				pools = append(pools, p.Path)
			}
		}
	}

	out = append(out, poolCheck...)
	out = append(out, sys.FstabDevices())

	disk := d.env.Disk
	if disk == nil {
		disk = []string{"/", "/var/lib/hostashi", orDefault(d.stateDir, DefaultStateDir)}
	}

	out = append(out, sys.Disk(append(disk, pools...))...)

	for _, p := range pools {
		if d.h.quota == nil {
			break
		}

		st, err := d.h.quota.Status(p)
		out = append(out, doctor.Quota(p, st, err))
	}

	out = append(out, sys.Proc())
	for _, p := range pools {
		out = append(out, doctor.PoolMode(p))
	}

	out = append(out, doctor.Panel(ctx, orDefault(d.env.PanelSocket, PanelSocket)))
	out = append(out, d.nginx(ctx))
	out = append(out, d.mariadb(ctx))
	f2bCheck := doctor.Socket(ctx, "service.fail2ban", "fail2ban", orDefault(d.env.Fail2banSocket, Fail2banSocket), "fail2ban.hint")
	out = append(out, f2bCheck)

	// The jails only when fail2ban answers; otherwise the check above
	// already says it (ADR-0078).
	if d.f2b != nil && f2bCheck.Status == agentapi.DoctorOK {
		st, err := d.f2b.Status(ctx, agentapi.Fail2banStatusRequest{Limit: 1})
		out = append(out, doctor.Fail2banJails(st, err))
	}

	if d.dbTool {
		out = append(out, doctor.Socket(ctx, "service.dbtool", "the database tool", orDefault(d.env.DBToolSocket, DBToolSocket), "dbtool.hint"))
	}

	hard := d.env.Hardening
	if hard.Active == nil {
		hard.Active = phpctl.UnitActive
	}

	if hard.Now == nil {
		hard.Now = d.now
	}

	out = append(out, hard.Firewall(ctx))

	if d.fw != nil {
		st, err := d.fw.Status()
		if c, ok := doctor.FirewallPanel(st, err, d.now()); ok {
			out = append(out, c)
		}
	}

	out = append(out, hard.Updates(), hard.Reboot(), hard.PHP(), hard.Files(), hard.SSH(), hard.Sysctl())
	st, err := updates.Read(d.env.Updates)
	out = append(out, doctor.Update(st, err, d.now())...)
	out = append(out, doctor.Runtimes(st)...)
	out = append(out, d.valkey(ctx))
	out = append(out, d.nodeChecks()...)

	if d.h != nil {
		out = append(out, d.sites(ctx)...)
		out = append(out, d.h.pageCacheChecks()...)
	}

	return agentapi.DoctorReport{Checks: out}
}

// nodeChecks checks the installed Node.js lines and the sites on them.
func (d *doctorOps) nodeChecks() []agentapi.DoctorCheck {
	if d.node == nil {
		return nil
	}

	users := map[string][]string{}

	if d.h != nil && d.h.apps != nil {
		for _, i := range d.node.Installed() {
			if u, err := d.h.apps.Users(i.Line); err == nil {
				users[i.Line] = u
			}
		}
	}

	return doctor.Node(nodeinstall.Installed(d.node.Installed()), users, d.now())
}

func (d *doctorOps) nginx(ctx context.Context) agentapi.DoctorCheck {
	const id = "service.nginx"

	if d.h == nil || d.h.web == nil {
		return doctor.New(agentapi.DoctorFail, id, "nginx.missing", "installer.hint", nil)
	}

	h, err := d.h.web.Health(ctx)
	if errors.Is(err, web.ErrBusy) {
		return doctor.New(agentapi.DoctorWarn, id, "nginx.busy", "nginx.busy.hint", nil)
	}

	if err != nil {
		return doctor.New(agentapi.DoctorFail, id, "nginx.error", "", doctor.P{"error": err.Error()})
	}

	return nginxCheck(h)
}

func nginxCheck(h web.Health) agentapi.DoctorCheck {
	const id = "service.nginx"

	switch {
	case h.Current == 0:
		return doctor.New(agentapi.DoctorWarn, id, "nginx.none", "nginx.none.hint", nil)
	case h.LiveErr != nil:
		return doctor.New(agentapi.DoctorFail, id, "nginx.down", "nginx.down.hint", doctor.P{"error": h.LiveErr.Error()})
	case h.Live != h.Current:
		return doctor.New(agentapi.DoctorFail, id, "nginx.behind", "nginx.behind.hint", doctor.P{"live": strconv.Itoa(h.Live), "current": strconv.Itoa(h.Current)})
	case len(h.Drift) > 0:
		return doctor.New(agentapi.DoctorWarn, id, "nginx.drift", "nginx.drift.hint", doctor.P{"files": strings.Join(h.Drift, ", ")})
	case !h.Confirmed:
		return doctor.New(agentapi.DoctorWarn, id, "nginx.unconfirmed", "websync.hint", doctor.P{"current": strconv.Itoa(h.Current)})
	}

	return doctor.New(agentapi.DoctorOK, id, "nginx.ok", "", doctor.P{"live": strconv.Itoa(h.Live)})
}

func (d *doctorOps) mariadb(ctx context.Context) agentapi.DoctorCheck {
	const id = "service.mariadb"

	if d.h == nil || d.h.db == nil {
		return doctor.New(agentapi.DoctorWarn, id, "mariadb.missing", "mariadb.missing.hint", nil)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := d.h.db.Ping(ctx); err != nil {
		return doctor.New(agentapi.DoctorFail, id, "mariadb.down", "mariadb.down.hint", doctor.P{"error": err.Error()})
	}

	return doctor.New(agentapi.DoctorOK, id, "service.running", "", doctor.P{"name": "MariaDB"})
}

// sites checks each site's disk use, its slice and its certificate.
func (d *doctorOps) sites(ctx context.Context) []agentapi.DoctorCheck {
	var out []agentapi.DoctorCheck

	count := func(n uint64) string { return strconv.FormatUint(n, 10) }

	var pools map[string]php.Pool
	if d.h.php != nil {
		pools, _ = d.h.php.Current()
	}

	for _, s := range d.h.m.List() {
		if p, ok := pools[s.User]; ok && p.Version != "" {
			out = append(out, doctor.PHPSupport(s.User, p.Version, d.now()))
		}

		home, ok := d.h.m.Home(s.User)
		if !ok {
			out = append(out, doctor.NewSite(agentapi.DoctorFail, "site.home", s.User, "home.nopool", "", nil))

			continue
		}

		if d.h.quota != nil {
			if avail, err := d.h.quota.Available(home); err == nil && avail {
				if q, err := d.h.readQuota(home, s.UID); err != nil {
					out = append(out, doctor.NewSite(agentapi.DoctorFail, "site.disk", s.User, "sitedisk.error", "", doctor.P{"error": err.Error()}))
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

		if c, ok := d.cache(ctx, s.User, s.UID); ok {
			out = append(out, c)
		}
	}

	return out
}

func (d *doctorOps) slice(user string) agentapi.DoctorCheck {
	const id = "site.limits"

	_, ok, err := d.h.slices.Get(user)

	switch {
	case err != nil:
		return doctor.NewSite(agentapi.DoctorFail, id, user, "slice.error", "", doctor.P{"error": err.Error()})
	case !ok:
		return doctor.NewSite(agentapi.DoctorWarn, id, user, "slice.none", "slice.none.hint", nil)
	}

	return doctor.NewSite(agentapi.DoctorOK, id, user, "slice.ok", "", nil)
}

// cert reports false for a site without a certificate.
func (d *doctorOps) cert(user string) (agentapi.DoctorCheck, bool) {
	info, err := d.h.certs.Live(user)
	if errors.Is(err, certs.ErrNotFound) {
		return agentapi.DoctorCheck{}, false
	}

	if err != nil {
		return doctor.NewSite(agentapi.DoctorFail, "site.tls", user, "cert.error", "", doctor.P{"error": err.Error()}), true
	}

	return doctor.Cert(user, info.NotAfter, d.now()), true
}
