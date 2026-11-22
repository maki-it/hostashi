# ADR-0060 Time zone, hostname and reboot from the panel

**Status:** Proposed. **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0003 (stack, typed agent operations), ADR-0033 (hardening), ADR-0035 (roles), ADR-0052 (service management), ADR-0058 (OpenAPI and CLI parity).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- "Time zone, hostname, reboot" is a chosen P1 row of the 1.0 scope [V: `docs/03-features.md` 3.0]. Today an admin needs SSH for all three [V: no agent operation for them in `internal/agentapi`].
- The agent runs with `ProtectHostname=yes`: it has its own UTS namespace, so a `sethostname()` from the agent changes only its own view, not the server's [V: `packaging/systemd/hostashi-agent.service`; U: systemd.exec(5)]. It also has `ProtectClock=yes` and no `CAP_SYS_BOOT` [V: same file]. Neither is needed for the time zone (a symlink, not the clock) or for a reboot that PID 1 performs [U].
- systemd ships three small services for exactly this: `systemd-timedated` (`org.freedesktop.timedate1.SetTimezone`), `systemd-hostnamed` (`org.freedesktop.hostname1.SetStaticHostname`, `SetHostname`) and `systemd-logind` (`org.freedesktop.login1.Manager.Reboot`). They are started on demand over D-Bus; a caller running as root passes polkit without a prompt [U: systemd docs]. `timedatectl` and `hostnamectl` are only D-Bus clients of these services [U].
- They need a running system bus (`dbus-daemon` or `dbus-broker`) [U]. Debian 13 and Ubuntu 26.04 server images ship one [A: to verify in the container test; the installer can add the `dbus` package].
- ADR-0052 rejected D-Bus for service management because a bus connection reaches all of PID 1's methods while we needed five verbs, and the existing `internal/phpctl` exception already runs `systemctl` [V: `docs/adr/0052-service-management-and-server-dashboard.md`, option B]. For the hostname there is no such existing path: `systemctl` cannot set it.
- Site cron jobs are systemd timers whose `OnCalendar=` values have no zone, so they follow the server's time zone [V: `internal/cron/schedule.go`, `unit.go`; U: systemd.time(7)]. MariaDB reads the system zone only at start (`system_time_zone`) [U]. PHP uses `date.timezone`, which does not follow the system zone [U].
- On Hetzner Cloud, cloud-init sets the hostname from the server's name at boot unless `preserve_hostname: true` is set, and may rewrite `/etc/hosts` [U: cloud-init docs; A: web2 and Kim's other servers run cloud-init, to check].
- The doctor already warns when `/run/reboot-required` exists, and says "the server never reboots by itself" (ADR-0033) [V: `internal/doctor/hardening.go`]. A reboot started by an admin keeps that promise.

## Options

### How the agent changes the three settings

| Option | Pro | Contra |
|--------|-----|--------|
| **A. D-Bus to timedated, hostnamed and logind through one small package (`internal/sysconf`, `github.com/godbus/dbus/v5`)** | No process start, no new `os/exec` exception; systemd does the same steps as `timedatectl`/`hostnamectl`, including `/etc/timezone`, `/etc/hostname` and the change signals [U] | New dependency (BSD-2-Clause, on the allowlist) [V: v5.2.4 is the latest]. A bus connection could call any method, so the package must be the only importer (depguard rule) and expose only typed functions |
| B. `timedatectl`, `hostnamectl` and `systemctl reboot` as a new `os/exec` exception | Familiar tools | A new exception for tools that are themselves D-Bus clients: same bus dependency, plus process start and output parsing |
| C. Write `/etc/localtime`, `/etc/hostname` directly and drop `ProtectHostname` from the agent | No dependency | Weakens the agent's hardening; misses steps systemd does (signals, transient hostname); reboot still needs a path |

### How a reboot is started

| Option | Pro | Contra |
|--------|-----|--------|
| **D. logind `Reboot(false)` over the same D-Bus package** | One mechanism for all three | One more method on the bus |
| E. `systemctl reboot` through the existing `internal/phpctl` exception | No D-Bus for this part | phpctl grows a verb that is not about units [A] |

## Recommendation

**Option A with D.**

1. **Package `internal/sysconf`** is the only importer of `godbus/dbus/v5` (depguard). It exposes three functions with fixed bus names, object paths and method names: `SetTimezone(zone)`, `SetHostname(name)` (static and transient hostname) and `Reboot()`, plus a read-only `Info()` (hostname, time zone, server time, uptime, reboot required). No function takes a method or property name.
2. **Typed agent operations:** `system.info` (read-only), `system.timezone.set{zone}`, `system.hostname.set{name}`, `system.reboot{}`. Validation happens in `agentapi` and again in the agent:
   - zone: one of the names in the server's tzdata (`/usr/share/zoneinfo/tzdata.zi` or `zone1970.tab`), loaded by Go's `time.LoadLocation` as a second check; no paths, no `..` [A];
   - hostname: lowercase RFC 1123 labels, at most 63 characters per label and 64 in total (the kernel's limit), no trailing dot [U];
   - reboot: no arguments.
3. **Side effects the agent handles:**
   - hostname: replaces the `127.0.1.1` line in `/etc/hosts` (Debian's convention) so `sudo` and local lookups keep working, and writes `/etc/cloud/cloud.cfg.d/90-hostashi-hostname.cfg` with `preserve_hostname: true` when `/etc/cloud` exists, so cloud-init does not undo the change at the next boot [A: to test on a Hetzner image];
   - time zone: nothing is restarted. The panel says that site cron jobs follow the new zone at once and that MariaDB uses it after its next restart, with a button to the service page [A].
4. **Panel and API:** a page "System" in the server group, admin only (ADR-0035), routes `GET /api/v1/system`, `PUT /api/v1/system/timezone`, `PUT /api/v1/system/hostname`, `POST /api/v1/system/reboot` with OpenAPI records (ADR-0058), scopes `system:read` and `system:write`, and CLI commands from the generated client. The time zone is a searchable list, the hostname a text field.
5. **Reboot safety:** the dialog asks the admin to type the hostname, as for stopping MariaDB (ADR-0052). The panel writes the audit entry before the call, the agent answers first and asks logind to reboot two seconds later [E], so the response reaches the browser. The page then waits for the panel to answer again, like the SSH port change. The doctor's "reboot needed" check links to this button. No scheduled reboots in v1.
6. **Audit log:** each change records old and new value; a reboot records who started it and the reason the doctor gave, if any.
7. **Installer:** adds `dbus` to the package list if it is missing [A], and the container test checks all four operations (reboot only up to the logind call, with a fake bus or a check of the job) [A: how far the container's systemd allows it].

## Consequences

- One new dependency and a bus connection from the root agent; the depguard rule and the narrow package keep it to four methods [A].
- No new `os/exec` exception, no change to the agent unit's hardening.
- A wrong time zone shifts every site's cron jobs; the panel shows the server time in the new zone before saving [A].
- A reboot cuts every site off for the boot time [E: 30–90 s on a Hetzner cloud server]. Typing the hostname makes it hard to start one by accident.
- Not in scope: NTP settings (systemd-timesyncd stays as installed), the pretty hostname, scheduled reboots, shutdown.
