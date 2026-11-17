# Spike S8 report: project quota and resource limits on Debian 13 / Ubuntu 24.04

Status: throwaway spike, nothing here is production code. Evidence tags as in the ADRs: **[V]** verified by a test or a package inspection in this spike, **[U]** unverified assumption, **[E]** estimate.

Environment: no real Debian/Ubuntu server was used and nothing was bought or changed. Tests ran in a QEMU VM (software emulation, no KVM) with a Debian 13 rootfs from `debootstrap` and the stock Debian 13 kernel `6.12.107+deb13-amd64`, ext4 root created like a cloud image (plain `mkfs.ext4`, no quota). The sandbox kernel itself has no `quota_v2`, so a loop device on the host was useless. Timings in the VM are inflated by emulation.

## Answers to the open questions (ADR-0015, S8)

| # | Question | Result |
|---|----------|--------|
| 1 | Project quota on a stock ext4 root, with or without reboot? | **Needs one reboot [V].** Stock Debian 13 ext4 has neither the `project` nor the `quota` feature. `tune2fs -O project,quota` on the mounted root is refused ("may only be changed when the filesystem is unmounted"). `mount -o remount,prjquota /` is refused ("Project quota feature not enabled"). |
| 2 | Does a project quota stop one site from filling the disk, neighbours unaffected? | **Yes, but only after enforcement is switched on [V], see finding 3.** Block limit 20 MiB: unprivileged writer stopped at 19 of 40 MiB, `Disk quota exceeded`. Inode limit works (stopped at the limit). A second project kept writing normally. Not tested: WordPress and Node behaviour on `EDQUOT` [U]. |
| 3 | RAM of one idle FPM master per site | **About 7 to 10 MB per idle master [V, once]**, see below. |
| 4 | `MemoryMax`, `TasksMax`, `CPUWeight`, `IOWeight` | **Not tested** (no time box left, needs a real systemd host with workloads). cgroup v2 with the `cpu io memory pids` controllers is active on Debian 13 [V]. |
| 5 | Cron and SSH inside the site's limits | **Not tested [U].** |
| 6 | First-run setup enables quota safely and idempotently | **Works with caveats [V]**, see findings. |
| 7 | Cheap usage reads for a dashboard | `repquota -Pn /` took about 0.1 s with 3 projects in the VM [V]. Real cost with 22+ projects and a big fs not measured. In Go the agent would use `quotactl` directly [U]. |

## Findings

1. **Kernel support is present on both distros, but as a module [V].** Debian 13 (`6.12.107+deb13`) and Ubuntu 24.04 (`6.8.0-149-generic`): `CONFIG_QUOTA=y`, `CONFIG_QFMT_V2=m`, `CONFIG_XFS_QUOTA=y`. On Debian `quota_v2.ko` ships in the `linux-image` package. On Ubuntu `generic` it ships **only in `linux-modules-extra`** (not in `linux-modules`). Ubuntu cloud kernels (`-kvm`, `-azure` and similar) may lack it [U]. Which kernel the Hetzner image uses was not checked [U]. The prototype therefore checks `modinfo quota_v2` first and refuses otherwise.
2. **Biggest risk: an unbootable server [V].** Debian's initramfs does not contain `quota_v2`. Once the `quota` feature is set, the **root mount fails** ("Failed to enable quota tracking, err=-3", dropped into the initramfs shell). I hit this twice, once from the hook running without the module and once with only the feature set. The fix in the prototype: the hook adds `quota_v2` to the initramfs (`manual_add_modules`, survives kernel updates because it is a hook), the premount script loads the module **before** touching the fs and does nothing if the load fails, and `apply` checks with `lsinitramfs` that module and hook are inside the new initramfs before it asks for a reboot.
3. **Mounting with `prjquota` is not enough [V].** After boot `quotaon -Ppv /` says "project quota on / is on (accounting)": usage is counted, limits are **not enforced**. A 40 MiB write into a 20 MiB project succeeded. After `quotaon -P /` the same test stopped at 20 MiB. The prototype installs `hostashi-quota-enforce.service` (`quotaon -P /` after `local-fs.target`). Verified in a fresh boot with the unit present. Why the kernel does not enforce on mount was not investigated [U].
4. **Root bypasses the limits [V]** (`CAP_SYS_RESOURCE`): a root-run restore or cron can exceed a site's quota. Run site workloads and restores as the site user, or drop the capability.
5. **Counting is by project, not by owner [V].** A root-owned file and a file chowned to another UID inside the site directory both count for the project. Hard links across projects fail (`EXDEV`). `mv` across projects falls back to copy and the file takes the new project ID.
6. **Wrong order breaks boot services but not boot [V].** `prjquota` in fstab while the fs lacks the feature: `systemd-remount-fs` fails, boot continues. The prototype therefore only writes fstab together with the initramfs step, backs up fstab first and rolls back on any failure.
7. **Idempotent [V].** Second `apply --yes` after the reboot: "Nothing to do", exit 0, 3 s (VM). Without `--yes` nothing is changed (exit 2).
8. **Not safe cases refuse and change nothing [V on the sandbox host]:** `plan`/`apply` exit 2 with "server left unchanged" when the kernel lacks `quota_v2` or the inode size cannot be read. XFS root and unknown layouts are refused by design in the prototype.

