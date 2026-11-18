# Spike S5 report: idempotent installer on fresh Debian 13 / Ubuntu 24.04

Status: throwaway spike, nothing here is production code. Evidence tags as in the ADRs: **[V]** verified by a test in this spike, **[A]** assumption, **[U]** unverified, **[E]** estimate.

Question (`06-roadmap.md`): idempotent installer on fresh Ubuntu 24.04 and Debian 13 (amd64 + arm64). Abort criterion: "runs twice without change".

**Result: the criterion is met [V].** On Debian 13 and Ubuntu 24.04 (amd64) the second run reports `changes=0` and a fingerprint of files, permissions, packages, unit states and listeners is byte-identical before and after it. In a Debian 13 VM the full path including project quota and the one reboot works: runs 2, 3 and 4 (before and after the reboot) change nothing, and a 20 MiB site limit is enforced. arm64 (emulated `qemu-user` container): works, after a harness-only workaround, see finding 10 [V].

Environment: nothing was bought and nothing in the Hetzner project was touched. Tests ran in the build sandbox (Ubuntu 24.04 host, no KVM):

- **Containers** (`harness/container-test.sh`): official `debian:trixie` and `ubuntu:24.04` images with systemd as PID 1. They share the host kernel, so quota and reboot are not covered there (`--no-quota`).
- **VM** (`harness/vm-build.sh`, `vm-boot.sh`, `vm-runner.sh`): Debian 13 rootfs from `debootstrap`, stock kernel `6.12.107+deb13-amd64`, ext4 root made with plain `mkfs.ext4` (no quota features, like a cloud image), QEMU with software emulation. Kernel and initrd are re-read from the image before each boot, like GRUB. Times in the VM are inflated by emulation.
- **arm64**: `debian:trixie` arm64 container under `qemu-user` emulation, same script.

The sandbox forces HTTPS through a TLS-intercepting proxy, so the harness adds the proxy CA and proxy settings to the test systems. That is a sandbox artefact; a real server needs none of it, and the installer itself contains no proxy logic.

## Results

| Test | First run | Second run | Fingerprint | Notes |
|------|-----------|------------|-------------|-------|
| Debian 13 amd64, container | 11 steps, 48 s | `Nothing to do`, 0 s | identical (1,728 lines) | [V] |
| Ubuntu 24.04 amd64, container | 11 steps, 84 s | `Nothing to do`, 0 s | identical (1,778 lines) | [V] |
| Debian 13 amd64, VM with quota | 12 steps, 1,015 s (emulated) | same boot: 0 changes, 13 s | identical | reboot, then runs 3 and 4: 0 changes, identical [V] |
| Debian 13 arm64, container (emulated) | MariaDB step failed first (emulation, see finding 10), then 6 steps, 133 s | `Nothing to do`, 9 s | identical (1,733 lines) | nginx 1.30.6 arm64 from nginx.org, all `verify.sh` checks ok [V] |
| Refusal and recovery cases (Debian 13 container) | 16 of 16 pass | | | see below [V] |

After the reboot in the VM [V]: `/` is mounted with `prjquota`, the fs has the features `quota project`, `quotaon` reports "project quota on / is on (enforced)", and an unprivileged writer in a 20 MiB project stopped at 20 of 40 MiB. The initramfs hook from S8 logged `hostashi-quota: enabling project quota ... done` during the boot.

What `harness/verify.sh` checked on all three amd64 systems [V]:

- nginx **1.30.6 from nginx.org** (`1.30.6-1~trixie` / `~noble`, pin priority 900), answers on port 80, `nginx -t` ok, workers run as `nginx`.
- MariaDB from the distribution: **11.8.6 on Debian 13, 10.11.14 on Ubuntu 24.04**. Listens on 127.0.0.1 only. Only the accounts `root`, `mysql` and `mariadb.sys` exist, all `@localhost`, no anonymous user and no `test` database out of the box. An unprivileged Unix user cannot log in as MariaDB `root`.
- PHP from the distribution: **8.4 on Debian 13, 8.3 on Ubuntu 24.04**. The distro FPM service (`php8.x-fpm`) is disabled and stopped, because ADR-0015 starts one master per site.
- User `hostashi` (system, no login shell), `/etc/hostashi` 750 root:hostashi, `/var/lib/hostashi` and `/var/log/hostashi` 750 hostashi:hostashi, installer state in `/var/lib/hostashi-installer` 700 root.
- `hostashi-agent.service` runs as root, `hostashi.service` as `hostashi` (stand-in binary, see `stub/`).
- `unattended-upgrade --dry-run` lists the distribution security origins **and `origin=nginx,label=nginx`**, so nginx.org updates are installed automatically.

