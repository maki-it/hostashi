// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { deleteParked, listParked, parkedMessage, setParked, type ParkedDomain, type ParkedTLS } from "./parked";
import { listPages, type StatusPage } from "./pages";
import { domainProblem } from "./sites";

function httpsLabel(t: ParkedTLS): string {
  if (!t.enabled) return "Off";
  if (t.active) return "On" + (t.notAfter ? ", valid until " + t.notAfter.slice(0, 10) : "");
  return t.error ? "Waiting: the last attempt failed" : "Waiting for the certificate";
}

// Parked lists the parked domains and parks new ones. A parked domain shows
// a parking page on every path; it is dropped when a site takes the domain.
export function Parked() {
  const [parked, setList] = useState<ParkedDomain[] | null>(null);
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const [list, all] = await Promise.all([listParked(), listPages()]);
      setList(list);
      setPages(all.filter((p) => p.kind === "parking"));
      setError("");
    } catch (err) {
      setError(parkedMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // A certificate comes in the background: look again while one is pending.
  const pending = parked?.some((p) => p.tls.enabled && !p.tls.active && !p.tls.error) ?? false;
  useEffect(() => {
    if (!pending) return;
    const t = setInterval(() => void load(), 10000);
    return () => clearInterval(t);
  }, [pending, load]);

  if (!parked) {
    return error ? (
      <p role="alert" className="card">
        {error}
      </p>
    ) : (
      <p aria-busy="true">Loading…</p>
    );
  }

  return (
    <div className="stack">
      <ParkCard pages={pages} onChange={load} />
      <section className="card" aria-labelledby="parked-list">
        <h2 id="parked-list">Parked domains</h2>
        {error && <p role="alert">{error}</p>}
        {parked.length === 0 ? (
          <p className="muted">No domain is parked.</p>
        ) : (
          <ul className="rows">
            {parked.map((p) => (
              <ParkedRow key={p.domain} parked={p} pages={pages} onChange={load} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function PageSelect({ pages, value, onChange, label }: { pages: StatusPage[]; value: string; onChange: (v: string) => void; label: string }) {
  return (
    <label>
      {label}
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        {pages.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name}
          </option>
        ))}
      </select>
    </label>
  );
}

function ParkCard({ pages, onChange }: { pages: StatusPage[]; onChange: () => Promise<void> }) {
  const [domain, setDomain] = useState("");
  const [page, setPage] = useState("builtin-parking");
  const [tls, setTls] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    const d = domain.trim().toLowerCase();
    const problem = d === "" ? "Enter a domain." : domainProblem(d);
    if (problem) {
      setError(problem);
      setDone("");
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      const p = await setParked(d, page, tls);
      setDone(
        p.tls.enabled && !p.tls.active
          ? `${p.domain} is parked. It is served over HTTP until its certificate is there.`
          : `${p.domain} is parked.`,
      );
      setDomain("");
      await onChange();
    } catch (err) {
      setError(parkedMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card" aria-labelledby="park-new">
      <h2 id="park-new">Park a domain</h2>
      <form onSubmit={(e) => void submit(e)} noValidate>
        <label>
          Domain
          <small>A domain without a site, like example.com. Its DNS must point to this server.</small>
          <input value={domain} onChange={(e) => setDomain(e.target.value)} autoComplete="off" spellCheck={false} />
        </label>
        <PageSelect pages={pages} value={page} onChange={setPage} label="Page" />
        <label className="check">
          <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
          HTTPS with a Let's Encrypt certificate
        </label>
        {error && <p role="alert">{error}</p>}
        {done && <p role="status">{done}</p>}
        <button type="submit" disabled={busy}>
          {busy ? "Parking…" : "Park domain"}
        </button>
      </form>
    </section>
  );
}

function ParkedRow({ parked, pages, onChange }: { parked: ParkedDomain; pages: StatusPage[]; onChange: () => Promise<void> }) {
  const [mode, setMode] = useState<"" | "edit" | "delete">("");
  const [page, setPage] = useState(parked.page);
  const [tls, setTls] = useState(parked.tls.enabled);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const msg = await f();
      setMode("");
      setDone(msg);
      await onChange();
    } catch (err) {
      setError(parkedMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function save(e: FormEvent) {
    e.preventDefault();
    void act(async () => {
      await setParked(parked.domain, page, tls);
      return "Saved.";
    });
  }

  const pageName = pages.find((p) => p.id === parked.page)?.name ?? parked.page;

  return (
    <li>
      <span>
        <strong>{parked.domain}</strong>
      </span>
      {mode === "" && (
        <span className="actions">
          <button
            type="button"
            className="secondary"
            onClick={() => {
              setPage(parked.page);
              setTls(parked.tls.enabled);
              setMode("edit");
            }}
          >
            Change
          </button>
          <button type="button" className="secondary danger" onClick={() => setMode("delete")}>
            Unpark
          </button>
        </span>
      )}
      <dl className="facts" aria-label={"Details of " + parked.domain}>
        <dt>Page</dt>
        <dd>{pageName}</dd>
        <dt>HTTPS</dt>
        <dd>{httpsLabel(parked.tls)}</dd>
      </dl>
      {parked.tls.enabled && !parked.tls.active && parked.tls.error && (
        <p className="muted">Last attempt failed: {parked.tls.error}. Hostashi tries again within an hour.</p>
      )}
      {mode === "edit" && (
        <form onSubmit={save} noValidate>
          <PageSelect pages={pages} value={page} onChange={setPage} label={"Page for " + parked.domain} />
          <label className="check">
            <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
            HTTPS for {parked.domain}
          </label>
          <div className="actions">
            <button type="submit" disabled={busy}>
              Save
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setMode("")}>
              Cancel
            </button>
          </div>
        </form>
      )}
      {mode === "delete" && (
        <div>
          <p>Unpark {parked.domain}? The web server stops answering for it and its certificate is removed.</p>
          <div className="actions">
            <button
              type="button"
              className="danger"
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await deleteParked(parked.domain);
                  return "";
                })
              }
            >
              Unpark {parked.domain}
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setMode("")}>
              Cancel
            </button>
          </div>
        </div>
      )}
      {error && <p role="alert">{error}</p>}
      {done && <p role="status">{done}</p>}
    </li>
  );
}
