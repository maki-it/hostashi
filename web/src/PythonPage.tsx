// SPDX-License-Identifier: AGPL-3.0-or-later

// The Python lines Hostashi installs from the CPython builds of
// python-build-standalone, each checked against its pinned checksum
// (ADR-0061).

import { DownloadSimple, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { getPython, installPython, pythonMessage, removePython, sortPythonLines, type PythonRelease, type PythonStatus } from "./python";
import { daysUntil, todayUTC } from "./php";
import { listSites, type Site } from "./sites";
import { Badge, Button, Card, IconButton, Notice, PageHead, Table, type Column } from "./ui";

type Row = PythonRelease & { installed: string; installedBuild: string; sites: number | null };

export function PythonPage({ today = todayUTC(), poll = 3000 }: { today?: string; poll?: number }) {
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

  const head = (
    <PageHead
      title="Python versions"
      description="The supported lines of Python, from the CPython builds of python-build-standalone. Hostashi checks every download against its pinned checksum; a Hostashi update brings newer releases and rebuilds."
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
      header: "Version",
      cell: (r) => (
        <div className="ui-cell-main">
          <strong>Python {r.line}</strong>
          <span>
            release {r.version}, build {r.build}
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
              <Badge tone={left < 0 ? "err" : left <= 180 ? "warn" : "ok"}>{left < 0 ? "End of life" : "Supported"}</Badge>
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
          {r.installed === r.version && r.installedBuild === r.build ? (
            <Badge tone="ok">Installed</Badge>
          ) : running && status.last === r.line ? (
            <span aria-busy="true">Installing…</span>
          ) : (
            <Button icon={<DownloadSimple size={16} aria-hidden="true" />} disabled={running || busy} onClick={() => void start(r.line)}>
              {r.installed === r.version ? `Update to build ${r.build}` : r.installed ? `Update to ${r.version}` : `Install Python ${r.line}`}
            </Button>
          )}
          {r.installed && r.sites === 0 && (
            <IconButton label={"Remove Python " + r.line} icon={<Trash size={16} aria-hidden="true" />} danger disabled={running || busy} onClick={() => setConfirm(r.line)} />
          )}
        </span>
      ),
    },
  ];

  return (
    <>
      {head}
      {error && <Notice tone="err" title={error} />}
      {removed && <Notice tone="ok" title={"Python " + removed + " was removed."} />}
      {confirm && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="python-remove-title">
          <h2 id="python-remove-title" className="ui-flush">
            Remove Python {confirm}?
          </h2>
          <p className="ui-flush">No site runs on it. Its files under /opt/hostashi/python are deleted; you can install it again later.</p>
          <div className="ui-row">
            <Button variant="danger" disabled={busy} onClick={() => void remove(confirm)}>
              Remove
            </Button>
            <Button onClick={() => setConfirm(null)}>Cancel</Button>
          </div>
        </section>
      )}
      {status.state !== "idle" && status.last && (
        <Card
          title={
            status.state === "running"
              ? "Installing Python " + status.last + "…"
              : status.state === "succeeded"
                ? "Python " + status.last + " was installed"
                : "Installing Python " + status.last + " failed"
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
        <Table label="Python versions" columns={columns} rows={rows} rowKey={(r) => r.line} empty="No Python version is offered." />
      </Card>
      <p className="ui-hint ui-flush">
        A Python site runs on the version chosen in its app settings, from its own environment ~/.venv. A newer release or build of a line keeps the environments and restarts their apps. A version can be removed once no site uses it.
      </p>
    </>
  );
}
