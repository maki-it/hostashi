# Spike S4: file access as the site user (helper process)

**Status:** throwaway prototype, not product code. Nothing here is a decision; any follow-up goes through an ADR (ADR-0003 stays Proposed).

**Question (from [docs/06-roadmap.md](../../docs/06-roadmap.md) S4 and [ADR-0003](../../docs/adr/0003-language-and-stack.md)):** Is it safe and practical for the root agent to start a helper process under the site user for file operations, with path confinement against symlink and TOCTOU attacks, together with the typed RPC from S1, storage pools (ADR-0016) and quotas (ADR-0015, S8)?

Evidence tags as in [docs/adr/README.md](../../docs/adr/README.md): **[V]** verified by test or code in this spike, **[A]** assumption, **[E]** estimate, **[U]** unverified general knowledge.

## Environment and limits of this spike

- Sandbox container, root with full capabilities, kernel 6.18, Go 1.24.7, `golang.org/x/sys` v0.33.0. No real Debian 13 or Ubuntu 24.04 server was used, nothing was bought or changed, nothing was created at Hetzner.
- Site users are plain numeric uids (20001, 20002) without passwd entries. The helper never needs a passwd entry [V].
- The sandbox kernel has neither `quota_v2` nor XFS quota support (see S8). Real project quota was therefore tested in a QEMU VM with the **stock Debian 13 kernel 6.12.107** (`harness/vm-quota-test.sh`, same approach as S8, software emulation). In the sandbox itself a size-limited tmpfs stands in for a full quota.
- `fs.protected_symlinks` is 0 in this sandbox (Debian and Ubuntu default: 1). The design does not rely on it [V, tests ran with it off].
- Debian 13 (kernel 6.12) and Ubuntu 24.04 (kernel 6.8) are far above the 5.6 needed for `openat2` and the 5.8 needed for `STATX_ATTR_MOUNT_ROOT` [V from S8 kernel notes]. arm64: `go vet` passes, tests were not run there.

## What was built

Module `spike/s4-site-helper`, depends on S1 through a local `replace`. About 3,200 lines of Go including tests (about 600 in `confine`).

| Package | Content |
|---------|---------|
| `confine` | `Rel`: positively validated relative path (no `..`, `.`, absolute, NUL, control characters, invalid UTF-8, reserved temp prefix, length limits). `Root`: a directory fd plus operations `Stat`, `List` (paged), `OpenRead`, `WriteFile` (atomic), `Mkdir`, `Chmod`, `Rename`, `Remove` (recursive, fd-walk). Everything resolves with `openat2(RESOLVE_BENEATH | NO_MAGICLINKS | NO_XDEV)`. |
| `helper` | Wire protocol (one JSON header line plus raw body), `Harden()`, `Main()` (the helper side) and `Runner` (the agent side that starts it). |
| `agent` | `Registry` (sites and pools as root-owned state), `OpenRoot` (pool, marker, mount-root and home-directory checks on fds), and seven typed S1 operations `SiteFileStat/List/Read/Write/Mkdir/Remove/Rename`. |

How one request flows:

1. The web process calls an S1 operation with `{site, path, ...}`. S1 authorizes the peer by uid, decodes strictly and runs `Validate()` (site name by the S1 user rule, path by `ParseRel`).
2. The agent looks the site up in its **own** registry (uid, gid, pool). The caller never supplies a uid, a gid or a pool path.
3. `OpenRoot` opens the pool (no symlinks, root-owned, not writable by others, optional mount-root check, marker file with pool id), then the site home beneath it (one component, no symlinks, no mount crossing) and checks owner, group and mode on the open fd.
4. The agent re-executes its own binary with `__site-helper`, credentials set between fork and exec (`SysProcAttr.Credential`, empty group list), empty environment, cwd `/`, the home directory as fd 3, and a timeout.
5. The helper hardens itself, re-checks that fd 3 belongs to its own uid, reads one request, validates it again, performs the operation through `confine`, answers and exits.

There is no generic exec operation anywhere. The helper only knows eight operations with typed fields; unknown fields and fields that do not belong to the operation are rejected [V].

## Results