## Findings

1. **Step model: check, apply, re-check [V].** Every step has a read-only check and an apply. The runner calls apply only if the check fails and then runs the check again; if the postcondition does not hold, the step fails instead of reporting success. `plan` runs only the checks, so it is a dry run and later also the core of `hostashi doctor`. Idempotence then follows from the checks, not from hoping that apt and `cp` are idempotent. This model maps directly onto typed Go agent operations.
2. **No apt call on a re-run [V].** `apt-get update` only runs inside a step that has something to install. A re-run on a finished server needs no network and takes 0 s in a container (11 to 13 s in the emulated VM, mostly `quota-setup.sh plan` and `mariadb` calls).
3. **nginx.org repository works for both distributions and both architectures [V].** `trixie` and `noble` exist with `amd64` and `arm64` (also `resolute` = Ubuntu 26.04 [V: directory listing, not installed]). The key file `nginx_signing.key` contains **three** keys. The installer accepts it only if every key is on a pinned fingerprint list and rebuilds the keyring if a foreign key appears. This answers the open point in ADR-0005: **use the nginx.org repository as an apt source instead of vendoring the packages** (suggestion, Kim decides). Risk: if nginx.org rotates keys, installs fail until the list is updated [A].
4. **The pin is necessary on Ubuntu [V].** Without a pin apt could choose the distro package depending on versions; with `Pin: origin nginx.org` at 900 the nginx.org build always wins (`apt-cache policy` shows it). The pin covers `nginx*`.
5. **"Fresh server only" is enforceable cheaply [V, see fault tests].** Preflight refuses if apache2, nginx, MariaDB/MySQL, PHP-FPM, Caddy or lighttpd are installed, or if ports 80, 443 or 3306 are in use, unless the installer's own state file exists (so a re-run after a partial failure is still allowed).
6. **Quota is checked before anything changes [V].** Preflight runs `quota-setup.sh plan` (S8). If quota cannot be enabled safely (for example a container's overlay root), the installer refuses with exit 2 and changes nothing. `--no-quota` exists for tests only. On Ubuntu without `quota_v2` the base step installs `linux-modules-extra-$(uname -r)` [U: code path not exercised, the sandbox kernel is not an Ubuntu kernel].
7. **Reboot handling [V].** After the quota step the installer prints `REBOOT REQUIRED` and exits 0. A re-run in the same boot recognises "prepared, reboot pending" through the kernel `boot_id` and changes nothing. After the reboot the quota check passes and the state file gets `completed=`. `--reboot` reboots automatically (meant for cloud-init) [U: flag not exercised, the VM runner rebooted itself].
8. **Distribution differences the panel must handle [V].** PHP 8.4 vs 8.3 and MariaDB 11.8 vs 10.11. One Hostashi build has to template for both. Several PHP versions per server (third-party repository such as sury.org) are out of scope here and would need their own ADR.
9. **systemd hardening of the panel unit is only a start [V].** `systemd-analyze security` rates `hostashi.service` 6.1 (MEDIUM). The final unit needs a real binary to tighten (`CapabilityBoundingSet=`, `SystemCallFilter=`, `IPAddressDeny` and so on). The agent unit is root by design and barely hardened.
10. **Emulated arm64 is a smoke test only [V].** Under `qemu-user` systemd needs minutes to come up (an early start was refused cleanly with "systemd is not PID 1", exit 2, nothing changed). MariaDB's unit failed with `status=226/NAMESPACE` because `qemu-user` cannot build mount namespaces. That is an emulation limit, not an installer or arm64 problem: after a harness-only drop-in that disables systemd sandboxing, the installer finished the remaining 6 steps, the second run changed nothing and `verify.sh` passed. The `systemd-analyze security` score for the panel unit is therefore meaningless on arm64. The installer correctly reported the failed step and the re-run recovered, which doubles as another recovery test. A real arm64 VM or server is still untested [U].
11. **First run in the emulated VM took 17 minutes [V], on real hardware this is expected to be 1 to 3 minutes [E].** The container runs (48 s and 84 s, real CPU) are the better estimate.

## Refusal and recovery cases (`harness/fault-tests.sh`)

16 cases on a fresh Debian 13 container, all as expected [V]. "Unchanged" means: no state file and no nginx.org repository afterwards.

| Case | Expected | Result |
|------|----------|--------|
| `apply` without `--yes` | exit 2, unchanged | pass |
| Debian 12 | exit 2, unchanged | pass |
| Ubuntu 26.04 without `--allow-untested` | exit 2, unchanged | pass |
| Ubuntu 26.04 `plan --allow-untested` | exit 0 | pass |
| riscv64 | exit 2, unchanged | pass |
| Root on overlay, quota wanted | exit 2, unchanged, message names the reason | pass |
| `--binary` file missing | exit 2, unchanged | pass |
| Port 80 already in use | exit 2, unchanged | pass |
| Not fresh (apache2 installed) | exit 2, unchanged | pass |
| nginx.org unreachable | exit 3 at step `nginx_repo`, message says re-run is safe | pass |
| Re-run after that failure | exit 0, finishes the 9 remaining steps | pass |
| Re-run again | `changes=0` | pass |
| MariaDB drop-in changed to `0.0.0.0` | restored, `bind_address` 127.0.0.1 again | pass |
| Foreign GPG key in the nginx keyring | detected, keyring rebuilt | pass |
| Distro `php-fpm` re-enabled by hand | disabled again | pass |
| Final run | `changes=0` | pass |

The first run of this harness had 4 failures that were harness bugs (no `python3` in the image to occupy port 80, so the "refused" cases ran a real install). The corrected harness is the one in the repository. The same cases were not repeated on Ubuntu 24.04 [U].

## What the prototype is (`install.sh`)

`install.sh [plan]` (dry run) and `install.sh apply --yes` with `--binary PATH`, `--no-quota`, `--allow-untested`, `--reboot`. Exit codes: 0 ok (also "reboot required"), 2 refused (server unchanged), 3 step failed (re-run is safe), 64 usage. Steps in order: `base_packages`, `nginx_repo`, `nginx`, `php`, `mariadb` (drop-in: `bind-address = 127.0.0.1`, `skip-name-resolve`, `local-infile = 0`), `account`, `dirs`, `binary`, `units`, `updates` (unattended-upgrades incl. nginx.org), `quota` (calls the S8 script, copied unchanged as `quota-setup.sh`). The quota step is last, so a failure there leaves an otherwise working server.

## Not done / open

- **Ubuntu 24.04 in a VM** (quota with `linux-modules-extra`, real initramfs) and **arm64 in a VM** (quota on an arm64 kernel) were not booted. S8 covered Ubuntu 24.04 quota with the same script [V: S8].
- **Real Hetzner image**: GRUB path, cloud-init rewriting fstab or the kernel command line, the `-cloud` kernel. S8 ran on Kim's real Debian 13 server [V: S8], the full installer did not.
- **cloud-init / one-liner**: not tested. Proposed shape: cloud-init `runcmd` downloads a release, checks its signature, runs `install.sh apply --yes --reboot` [A].
- **Packaging**: no `.deb`, no signed Hostashi APT repository, no release signing. The architecture plan (`04-architecture.md` 4.8) stays: `.deb` from a signed repo, a small bootstrap script that only adds the repo.
- **Firewall** (nftables with lockout protection), panel TLS, first-run token and admin account: not part of this spike.
- **Uninstall / rollback** of a full install: not built. Only the quota step rolls back (from S8).
- Interruption in the middle of `apt-get` (power loss) not simulated; dpkg itself recovers with `dpkg --configure -a` [U], the installer does not call it yet.

## Suggestions (Kim decides)

- Keep the check/apply/re-check step model for the production installer and implement the steps as typed Go operations inside `hostashi` (`hostashi setup`, `hostashi doctor` = the checks). The shell script stays a spike.
- ADR-0005 follow-up: nginx.org as an apt source with pinned key fingerprints and a pin, nginx.org in the unattended-upgrades origins.
- ADR-0009 follow-up: preflight rules from finding 5 as the definition of "fresh server".
- Next test before Phase 1: one run of `install.sh` on a cheap Hetzner Debian 13 VM, only with Kim's approval for that project.

## How to re-run

```text
harness/build-stub.sh                                          # stand-in binaries (not committed)
harness/container-test.sh debian:trixie  t1 [linux/arm64]      # install twice, compare fingerprints
harness/container-test.sh ubuntu:24.04   t2
harness/fault-tests.sh debian:trixie                           # 16 refusal and recovery cases
harness/vm-build.sh /tmp/s5vm && harness/vm-boot.sh /tmp/s5vm/debian13.img /tmp/s5vm/vm.log 2   # needs root, debootstrap, QEMU
```

The container and VM scripts contain sandbox-only lines for the TLS-intercepting proxy, marked as such.