## RAM per FPM master [V, one run, Ubuntu 24.04 userland, PHP 8.3, 22 pools]

Measured on the sandbox host (not Debian 13, not in the VM), `smaps_rollup`, no workload:

| Setup | Idle processes | PSS total | RSS total |
|-------|----------------|-----------|-----------|
| 22 masters, 1 pool each, `ondemand` | 22 | about 162 MB (7.4 MB each) | about 225 MB (10.2 MB each) |
| 22 masters, `dynamic` with 1 spare worker | 44 | about 166 MB | about 438 MB |
| 1 shared master, 22 pools, `ondemand` | 1 | about 9 MB | about 10 MB |

So **one master per site costs roughly 160 to 230 MB for 22 sites [V]**, inside the earlier estimate (0.2 to 0.6 GB [E]) and below the pessimistic 0.7 to 2.5 GB. Request latency was 4 to 8 ms for the first request and 3 to 6 ms for the second, so `ondemand` start cost is small here [V, tiny script, no opcache warm-up, no real app]. A second run was polluted by leftover masters from the first and discarded, so this is a single clean run. Not measured: Debian 13 / PHP 8.4, opcache memory, real WordPress workers.

## What the prototype does (`quota-setup.sh`)

`detect`, `plan` (preview, no change), `apply --yes` (explicit confirmation). Steps for an ext4 root: back up fstab, add `prjquota` to `/`, install the initramfs hook (adds `quota_v2`, then `e2fsck -fp` and `tune2fs -O project,quota` before root is mounted, never blocks boot, exits 0 on any problem), install the enforcement unit, add `rootflags=prjquota` through a GRUB drop-in if `update-grub` exists, rebuild and verify the initramfs, ask for a reboot. Rollback on any failed step.

Tested end to end in the VM: the version without the enforcement unit (stage 0, reboot, stage 1). The enforcement unit was tested separately afterwards by adding it to that VM. **The final script with the unit built in was not run through a full VM cycle [U].** The GRUB drop-in path was not exercised (the VM boots the kernel directly, there is no GRUB) [U].

## Not done / open

- Ubuntu 24.04 was only checked by inspecting the kernel packages, no boot test [U]. The same flow should work but the `linux-modules-extra` dependency is the likely trap.
- XFS (Debian kernel has `CONFIG_XFS_QUOTA=y`): would need `rootflags=prjquota` on the kernel command line and no feature step [U], not tested.
- Interruption during `tune2fs`/`e2fsck` (power loss) not simulated [U]. The steps are short but not atomic.
- Real Hetzner image behaviour (GRUB, cloud-init rewriting fstab or the kernel command line) not tested. A paid Hetzner VM was not created, it needs Kim's approval.
- Items 4, 5 and 7 of the ADR spike list (cgroup limits under load, cron/SSH, I/O weight, dashboard polling cost at scale).

## Consequences for ADR-0015 (suggestions, Kim decides)

- Setup on a fresh server is feasible with one reboot, so abort criterion "unacceptable downtime" is not met for fresh servers [V].
- The setup must include the enforcement step, otherwise quotas only count [V].
- Add "quota_v2 available" to `hostashi doctor` and to the installer pre-checks, with the Ubuntu `linux-modules-extra` hint.
- Prefer creating the filesystem with `-O quota,project -E quotatype=prjquota` where the image can be built that way [U]; this removes the initramfs step.