| Check | Result |
|-------|--------|
| Helper runs with uid, gid (real, effective, saved), empty supplementary groups, `CapEff/CapPrm/CapAmb/CapInh` all zero, `NoNewPrivs=1`, empty environment, cwd `/`, `RLIMIT_CORE=0`, own process group | [V] `TestHelperProcessIsDeprivileged` |
| Only fds 0 to 3 are inherited; an extra fd and an environment variable set in the agent do not reach the helper | [V] same test |
| Helper refuses to run when started as root | [V] exit code 70, `TestHelperRefusesWrongRootOwnerAndRootUser` |
| Helper refuses a root directory that is not owned by its own uid (agent bug) | [V] same test |
| Agent refuses to start a helper as uid 0, gid 0 or below the configured minimum id, and refuses a relative executable path | [V] `TestIdentityGuards` |
| Invalid requests (unknown op, `..`, absolute path, setuid mode, field of another op) never start a process | [V] `TestIdentityGuards` |
| Inside the helper, site A cannot read or write site B's 0750 files, root-only files, or `/etc/passwd` for writing; `setuid(0)`, `setresuid`, `setgid(0)`, `mount`, `chroot` fail with `EPERM` | [V] `TestDACStillHoldsInsideHelper` |
| World-readable files (`/etc/passwd`, any 0755 home) **stay readable** for the helper | [V] same test, an honest limit |
| Absolute or relative symlink out of the tree, `..` inside a symlink, `/proc/self/root` magic link, symlink loops: refused with `path_escape` or `symlink_refused`, for read, write, mkdir, list, rename | [V] `TestEscapesRefused` |
| A symlink planted by site A that points to a world-readable site B is refused (DAC alone would have allowed it) | [V] `TestForeignSiteViaSymlinkIsRefusedEvenWhenDACWouldAllow`, `TestRPCEndToEnd` |
| Relative symlinks that stay inside the tree work (`public/storage -> ../storage/app/public`); `NoSymlinks` mode refuses them | [V] `TestInsideSymlinkPolicy` |
| A final symlink is never followed by write, remove or chmod: write replaces the link, remove deletes the link, chmod refuses | [V] `TestSymlinkAsLastComponent` |
| Recursive remove does not follow links, does not enter mounts | [V] `TestRecursiveRemoveDoesNotFollowLinks`, `TestNoXdevRefusesMountInsideTree` |
| Reading a FIFO does not block; sockets, devices and directories are refused for read | [V] `TestFIFOAndSpecialFiles` |
| setuid, setgid and sticky bits cannot be set (write, mkdir, chmod), at path, helper and RPC level | [V] `TestModeBitsRefused`, `TestMalformedHelperInput`, `TestRPCEndToEnd` |
| Atomic write: temp file in the same directory, `RENAME_NOREPLACE` unless overwrite is asked for, wrong body length leaves old content and no temp file | [V] `TestWriteSemantics`, `TestLargeBodyBothWays` |
| Files and directories are created owned by the site user with exactly the requested mode, without any `chown` | [V] `TestRoundTripAsSiteUser` |
| Out of space: error code `no_space`, no partial file, old content intact, delete and writes work again afterwards | [V on tmpfs stand-in in the sandbox] `TestNoSpaceLeavesNoPartialFile`, `TestRPCQuotaStandIn` |
| **Real ext4 project quota (Debian 13 kernel, VM):** project id and `+P` set only on the site home by root; every directory and file created later by the helper inherits project 42 without any `chattr` by the helper; the 256 KiB limit stops the 8th 32 KiB write with `no_space` (usage 227 of 256 KiB), no temp file left, delete frees budget, a neighbour without limit on the same file system is unaffected | [V] `TestRPCRealProjectQuotaExt4`, one run, log excerpt below |
| Pool, home and RPC end-to-end tests also pass on the Debian 13 kernel (including real `statx` mount-root check on a real ext4 mount) | [V] same VM run |
| Malformed helper input (garbage, giant header, unknown field, NUL, trailing data, short body, huge size) produces an error response, no panic, no hang, no file | [V] `TestMalformedHelperInput` |
| Helper that never answers is killed at the timeout | [V] `TestTimeoutKillsHelper` |
| Unmounted pool: a plain directory where the volume should be is refused (`STATX_ATTR_MOUNT_ROOT`), also after a detach; wrong or missing or symlinked or writable marker is refused | [V] `TestUnmountedPoolIsRefused`, `TestOpenRootChecks` |
| Home directory with wrong owner, group-writable mode, or replaced by a symlink to another site is refused; the RPC client sees only `internal`, the detail stays in the log | [V] `TestOpenRootChecks`, `TestRPCUnsafeHomeIsOpaqueInternalError` |
| Unknown site `not_found`; site names like `root`, `../beta`, `a/b`, empty are `invalid_argument`; unauthorized RPC peer gets no reply | [V] `TestRPCEndToEnd`, `TestRPCUnauthorizedPeer` |
| `ParseRel` fuzz target, 15 s, 700,000 executions | [V] no invariant violation (short run only) |
| Tests under `-race` | [V] pass (helper package with `-short`, the race runtime makes each helper start slow) |
| Tests as a non-root user | [V] root-only tests skip, the rest passes |

### Race tests (1.5 s each, an atomic `RENAME_EXCHANGE` swaps a real directory and a symlink to an outside directory with a secret)

