// SPDX-License-Identifier: AGPL-3.0-or-later

// The Python lines Hostashi installs from the CPython builds of
// python-build-standalone, each checked against its pinned checksum
// (ADR-0061).

import { RuntimesHead } from "./Runtimes";
import { DownloadSimple, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getPython, installPython, pythonMessage, removePython, sortPythonLines, type PythonRelease, type PythonStatus } from "./python";
import { daysUntil, formatDay, todayUTC } from "./php";
import { listSites, type Site } from "./sites";
import { Badge, Button, Card, IconButton, JobCard, Notice, Table, useJobCard, type Column } from "./ui";

type Row = PythonRelease & { installed: string; installedBuild: string; sites: number | null };

export function PythonPage({ today = todayUTC(), poll = 3000 }: { today?: string; poll?: number }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<PythonStatus | null>(null);
  const [sites, setSites] = useState<Site[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<string | null>(null);
  const [removed, setRemoved] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setStatus(await getPython());
      setError("");
    } catch (err) {
      setError(pythonMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
    listSites()
      .then(setSites)
      .catch(() => setSites(null));
  }, [load]);

  const running = status?.state === "running";
  const job = useJobCard(status?.state);
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void load(), poll);
    return () => clearInterval(t);
  }, [running, load, poll]);

  const start = async (line: string) => {
    setRemoved("");
    try {
      setStatus(await installPython(line));
      setError("");
    } catch (err) {
      setError(pythonMessage(err));
    }
  };

  const remove = async (line: string) => {
    setBusy(true);
    try {
      setStatus(await removePython(line));
      setError("");
      setRemoved(line);
    } catch (err) {
      setError(pythonMessage(err));
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  };

  const head = <RuntimesHead active="python" description={t("server.runtimes.python.description")} />;

  if (!status) {
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

  const order = sortPythonLines(status.releases.map((r) => r.line));
  const rows: Row[] = [...status.releases]
    .sort((a, b) => order.indexOf(a.line) - order.indexOf(b.line))
    .map((r) => {
      const i = status.installed.find((x) => x.line === r.line);
      return { ...r, installed: i?.version ?? "", installedBuild: i?.build ?? "", sites: sites ? sites.filter((s) => s.python?.version === r.line).length : null };
    });

  const columns: Column<Row>[] = [
    {
      key: "line",
      header: t("server.runtimes.version"),
      cell: (r) => (
        <div className="ui-cell-main">
          <strong>{"Python " + r.line}</strong>
          <span>{t("server.runtimes.python.release", { version: r.version, build: r.build })}</span>
        </div>
      ),
    },
    {
      key: "support",
      header: t("server.runtimes.upstream"),
      cell: (r) => {
        const left = daysUntil(r.end, today);
        return (
          <div className="ui-cell-main">
            <span className="ui-row">
              <Badge tone={left < 0 ? "err" : left <= 180 ? "warn" : "ok"}>{left < 0 ? t("server.runtimes.endOfLife") : t("server.runtimes.supported")}</Badge>
            </span>
            <span>{left < 0 ? t("server.runtimes.noFixesSince", { date: formatDay(r.end) }) : t("server.runtimes.fixesUntil", { date: formatDay(r.end) })}</span>
            {left >= 0 && left <= 180 && <p className="ui-error">{t("server.runtimes.fixesEnd", { count: left })}</p>}
          </div>
        );
      },
    },
    ...(sites ? [{ key: "sites", header: t("server.runtimes.sites"), cell: (r: Row) => r.sites ?? "" }] : []),
    {
      key: "status",
      header: t("server.runtimes.status"),
      cell: (r) => (
        <span className="ui-row">
          {r.installed === r.version && r.installedBuild === r.build ? (
            <Badge tone="ok">{t("server.runtimes.installed")}</Badge>
          ) : running && status.last === r.line ? (
            <span aria-busy="true">{t("server.runtimes.installing")}</span>
          ) : (
            <Button icon={<DownloadSimple size={16} aria-hidden="true" />} disabled={running || busy} onClick={() => void start(r.line)}>
              {r.installed === r.version
                ? t("server.runtimes.python.updateBuild", { build: r.build })
                : r.installed
                  ? t("server.runtimes.updateTo", { version: r.version })
                  : t("server.runtimes.installName", { name: "Python " + r.line })}
            </Button>
          )}
          {r.installed && r.sites === 0 && (
            <IconButton label={t("server.runtimes.removeName", { name: "Python " + r.line })} text={t("server.runtimes.remove")} icon={<Trash size={16} aria-hidden="true" />} danger disabled={running || busy} onClick={() => setConfirm(r.line)} />
          )}
        </span>
      ),
    },
  ];

  return (
    <>
      {head}
      {error && <Notice tone="err" title={error} />}
      {removed && <Notice tone="ok" title={t("server.runtimes.removed", { name: "Python " + removed })} />}
      {confirm && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="python-remove-title">
          <h2 id="python-remove-title" className="ui-flush">
            {t("server.runtimes.removeTitle", { name: "Python " + confirm })}
          </h2>
          <p className="ui-flush">{t("server.runtimes.python.removeText")}</p>
          <div className="ui-row">
            <Button variant="danger" disabled={busy} onClick={() => void remove(confirm)}>
              {t("server.runtimes.remove")}
            </Button>
            <Button onClick={() => setConfirm(null)}>{t("common.actions.cancel")}</Button>
          </div>
        </section>
      )}
      {job.show && status.last && (
        <JobCard
          title={
            t(
              status.state === "running"
                ? "server.runtimes.jobInstalling"
                : status.state === "succeeded"
                  ? "server.runtimes.jobInstalled"
                  : "server.runtimes.jobInstallFailed",
              { name: "Python " + status.last },
            )
          }
          state={status.state}
          log={status.log}
          logLabel={t("server.runtimes.installLog")}
          onClose={job.close}
        >
          {status.state === "failed" && <Notice tone="err" title={t("server.runtimes.installFailed")} />}
        </JobCard>
      )}
      <Card padded={false}>
        <Table label={t("server.runtimes.python.table")} columns={columns} rows={rows} rowKey={(r) => r.line} empty={t("server.runtimes.python.none")} />
      </Card>
      <p className="ui-hint ui-flush">{t("server.runtimes.python.footer")}</p>
    </>
  );
}
