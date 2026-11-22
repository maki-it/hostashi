// SPDX-License-Identifier: AGPL-3.0-or-later

import { DownloadSimple, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { daysUntil, getPHPInstall, installPHP, phpMessage, removePHP, support, todayUTC, type PHPInstall, type Support } from "./php";
import { listSites, phpVersions, type PHPVersions, type Site } from "./sites";
import { Badge, Button, Card, IconButton, Notice, PageHead, Table, type Column, type Sort, type Tone } from "./ui";

const supportLabel: Record<Support, string> = {
  active: "Supported",
  security: "Security fixes only",
  eol: "End of life",
};
const supportTone: Record<Support, Tone> = { active: "ok", security: "warn", eol: "err" };

type QueryProps = {
  /** The address query: show (all or installed), sort, dir. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

type Row = { version: string; support: Support; until: string; installed: boolean; sites: number | null };

// PHPPage lists the PHP versions Hostashi can run, newest first with the
// distribution's own version marked (ADR-0037), installs more of them from
// sury.org (ADR-0036), and names newer versions that sury.org already
// offers but Hostashi does not run yet.
export function PHPPage({
  today = todayUTC(),
  poll = 3000,
  query = new URLSearchParams(),
  onQuery = () => undefined,
}: { today?: string; poll?: number } & QueryProps) {
  const [versions, setVersions] = useState<PHPVersions | null>(null);
  const [inst, setInst] = useState<PHPInstall | null>(null);
  const [sites, setSites] = useState<Site[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<string | null>(null);
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [v, i] = await Promise.all([phpVersions(), getPHPInstall()]);
      setVersions(v);
      setInst(i);
      setError("");
    } catch (err) {
      setError(phpMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
    // The number of sites per version is a help, not a must.
    listSites()
      .then(setSites)
      .catch(() => setSites(null));
  }, [load]);

  // While an installation runs, follow it.
  const running = inst?.state === "running";
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void load(), poll);
    return () => clearInterval(t);
  }, [running, load, poll]);

  const start = async (version: string, confirmEol: boolean) => {
    setConfirm(null);
    try {
      setInst({ ...(inst ?? { repository: false, newer: [] }), ...(await installPHP(version, confirmEol)) });
      setError("");
    } catch (err) {
      setError(phpMessage(err));
    }
  };

  const remove = async (version: string) => {
    setConfirmRemove(null);
    try {
      setInst({ ...(inst ?? { repository: false, newer: [] }), ...(await removePHP(version)) });
      setError("");
    } catch (err) {
      setError(phpMessage(err));
    }
  };

  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  const head = (
    <PageHead
      title="PHP versions"
      description="The distribution's PHP is always installed. More versions come from the sury.org repository and get their updates automatically; one that no site uses can be removed."
    />
  );

  if (!versions || !inst) {
    return (
      <>
        {head}
        {error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>Try again</Button>
          </Notice>
        ) : (
          <p aria-busy="true">Loading…</p>
        )}
      </>
    );
  }

  const rows: Row[] = versions.supported.map((v) => ({
    version: v,
    support: support(versions, v, today),
    until: versions.securityUntil?.[v] ?? "",
    installed: versions.installed.includes(v),
    sites: sites ? sites.filter((s) => s.php === v).length : null,
  }));
  const onlyInstalled = query.get("show") === "installed";
  const shown = onlyInstalled ? rows.filter((r) => r.installed) : rows;

  const columns: Column<Row>[] = [
    {
      key: "version",
      header: "Version",
      sortValue: (r) => r.version,
      cell: (r) => (
        <span className="ui-row">
          <strong>PHP {r.version}</strong>
          {versions.distro === r.version && <Badge tone="off">Distribution</Badge>}
        </span>
      ),
    },
    {
      key: "support",
      header: "Upstream support",
      sortValue: (r) => r.until,
      cell: (r) => {
        const left = r.until ? daysUntil(r.until, today) : -1;
        return (
          <div className="ui-cell-main">
            <span className="ui-row">
              <Badge tone={supportTone[r.support]}>{supportLabel[r.support]}</Badge>
            </span>
            {r.until && <span>{r.support === "eol" ? "no security fixes since " + r.until : "security fixes until " + r.until}</span>}
            {r.support !== "eol" && left >= 0 && left <= 60 && (
              <p className="ui-error">
                Security fixes end in {left === 1 ? "1 day" : left + " days"}. Move its sites to a newer version.
              </p>
            )}
          </div>
        );
      },
    },
    ...(sites ? [{ key: "sites", header: "Sites", sortValue: (r: Row) => r.sites ?? 0, cell: (r: Row) => r.sites ?? "" }] : []),
    {
      key: "status",
      header: "Status",
      sortValue: (r) => (r.installed ? 0 : 1),
      cell: (r) =>
        inst.state === "running" && inst.version === r.version ? (
          <span aria-busy="true">{inst.action === "remove" ? "Removing…" : "Installing…"}</span>
        ) : r.installed ? (
          <span className="ui-row">
            <Badge tone="ok">Installed</Badge>
            {r.sites === 0 && versions.distro !== r.version && (
              <IconButton label={"Remove PHP " + r.version} icon={<Trash size={16} aria-hidden="true" />} danger disabled={running} onClick={() => setConfirmRemove(r.version)} />
            )}
          </span>
        ) : (
          <Button
            icon={<DownloadSimple size={16} aria-hidden="true" />}
            disabled={running}
            onClick={() => (r.support === "eol" ? setConfirm(r.version) : void start(r.version, false))}
          >
            Install PHP {r.version}
          </Button>
        ),
    },
  ];
  const sortKey = query.get("sort");
  const sort: Sort = sortKey ? { key: sortKey, dir: query.get("dir") === "asc" ? "asc" : "desc" } : { key: "version", dir: "desc" };

  return (
    <>
      {head}
      {error && <Notice tone="err" title={error} />}

      {confirm && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="php-eol-title">
          <h2 id="php-eol-title" className="ui-flush">
            Install PHP {confirm} without security fixes?
          </h2>
          <p className="ui-flush">
            PHP {confirm} gets no security fixes from PHP since {versions.securityUntil?.[confirm] ?? "its end of life"}. Install it only
            for an old application that cannot move yet, and plan the move.
          </p>
          <div className="ui-row">
            <Button variant="danger" onClick={() => void start(confirm, true)}>
              Install anyway
            </Button>
            <Button onClick={() => setConfirm(null)}>Cancel</Button>
          </div>
        </section>
      )}

      {confirmRemove && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="php-remove-title">
          <h2 id="php-remove-title" className="ui-flush">
            Remove PHP {confirmRemove}?
          </h2>
          <p className="ui-flush">No site runs on it. Its packages and settings are removed from the server; you can install it again later.</p>
          <div className="ui-row">
            <Button variant="danger" onClick={() => void remove(confirmRemove)}>
              Remove
            </Button>
            <Button onClick={() => setConfirmRemove(null)}>Cancel</Button>
          </div>
        </section>
      )}

      {inst.state !== "idle" && inst.version && (
        <Card
          title={
            inst.action === "remove"
              ? inst.state === "running"
                ? "Removing PHP " + inst.version + "…"
                : inst.state === "succeeded"
                  ? "PHP " + inst.version + " was removed"
                  : "Removing PHP " + inst.version + " failed"
              : inst.state === "running"
                ? "Installing PHP " + inst.version + "…"
                : inst.state === "succeeded"
                  ? "PHP " + inst.version + " was installed"
                  : "Installing PHP " + inst.version + " failed"
          }
        >
          <div className="ui-stack">
            {inst.state === "failed" && (
              <Notice tone="err" title={(inst.action === "remove" ? "The removal" : "The installation") + " did not finish. The log shows why; you can try again."} />
            )}
            {inst.log.length > 0 && <pre className="ui-code" aria-label={inst.action === "remove" ? "Removal log" : "Installation log"}>{inst.log.join("\n")}</pre>}
          </div>
        </Card>
      )}

      <Card padded={false}>
        <div className="ui-filters">
          <select className="ui-select" aria-label="Show" value={onlyInstalled ? "installed" : ""} onChange={(e) => set({ show: e.target.value })}>
            <option value="">All versions</option>
            <option value="installed">Installed only</option>
          </select>
        </div>
        <Table
          label="PHP versions"
          columns={columns}
          rows={shown}
          rowKey={(r) => r.version}
          sort={sort}
          onSort={(s) => set({ sort: s.key, dir: s.dir })}
          empty="No version matches the filter."
        />
      </Card>

      {inst.newer.length > 0 && (
        <Notice tone="info" title={"Not yet released for Hostashi: PHP " + inst.newer.join(", PHP ")}>
          <p>sury.org offers {inst.newer.length === 1 ? "this version" : "these versions"} already. Support comes with a Hostashi update.</p>
        </Notice>
      )}
    </>
  );
}
