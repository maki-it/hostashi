// SPDX-License-Identifier: AGPL-3.0-or-later

// The Node.js LTS lines Hostashi installs from the official nodejs.org
// builds, each checked against its pinned checksum (ADR-0054).

import { RuntimesHead } from "./Runtimes";
import { DownloadSimple, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getNode, installNode, nodeMessage, removeNode, type NodeRelease, type NodeStatus } from "./node";
import { daysUntil, formatDay, todayUTC } from "./php";
import { listSites, type Site } from "./sites";
import { Badge, Button, Card, IconButton, JobCard, Notice, Table, useJobCard, type Column } from "./ui";

type Row = NodeRelease & { installed: string; sites: number | null };

export function NodePage({ today = todayUTC(), poll = 3000 }: { today?: string; poll?: number }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<NodeStatus | null>(null);
  const [sites, setSites] = useState<Site[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<string | null>(null);
  const [removed, setRemoved] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setStatus(await getNode());
      setError("");
    } catch (err) {
      setError(nodeMessage(err));
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

  const start = async (major: string) => {
    setRemoved("");
    try {
      setStatus(await installNode(major));
      setError("");
    } catch (err) {
      setError(nodeMessage(err));
    }
  };

  const remove = async (major: string) => {
    setBusy(true);
    try {
      setStatus(await removeNode(major));
      setError("");
      setRemoved(major);
    } catch (err) {
      setError(nodeMessage(err));
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  };

  const head = <RuntimesHead active="node" description={t("server.runtimes.node.description")} />;

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

  const rows: Row[] = [...status.releases]
    .sort((a, b) => Number(b.major) - Number(a.major))
    .map((r) => ({
      ...r,
      installed: status.installed.find((i) => i.major === r.major)?.version ?? "",
      sites: sites ? sites.filter((s) => s.node?.version === r.major).length : null,
    }));

  const columns: Column<Row>[] = [
    {
      key: "line",
      header: t("server.runtimes.version"),
      cell: (r) => (
        <div className="ui-cell-main">
          <strong>Node.js {r.major}</strong>
          <span>{t("server.runtimes.node.release", { codename: r.codename, version: r.version })}</span>
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
              <Badge tone={left < 0 ? "err" : left <= 180 ? "warn" : "ok"}>{left < 0 ? t("server.runtimes.endOfLife") : t("server.runtimes.node.lts")}</Badge>
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
          {r.installed === r.version ? (
            <Badge tone="ok">{t("server.runtimes.installed")}</Badge>
          ) : running && status.last === r.major ? (
            <span aria-busy="true">{t("server.runtimes.installing")}</span>
          ) : (
            <Button icon={<DownloadSimple size={16} aria-hidden="true" />} disabled={running || busy} onClick={() => void start(r.major)}>
              {r.installed ? t("server.runtimes.updateTo", { version: r.version }) : t("server.runtimes.installName", { name: "Node.js " + r.major })}
            </Button>
          )}
          {r.installed && r.sites === 0 && (
            <IconButton label={t("server.runtimes.removeName", { name: "Node.js " + r.major })} text={t("server.runtimes.remove")} icon={<Trash size={16} aria-hidden="true" />} danger disabled={running || busy} onClick={() => setConfirm(r.major)} />
          )}
        </span>
      ),
    },
  ];

  return (
    <>
      {head}
      {error && <Notice tone="err" title={error} />}
      {removed && <Notice tone="ok" title={t("server.runtimes.removed", { name: "Node.js " + removed })} />}
      {confirm && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="node-remove-title">
          <h2 id="node-remove-title" className="ui-flush">
            {t("server.runtimes.removeTitle", { name: "Node.js " + confirm })}
          </h2>
          <p className="ui-flush">{t("server.runtimes.node.removeText")}</p>
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
              { name: "Node.js " + status.last },
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
        <Table label={t("server.runtimes.node.table")} columns={columns} rows={rows} rowKey={(r) => r.major} empty={t("server.runtimes.node.none")} />
      </Card>
      <p className="ui-hint ui-flush">{t("server.runtimes.node.footer")}</p>
    </>
  );
}
