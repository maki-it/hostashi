// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from "react";
import { daysUntil, getPHPInstall, installPHP, phpMessage, support, todayUTC, type PHPInstall, type Support } from "./php";
import { phpVersions, type PHPVersions } from "./sites";

const supportLabel: Record<Support, string> = {
  active: "Supported",
  security: "Security fixes only",
  eol: "End of life",
};

// Badge shows upstream's support for a version; the date says until when.
function Badge({ s }: { s: Support }) {
  return <span className={"badge" + (s === "active" ? " ok" : s === "security" ? " warn" : "")}>{supportLabel[s]}</span>;
}

// PHPPage lists the PHP versions Hostashi can run, installs more of them
// from sury.org (ADR-0036), and shows newer versions that sury.org already
// offers but Hostashi does not run yet.
export function PHPPage({ today = todayUTC(), poll = 3000 }: { today?: string; poll?: number }) {
  const [versions, setVersions] = useState<PHPVersions | null>(null);
  const [inst, setInst] = useState<PHPInstall | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<string | null>(null);

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

  if (!versions || !inst) {
    return error ? (
      <div role="alert" className="card">
        <p>{error}</p>
        <button type="button" onClick={() => void load()}>
          Try again
        </button>
      </div>
    ) : (
      <p aria-busy="true">Loading…</p>
    );
  }

  const list = [...versions.supported].reverse();

  return (
    <div className="stack">
      <section className="card" aria-labelledby="php-title">
        <h2 id="php-title">PHP versions</h2>
        <p className="muted">
          The distribution&apos;s PHP is always installed. More versions come from the sury.org repository; installing one
          sets it up. Installed versions get their updates automatically.
        </p>
        {error && <p role="alert">{error}</p>}
        <table>
          <thead>
            <tr>
              <th scope="col">Version</th>
              <th scope="col">Upstream support</th>
              <th scope="col">Status</th>
            </tr>
          </thead>
          <tbody>
            {list.map((v) => {
              const s = support(versions, v, today);
              const until = versions.securityUntil?.[v];
              const installed = versions.installed.includes(v);
              const left = until ? daysUntil(until, today) : -1;
              return (
                <tr key={v}>
                  <th scope="row">PHP {v}</th>
                  <td>
                    <Badge s={s} />{" "}
                    {until && (
                      <span className="muted">
                        {s === "eol" ? "no security fixes since " + until : "security fixes until " + until}
                      </span>
                    )}
                    {s !== "eol" && left >= 0 && left <= 60 && (
                      <p>Security fixes end in {left === 1 ? "1 day" : left + " days"}. Move its sites to a newer version.</p>
                    )}
                  </td>
                  <td>
                    {installed ? (
                      "Installed"
                    ) : inst.state === "running" && inst.version === v ? (
                      <span aria-busy="true">Installing…</span>
                    ) : (
                      <button
                        type="button"
                        disabled={running}
                        onClick={() => (s === "eol" ? setConfirm(v) : void start(v, false))}
                      >
                        Install PHP {v}
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
            {inst.newer.map((v) => (
              <tr key={v} className="muted" aria-disabled="true">
                <th scope="row">PHP {v}</th>
                <td colSpan={2}>Offered by sury.org, not yet released for Hostashi. It comes with a Hostashi update.</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      {confirm && (
        <section className="card" role="alertdialog" aria-labelledby="php-eol-title">
          <h3 id="php-eol-title">Install PHP {confirm} without security fixes?</h3>
          <p>
            PHP {confirm} gets no security fixes from PHP since {versions.securityUntil?.[confirm] ?? "its end of life"}. Install it only
            for an old application that cannot move yet, and plan the move.
          </p>
          <div className="actions">
            <button type="button" className="danger" onClick={() => void start(confirm, true)}>
              Install anyway
            </button>
            <button type="button" className="secondary" onClick={() => setConfirm(null)}>
              Cancel
            </button>
          </div>
        </section>
      )}

      {inst.state !== "idle" && inst.version && (
        <section className="card" aria-labelledby="php-log-title">
          <h3 id="php-log-title">
            {inst.state === "running"
              ? "Installing PHP " + inst.version + "…"
              : inst.state === "succeeded"
                ? "PHP " + inst.version + " was installed"
                : "Installing PHP " + inst.version + " failed"}
          </h3>
          {inst.state === "failed" && <p role="alert">The installation did not finish. The log shows why; you can try again.</p>}
          {inst.log.length > 0 && <pre aria-label="Installation log">{inst.log.join("\n")}</pre>}
        </section>
      )}
    </div>
  );
}