| Implementation | Result |
|----------------|--------|
| Naive: `EvalSymlinks`, prefix check, then `os.ReadFile(path)` | **17,923 leaks in 77,904 attempts** (23 %) [V] |
| `confine` (openat2 beneath) | 90,288 correct reads, 174,354 refused, **0 leaks** [V] |
| Go 1.24 `os.Root` | 0 leaks in 274,193 attempts [V] |
| Write and mkdir through the swapping path | nothing outside the tree changed [V] |
| Recursive remove while a subdirectory is swapped for a symlink to precious files | 367 rounds, outside files intact [V, stress test, not a proof] |

The naive pattern is not a strawman: it is what a root agent does if it validates a path string and then opens it. A site user who controls the tree wins this race about every fourth try [V].

## Cost

[V] on the sandbox, tiny `list` request, helper started per request:

- Median **1.2 to 1.5 ms**, p95 about 1.8 ms per request including fork, exec, hardening and one operation.
- 32 parallel callers: about 2,300 to 2,600 requests per second.
- 16 MiB write plus 16 MiB read through the helper: about 0.3 s.

Conclusion: **no long-lived helper, no pool of helpers, no session protocol needed for v1.** One process per request is simple to reason about (no state, no leaked fds) and fast enough for a file manager [V for these numbers, E for real sites with big directories].

## Findings worth carrying into the design

1. **Two independent layers, and both mattered.** `openat2` confinement stops path tricks; the site uid stops everything else. Neither alone is enough: DAC alone allows reading a world-readable neighbour through a symlink; confinement alone makes every path bug a root bug. [V]
2. **File descriptors, not paths, cross the boundary.** The helper receives the home directory as an open fd and never resolves an absolute path. It works even when the path to the home is not traversable for the site user (parent directories 0700) and no path check can be raced between agent and helper. [V]
3. **The classic root-chown bug class disappears.** Files are created by the site uid, so there is no `chown` of a path the site user can swap for a symlink. [V]
4. **`st_dev` is not enough to detect a mount inside the tree.** A bind mount of the same file system has the same `st_dev`; the first version of recursive remove would have emptied a bind-mounted directory outside the site. Found by `TestNoXdevRefusesMountInsideTree`, fixed with `RESOLVE_NO_XDEV` on every step. [V]
5. **The helper is not a sandbox.** It has exactly the powers of the site's own PHP processes: world-readable system files, network, other processes of the same uid. The claim is only: *a bug in the helper cannot do more than the site user can already do, and cannot cross into other sites or root-owned state.* Homes must therefore be 0750 or 0700 (the agent refuses group/world-writable homes; world-readable is **not** refused yet, add it to the doctor checks). [V and A]
6. **Quota (ADR-0015, S8 finding 4):** root bypasses project quota; a helper running as the site user does not, so file-manager uploads and restores through this path count against the site by construction. Verified on real ext4 project quota in the Debian 13 VM: new files and directories inherit the project id from the home directory (`chattr -p 42 +P` once, by root), the limit turns into `no_space`, and nothing partial remains. Still **[A]**: mixing helper writes with PHP-FPM writes under the same limit at load, and root-run restores (these would bypass the limit, so they must run as the site user too). [V and A]
7. **Storage pools (ADR-0016) fit.** Pool name to path stays in root-owned state; the marker check plus mount-root check (`statx` mount-root attribute, kernel 5.8 and later, also verified on the Debian 13 kernel) prevents writing into an empty mount point after a detach. `RequiresMountsFor=` in the site units was not tested here. [V for the check, A for systemd]
8. **S1 integration works, with two limits.** A new operation costs about 10 lines in the agent. But the S1 frame limit (64 KiB) means file content is inline up to 32 KiB only and directory listings must be paged (limit 500 per call). **Bulk transfer (upload, download, archives) needs a separate data path.** Suggestion [A, not built]: the web process passes a pipe or socket fd with `SCM_RIGHTS` along with the RPC call and the agent connects it to the helper's stdin or stdout; the helper protocol already streams raw bodies. The S1 frame code would need fd passing.
9. **Authorization boundary, stated openly.** The agent trusts the web process to decide *which panel user may touch which site*. A compromised web process can therefore read and change the files of **every site** and, by writing a PHP file, run code as any site user. It cannot reach root, other paths, pool or panel state, or anything outside a site home. This is inherent to a panel and is the same trust level as the session layer; it belongs into `05-sicherheit.md` as a named residual risk. [A for the wording, V for the technical limits]
10. **Capability bounding set is not dropped in the helper** (`CapBnd` stays full as inherited from the agent). With `NoNewPrivs=1`, a non-root uid and empty permitted and effective sets it cannot be used [A], but defense in depth is cheap: a `CapabilityBoundingSet=` on the agent unit shrinks it for the helper too. No seccomp filter was tried; an allowlist for the helper (open, openat2, read, write, getdents, rename, unlink, fchmod, statx and a few more) is a sensible follow-up, as are `CLONE_NEWNET` and `CLONE_NEWIPC` in `SysProcAttr.Cloneflags`. [A]
11. **`os.Root` (Go 1.24) is good, but not enough on its own.** It passed the read race and is the right cross-check, but it cannot be created from an fd, its documentation states it does not stop mount traversal, and in 1.24 it has no `Rename`, `Chmod` or `RemoveAll` (added in later Go versions, [U], check the release notes). Recommendation: keep the thin `openat2` layer (about 600 lines without tests) and re-evaluate when the project uses a newer Go. [V for behavior, U for later versions]
12. **Known gaps in `Rel`.** Names that already exist on disk but are not valid UTF-8 or contain control characters (created by PHP or an archive) are listed but cannot be addressed, so the file manager cannot rename or delete them. Needs a decision (escape syntax or a "fix names" tool). [V, `TestExistingUnaddressableNames`] Also `List` reads and sorts all names of a directory per call, which is wasteful for directories with hundreds of thousands of entries [E].
13. **A move of a home directory while a helper runs** (pool move, ADR-0016 later step) leaves the helper on the old inode. Moves need the same maintenance lock as other site-wide operations. [A]
14. **Deliberately not offered by the helper:** symlink and hard link creation, `chown`, device nodes, recursive chmod ("rights reset"), copy, archive create and extract, ACL and xattr changes. Each needs its own fd-based walk and its own tests; hard links are covered by `fs.protected_hardlinks` (1 here and by default on both distros [U for the defaults]) but should not be an operation without need.

