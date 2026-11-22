// SPDX-License-Identifier: AGPL-3.0-or-later

// The Node.js LTS lines Hostashi installs from the official nodejs.org
// builds, each checked against its pinned checksum (ADR-0054).

import { DownloadSimple } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { getNode, installNode, nodeMessage, type NodeRelease, type NodeStatus } from "./node";
import { daysUntil, todayUTC } from "./php";
import { listSites, type Site } from "./sites";
import { Badge, Button, Card, Notice, PageHead, Table, type Column } from "./ui";

type Row = NodeRelease & { installed: string; sites: number | null };

export function NodePage({ today = todayUTC(), poll = 3000 }: { today?: string; poll?: number }) {
  const [status, setStatus] = useState<NodeStatus | null>(null);
  const [sites, setSites] = useState<Site[] | null>(null);
  const [error, setError] = useState("");

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
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void load(), poll);
    return () => clearInterval(t);
  }, [running, load, poll]);

  const start = async (major: string) => {
    try {
      setStatus(await installNode(major));
      setError("");
    } catch (err) {
      setError(nodeMessage(err));
    }
  };

  const head = (
    <PageHead
      title="Node.js versions"
      description="The long-term support lines of Node.js, from the official nodejs.org builds. Hostashi checks every download against its pinned checksum; a Hostashi update brings newer releases."
    />
  );

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
      cell: (r) =>
        r.installed === r.version ? (
          <Badge tone="ok">Installed</Badge>
        ) : running && status.last === r.major ? (
          <span aria-busy="true">Installing…</span>
        ) : (
          <Button icon={<DownloadSimple size={16} aria-hidden="true" />} disabled={running} onClick={() => void start(r.major)}>
            {r.installed ? `Update to ${r.version}` : `Install Node.js ${r.major}`}
          </Button>
        ),
    },
  ];

  return (
    <>
      {head}
      {error && <Notice tone="err" title={error} />}
      {status.state !== "idle" && status.last && (
        <Card
          title={
            status.state === "running"
              ? "Installing Node.js " + status.last + "…"
              : status.state === "succeeded"
                ? "Node.js " + status.last + " was installed"
                : "Installing Node.js " + status.last + " failed"
          }
        >
          <div className="ui-stack">
            {status.state === "failed" && <Notice tone="err" title="The installation did not finish. The log shows why; you can try again." />}
            {status.log.length > 0 && (
              <pre className="ui-code" aria-label="Installation log">
                {status.log.join("\n")}
              </pre>
            )}
          </div>
        </Card>
      )}
      <Card padded={false}>
        <Table label="Node.js versions" columns={columns} rows={rows} rowKey={(r) => r.major} empty="No Node.js version is offered." />
      </Card>
      <p className="ui-hint ui-flush">Apps of a site run on the version chosen on the site's Web tab.</p>
    </>
  );
}
