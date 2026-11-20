// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from "react";
import { checkTitle, doctorMessage, runDoctor, type DoctorCheck, type DoctorReport, type DoctorStatus } from "./doctor";

const labels: Record<DoctorStatus, string> = { ok: "OK", warn: "Warning", fail: "Failed" };

const Status = ({ status }: { status: DoctorStatus }) => (
  <span className={"badge" + (status === "ok" ? " ok" : status === "warn" ? " warn" : "")}>{labels[status]}</span>
);

function overall(r: DoctorReport): string {
  if (r.status === "ok") return "Everything is fine.";
  const parts = [];
  if (r.counts.fail) parts.push(r.counts.fail === 1 ? "1 check failed" : r.counts.fail + " checks failed");
  if (r.counts.warn) parts.push(r.counts.warn === 1 ? "1 warning" : r.counts.warn + " warnings");
  return parts.join(", ") + ".";
}

// Doctor shows the checks of hostashi doctor: the server first, then each
// site. Nothing here changes the server.
export function Doctor() {
  const [report, setReport] = useState<DoctorReport | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const [problemsOnly, setProblemsOnly] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      setReport(await runDoctor());
      setError("");
    } catch (err) {
      setError(doctorMessage(err));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!report) {
    return error ? (
      <div role="alert" className="card">
        <p>{error}</p>
        <button type="button" onClick={() => void load()}>
          Try again
        </button>
      </div>
    ) : (
      <p aria-busy="true">Running the checks…</p>
    );
  }

  const shown = report.checks.filter((c) => !problemsOnly || c.status !== "ok");
  const system = shown.filter((c) => !c.site);
  const sites = new Map<string, DoctorCheck[]>();
  for (const c of shown) {
    if (c.site) sites.set(c.site, [...(sites.get(c.site) ?? []), c]);
  }

  return (
    <div className="stack">
      <section className="card" aria-labelledby="doctor-title">
        <h2 id="doctor-title">
          Health <Status status={report.status} />
        </h2>
        <p>{overall(report)}</p>
        <p className="muted">Checked at {new Date(report.checkedAt).toLocaleString()}. The checks only read the server and change nothing.</p>
        {error && <p role="alert">{error}</p>}
        <div className="actions">
          <button type="button" disabled={busy} onClick={() => void load()}>
            {busy ? "Checking…" : "Check again"}
          </button>
          <label className="check">
            <input type="checkbox" checked={problemsOnly} onChange={(e) => setProblemsOnly(e.target.checked)} />
            Show only problems
          </label>
        </div>
      </section>
      <CheckList title="Server" checks={system} />
      {[...sites.entries()].map(([site, checks]) => (
        <CheckList key={site} title={"Site " + site} checks={checks} />
      ))}
    </div>
  );
}

function CheckList({ title, checks }: { title: string; checks: DoctorCheck[] }) {
  if (checks.length === 0) return null;
  const id = "doctor-" + title.replace(/\W+/g, "-").toLowerCase();
  return (
    <section className="card" aria-labelledby={id}>
      <h2 id={id}>{title}</h2>
      <ul className="rows">
        {checks.map((c, i) => (
          <li key={c.id + i}>
            <span>
              <strong>{checkTitle(c.id)}</strong> <Status status={c.status} />
            </span>
            <p>{c.summary}</p>
            {c.hint && <p className="muted">{c.hint}</p>}
          </li>
        ))}
      </ul>
    </section>
  );
}