## Does this answer the spike question?

For the file-manager style operations: **yes, practical and safe in this prototype.** The pattern from ADR-0003 (re-execute the agent binary as the site user, confinement with `openat2`) holds up in every test, including the race, mount and wrong-owner cases, and costs about 1.5 ms per request. Nothing found contradicts the ADR-0003 choice (it should not be set to Accepted because of one spike; Kim decides).

Suggested text changes, Kim decides:

- `docs/04-architektur.md` 4.2: replace "`openat2`/`RESOLVE_BENEATH` (oder `fchdir` in die Site-Wurzel plus Privilegien-Drop)" by "Der Agent startet einen Hilfsprozess unter dem Site-User und übergibt das Site-Verzeichnis als offenen Dateideskriptor; Auflösung mit `openat2` (`RESOLVE_BENEATH`, `NO_XDEV`, `NO_MAGICLINKS`)". The `fchdir` variant has no advantage here.
- `docs/05-sicherheit.md`: add the residual risk of finding 9 and the doctor checks for finding 5.
- ADR-0003 risk section: "tested in S4" with the tags from this report.

## Not covered

- `RequiresMountsFor`, Ubuntu 24.04 (needs `linux-modules-extra` for quota), a real Hetzner host instead of the VM, arm64 execution, XFS quota.
- Streaming bulk transfer and fd passing (finding 8), seccomp and namespaces for the helper (finding 10), archive operations, copy, rights reset.
- Interaction with PHP-FPM and cron running as the same uid (shared `NOFILE` or process limits via cgroups from ADR-0015).
- Fuzzing the helper header parser and `Request.Validate` (only `ParseRel` was fuzzed, for 15 s).
- A hostile site user racing the **agent-side** `OpenRoot` checks: the pool directory is root-owned and not writable by others, so a site user cannot rename its own home [A, checked by the writable-pool test, not by an active attacker].

### Quota test log (VM, Debian 13, kernel 6.12.107)

```text
=== S4 VM: kernel 6.12.107+deb13-amd64
--- PASS: TestOpenRootChecks / TestUnmountedPoolIsRefused / TestRPCEndToEnd
quota_test.go: 7 writes of 32 KiB succeeded before no_space; quota report:
Project  used  soft  hard
#0          13     0     0
#42        227     0   256
--- PASS: TestRPCRealProjectQuotaExt4 (1.87s)
```

## How to run

```sh
cd spike/s4-site-helper
go test ./...                    # root-only tests skip without root
sudo go test ./...               # full run, needs mount and setuid permission
sudo go test -race -short ./...  # helper starts are slow under the race detector
go test -fuzz FuzzParseRel -fuzztime 30s ./confine
```

The quota test and the pool tests in a Debian 13 VM: `sudo spike/s4-site-helper/harness/vm-quota-test.sh` (needs qemu and debootstrap, about 15 minutes the first time).

Tests create temporary directories under `/tmp`, mount small tmpfs instances and use uids 20001 and 20002. They do not touch anything else.
