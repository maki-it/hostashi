// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { setDomains, setWWW, siteMessage, type Site, type SiteWWW, type WWWMode } from "./sites";
import { Button, Card, Notice } from "./ui";

const off: SiteWWW = { mode: "off", pairs: [], missing: [] };

// wwwExample is the pair the labels show: the site's first pair, else the
// primary domain and its partner.
export function wwwExample(site: Site): { bare: string; www: string } {
  const www = site.www ?? off;
  const first = www.pairs[0];
  if (first) return first;
  const primary = site.domains[0] ?? "example.com";
  return primary.startsWith("www.") ? { bare: primary.slice(4), www: primary } : { bare: primary, www: "www." + primary };
}

// wwwMessage says what a saved mode does.
export function wwwMessage(mode: WWWMode): string {
  switch (mode) {
    case "to-www":
      return "Saved. The names without www now redirect to the www names.";
    case "to-non-www":
      return "Saved. The www names now redirect to the names without www.";
    default:
      return "Saved. Every domain shows the site again.";
  }
}

// WWWCard sets the redirect between a domain and its www name (ADR-0065).
// It works on pairs only, so both names must be domains of the site; the
// card offers to add the missing one. onDomains gets the new domain list
// after such an add.
export function WWWCard({ site, onChange, onDomains }: { site: Site; onChange: () => Promise<void>; onDomains: (d: string[]) => void }) {
  const www = site.www ?? off;
  const ex = wwwExample(site);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const msg = await f();
      await onChange();
      setDone(msg);
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const pick = (mode: WWWMode) =>
    act(async () => {
      await setWWW(site.name, mode);
      return wwwMessage(mode);
    });

  const add = (name: string) =>
    act(async () => {
      const list = [...site.domains, name];
      await setDomains(site.name, list);
      onDomains(list);
      return site.tls.enabled
        ? `${name} added. The site is served over HTTP until its certificate also names ${name}.`
        : `${name} added. You can choose the redirect now.`;
    });

  const noPair = www.pairs.length === 0;
  const scheme = site.tls.active ? "https" : "http";
  const choices: { mode: WWWMode; label: string; hint: string }[] = [
    { mode: "off", label: "No redirect", hint: "Every domain shows the site." },
    { mode: "to-non-www", label: "Redirect to the name without www", hint: `${ex.www} → ${ex.bare}` },
    { mode: "to-www", label: "Redirect to the www name", hint: `${ex.bare} → ${ex.www}` },
  ];

  return (
    <Card title="www redirect">
      <div className="ui-stack">
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>Between a domain and its www name</legend>
          {choices.map((c) => (
            <label className="ui-check" key={c.mode}>
              <input
                type="radio"
                name={site.name + "-www"}
                value={c.mode}
                checked={www.mode === c.mode}
                disabled={busy || (noPair && c.mode !== "off")}
                onChange={() => void pick(c.mode)}
              />
              <span>
                {c.label}
                {c.mode === "off" ? <span className="ui-hint ui-flush"> · {c.hint}</span> : <span className="ui-hint ui-flush"> · <code>{c.hint}</code></span>}
              </span>
            </label>
          ))}
        </fieldset>

        {noPair && (
          <>
            <p className="ui-hint ui-flush">
              The redirect needs a domain and its www name, both as domains of this site.
              {www.missing.length > 0 && " Add the missing name first; its certificate is requested with it."}
            </p>
            {www.missing.length > 0 && (
              <div className="ui-row">
                {www.missing.slice(0, 3).map((m) => (
                  <Button key={m} disabled={busy} onClick={() => void add(m)}>
                    Add <span className="ui-mono">{m}</span>
                  </Button>
                ))}
              </div>
            )}
            {www.mode !== "off" && <Notice tone="warn" title="No domain has its www partner any more, so the redirect has no effect." />}
          </>
        )}

        {!noPair && www.mode !== "off" && (
          <ul className="ui-hint ui-flush ui-mono">
            {www.pairs.map((p) => {
              const [from, to] = www.mode === "to-www" ? [p.bare, p.www] : [p.www, p.bare];
              return (
                <li key={p.bare}>
                  {from} → {scheme}://{to}
                </li>
              );
            })}
          </ul>
        )}

        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </div>
    </Card>
  );
}
