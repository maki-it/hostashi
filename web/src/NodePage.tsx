// SPDX-License-Identifier: AGPL-3.0-or-later

// The Node.js LTS lines Hostashi installs from the official nodejs.org
// builds, each checked against its pinned checksum (ADR-0054).

import { RuntimesHead } from "./Runtimes";
import { DownloadSimple, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { getNode, installNode, nodeMessage, removeNode, type NodeRelease, type NodeStatus } from "./node";
import { daysUntil, todayUTC } from "./php";
import { listSites, type Site } from "./sites";
import { Badge, Button, Card, IconButton, JobCard, Notice, Table, useJobCard, type Column } from "./ui";

type Row = NodeRelease & { installed: string; sites: number | null };

export function NodePage({ today = todayUTC(), poll = 3000 }: { today?: string; poll?: number }) {
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

  const head = <RuntimesHead active="node" description={"The long-term support lines of Node.js, from the official nodejs.org builds. Hostashi checks every download against its pinned checksum; a Hostashi update brings newer releases."} />;

  if (!status) {
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
      header: "Version",
      cell: (r) => (
        <div className="ui-cell-main">
          <strong>Node.js {r.major}</strong>
          <span>
            {r.codename}, release {r.version}
          </span>
        </div>
      ),
    },
    {
      key: "support",
      header: "Upstream support",
      cell: (r) => {
        const left = daysUntil(r.end, today);
        return (
          <div className="ui-cell-main">
            <span className="ui-row">
              <Badge tone={left < 0 ? "err" : left <= 180 ? "warn" : "ok"}>{left < 0 ? "End of life" : "LTS"}</Badge>
            </span>
            <span>{left < 0 ? "no fixes since " + r.end : "fixes until " + r.end}</span>
            {left >= 0 && left <= 180 && <p className="ui-error">Fixes end in {left === 1 ? "1 day" : left + " days"}. Move its sites to a newer version.</p>}
          </div>
        );
      },
    },
    ...(sites ? [{ key: "sites", header: "Sites", cell: (r: Row) => r.sites ?? "" }] : []),
    {
      key: "status",
      header: "Status",
      cell: (r) => (
        <span className="ui-row">
          {r.installed === r.version ? (
            <Badge tone="ok">Installed</Badge>
          ) : running && status.last === r.major ? (
            <span aria-busy="true">Installing…</span>
          ) : (
            <Button icon={<DownloadSimple size={16} aria-hidden="true" />} disabled={running || busy} onClick={() => void start(r.major)}>
              {r.installed ? `Update to ${r.version}` : `Install Node.js ${r.major}`}
            </Button>
          )}
          {r.installed && r.sites === 0 && (
            <IconButton label={"Remove Node.js " + r.major} text="Remove" icon={<Trash size={16} aria-hidden="true" />} danger disabled={running || busy} onClick={() => setConfirm(r.major)} />
          )}
        </span>
      ),
    },
  ];

  return (
    <>
      {head}
      {error && <Notice tone="err" title={error} />}
      {removed && <Notice tone="ok" title={"Node.js " + removed + " was removed."} />}
      {confirm && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="node-remove-title">
          <h2 id="node-remove-title" className="ui-flush">
            Remove Node.js {confirm}?
          </h2>
          <p className="ui-flush">No site runs on it. Its files under /opt/hostashi/node are deleted; you can install it again later.</p>
          <div className="ui-row">
            <Button variant="danger" disabled={busy} onClick={() => void remove(confirm)}>
              Remove
            </Button>
            <Button onClick={() => setConfirm(null)}>Cancel</Button>
          </div>
        </section>
      )}
      {job.show && status.last && (
        <JobCard
          title={
            status.state === "running"
              ? "Installing Node.js " + status.last + "…"
              : status.state === "succeeded"
                ? "Node.js " + status.last + " was installed"
                : "Installing Node.js " + status.last + " failed"
          }
          state={status.state}
          log={status.log}
          logLabel="Installation log"
          onClose={job.close}
        >
          {status.state === "failed" && <Notice tone="err" title="The installation did not finish. The log shows why; you can try again." />}
        </JobCard>
      )}
      <Card padded={false}>
        <Table label="Node.js versions" columns={columns} rows={rows} rowKey={(r) => r.major} empty="No Node.js version is offered." />
      </Card>
      <p className="ui-hint ui-flush">Apps of a site run on the version chosen on the site's Web tab. A version can be removed once no site uses it.</p>
    </>
  );
}
