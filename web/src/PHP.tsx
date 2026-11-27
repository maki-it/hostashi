// SPDX-License-Identifier: AGPL-3.0-or-later

import { RuntimesHead } from "./Runtimes";
import { DownloadSimple, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { daysUntil, formatDay, getPHPInstall, installPHP, phpMessage, removePHP, support, todayUTC, type PHPInstall, type Support } from "./php";
import { listSites, phpVersions, type PHPVersions, type Site } from "./sites";
import { Badge, Button, Card, IconButton, JobCard, Notice, Table, useJobCard, type Column, type Sort, type Tone } from "./ui";

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
  const { t } = useTranslation();
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
  const job = useJobCard(inst?.state);
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

  const head = <RuntimesHead active="php" description={t("server.runtimes.php.description")} />;

  if (!versions || !inst) {
    return (
      <>
        {head}
        {error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>{t("common.actions.tryAgain")}</Button>
          </Notice>
        ) : (
          <p aria-busy="true">{t("server.runtimes.loading")}</p>
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
      header: t("server.runtimes.version"),
      sortValue: (r) => r.version,
      cell: (r) => (
        <span className="ui-row">
          <strong>{"PHP " + r.version}</strong>
          {versions.distro === r.version && <Badge tone="off">{t("server.runtimes.php.distribution")}</Badge>}
        </span>
      ),
    },
    {
      key: "support",
      header: t("server.runtimes.upstream"),
      sortValue: (r) => r.until,
      cell: (r) => {
        const left = r.until ? daysUntil(r.until, today) : -1;
        return (
          <div className="ui-cell-main">
            <span className="ui-row">
              <Badge tone={supportTone[r.support]}>{t(`server.runtimes.php.support.${r.support}`)}</Badge>
            </span>
            {r.until && (
              <span>
                {r.support === "eol"
                  ? t("server.runtimes.php.noSecuritySince", { date: formatDay(r.until) })
                  : t("server.runtimes.php.securityUntil", { date: formatDay(r.until) })}
              </span>
            )}
            {r.support !== "eol" && left >= 0 && left <= 60 && (
              <p className="ui-error">{t("server.runtimes.php.securityEnds", { count: left })}</p>
            )}
          </div>
        );
      },
    },
    ...(sites ? [{ key: "sites", header: t("server.runtimes.sites"), sortValue: (r: Row) => r.sites ?? 0, cell: (r: Row) => r.sites ?? "" }] : []),
    {
      key: "status",
      header: t("server.runtimes.status"),
      sortValue: (r) => (r.installed ? 0 : 1),
      cell: (r) =>
        inst.state === "running" && inst.version === r.version ? (
          <span aria-busy="true">{inst.action === "remove" ? t("server.runtimes.removing") : t("server.runtimes.installing")}</span>
        ) : r.installed ? (
          <span className="ui-row">
            <Badge tone="ok">{t("server.runtimes.installed")}</Badge>
            {r.sites === 0 && versions.distro !== r.version && (
              <IconButton label={t("server.runtimes.removeName", { name: "PHP " + r.version })} text={t("server.runtimes.remove")} icon={<Trash size={16} aria-hidden="true" />} danger disabled={running} onClick={() => setConfirmRemove(r.version)} />
            )}
          </span>
        ) : (
          <Button
            icon={<DownloadSimple size={16} aria-hidden="true" />}
            disabled={running}
            onClick={() => (r.support === "eol" ? setConfirm(r.version) : void start(r.version, false))}
          >
            {t("server.runtimes.installName", { name: "PHP " + r.version })}
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
            {t("server.runtimes.php.eolTitle", { version: confirm })}
          </h2>
          <p className="ui-flush">
            {versions.securityUntil?.[confirm]
              ? t("server.runtimes.php.eolText", { version: confirm, date: formatDay(versions.securityUntil[confirm]) })
              : t("server.runtimes.php.eolTextNoDate", { version: confirm })}
          </p>
          <div className="ui-row">
            <Button variant="danger" onClick={() => void start(confirm, true)}>
              {t("server.runtimes.php.installAnyway")}
            </Button>
            <Button onClick={() => setConfirm(null)}>{t("common.actions.cancel")}</Button>
          </div>
        </section>
      )}

      {confirmRemove && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="php-remove-title">
          <h2 id="php-remove-title" className="ui-flush">
            {t("server.runtimes.removeTitle", { name: "PHP " + confirmRemove })}
          </h2>
          <p className="ui-flush">{t("server.runtimes.php.removeText")}</p>
          <div className="ui-row">
            <Button variant="danger" onClick={() => void remove(confirmRemove)}>
              {t("server.runtimes.remove")}
            </Button>
            <Button onClick={() => setConfirmRemove(null)}>{t("common.actions.cancel")}</Button>
          </div>
        </section>
      )}

      {job.show && inst.version && (
        <JobCard
          title={
            t(
              inst.action === "remove"
                ? inst.state === "running"
                  ? "server.runtimes.jobRemoving"
                  : inst.state === "succeeded"
                    ? "server.runtimes.jobRemoved"
                    : "server.runtimes.jobRemoveFailed"
                : inst.state === "running"
                  ? "server.runtimes.jobInstalling"
                  : inst.state === "succeeded"
                    ? "server.runtimes.jobInstalled"
                    : "server.runtimes.jobInstallFailed",
              { name: "PHP " + inst.version },
            )
          }
          state={inst.state}
          log={inst.log}
          logLabel={inst.action === "remove" ? t("server.runtimes.removeLog") : t("server.runtimes.installLog")}
          onClose={job.close}
        >
          {inst.state === "failed" && (
            <Notice tone="err" title={inst.action === "remove" ? t("server.runtimes.removeFailed") : t("server.runtimes.installFailed")} />
          )}
        </JobCard>
      )}

      <Card padded={false}>
        <div className="ui-filters">
          <select className="ui-select" aria-label={t("server.runtimes.php.show")} value={onlyInstalled ? "installed" : ""} onChange={(e) => set({ show: e.target.value })}>
            <option value="">{t("server.runtimes.php.all")}</option>
            <option value="installed">{t("server.runtimes.php.onlyInstalled")}</option>
          </select>
        </div>
        <Table
          label={t("server.runtimes.php.table")}
          columns={columns}
          rows={shown}
          rowKey={(r) => r.version}
          sort={sort}
          onSort={(s) => set({ sort: s.key, dir: s.dir })}
          empty={t("server.runtimes.php.noMatch")}
        />
      </Card>

      {inst.newer.length > 0 && (
        <Notice tone="info" title={t("server.runtimes.php.newer", { versions: "PHP " + inst.newer.join(", PHP ") })}>
          <p>{t("server.runtimes.php.newerText", { count: inst.newer.length })}</p>
        </Notice>
      )}
    </>
  );
}
