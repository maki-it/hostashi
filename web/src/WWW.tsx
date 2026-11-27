// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import i18n from "./i18n";
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
      return i18n.t("siteDomains.www.saved.toWWW");
    case "to-non-www":
      return i18n.t("siteDomains.www.saved.toNonWWW");
    default:
      return i18n.t("siteDomains.www.saved.off");
  }
}

// WWWCard sets the redirect between a domain and its www name (ADR-0065).
// It works on pairs only, so both names must be domains of the site; the
// card offers to add the missing one. onDomains gets the new domain list
// after such an add.
export function WWWCard({ site, onChange, onDomains }: { site: Site; onChange: () => Promise<void>; onDomains: (d: string[]) => void }) {
  const { t } = useTranslation();
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
      const s = await setDomains(site.name, list);
      onDomains(list);
      if (s.pendingDomains) return t("siteDomains.www.added.pending", { name });
      return site.tls.enabled ? t("siteDomains.www.added.http", { name }) : t("siteDomains.www.added.done", { name });
    });

  const noPair = www.pairs.length === 0;
  const scheme = site.tls.active ? "https" : "http";
  const choices: { mode: WWWMode; label: string; hint: string }[] = [
    { mode: "off", label: t("siteDomains.www.off"), hint: t("siteDomains.www.offHint") },
    { mode: "to-non-www", label: t("siteDomains.www.toNonWWW"), hint: `${ex.www} → ${ex.bare}` },
    { mode: "to-www", label: t("siteDomains.www.toWWW"), hint: `${ex.bare} → ${ex.www}` },
  ];

  return (
    <Card title={t("siteDomains.www.title")}>
      <div className="ui-stack">
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>{t("siteDomains.www.legend")}</legend>
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
              {t("siteDomains.www.needsPair")}
              {www.missing.length > 0 && " " + t("siteDomains.www.addMissing")}
            </p>
            {www.missing.length > 0 && (
              <div className="ui-row">
                {www.missing.slice(0, 3).map((m) => (
                  <Button key={m} disabled={busy} onClick={() => void add(m)}>
                    <Trans i18nKey="siteDomains.www.add" values={{ name: m }} components={{ mono: <span className="ui-mono" /> }} />
                  </Button>
                ))}
              </div>
            )}
            {www.mode !== "off" && <Notice tone="warn" title={t("siteDomains.www.noEffect")} />}
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
