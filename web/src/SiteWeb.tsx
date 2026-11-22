// SPDX-License-Identifier: AGPL-3.0-or-later

// The Web tab of a site (ADR-0054): its type and its web root folder, and
// for static sites the fallback for single-page apps and asset caching.

import { useState, type FormEvent } from "react";
import { formatHash } from "./routes";
import { AppCard, AppSettingsCard } from "./SiteNode";
import { ProxyCard } from "./SiteProxy";
import { RedirectCard } from "./SiteRedirect";
import { PHPOptions, SiteTypeChoice } from "./Sites";
import {
  assetCacheDefaults,
  assetCacheProblem,
  assetCacheUnits,
  parseExts,
  setPHP,
  setWebOptions,
  siteMessage,
  siteType,
  splitSeconds,
  webDirProblem,
  type PHPVersions,
  type Site,
} from "./sites";
import { Button, Card, Field, Notice } from "./ui";

type Props = { site: Site; php: PHPVersions | null; onChange: () => Promise<void>; admin?: boolean };

export function SiteWebSettings({ site, php, onChange, admin = false }: Props) {
  if (siteType(site) === "proxy") {
    return (
      <>
        <ProxyCard site={site} onChange={onChange} />
        <p className="ui-hint ui-flush">
          A reverse proxy site has no files and no Linux account. Changing it into a PHP or static site comes with the type switch in a later version.
        </p>
      </>
    );
  }

  if (siteType(site) === "node") {
    return (
      <>
        <AppCard site={site} onChange={onChange} />
        <AppSettingsCard site={site} onChange={onChange} admin={admin} />
        <p className="ui-hint ui-flush">Changing a Node.js site into a PHP or static site comes with the type switch in a later version.</p>
      </>
    );
  }

  if (siteType(site) === "redirect") {
    return (
      <>
        <RedirectCard site={site} onChange={onChange} />
        <p className="ui-hint ui-flush">
          A redirect site has no files and no Linux account. Changing it into a PHP or static site comes with the type switch in a later version.
        </p>
      </>
    );
  }

  return (
    <>
      <TypeCard site={site} php={php} onChange={onChange} />
      <WebRootCard site={site} onChange={onChange} />
    </>
  );
}

function TypeCard({ site, php, onChange }: Props) {
  const current = siteType(site) === "php" ? "php" : "static";
  const [kind, setKind] = useState<"static" | "php">(current);
  const newest = [...(php?.installed ?? [])].reverse().find((v) => !php?.legacy.includes(v)) ?? "";
  const [version, setVersion] = useState(site.php || newest);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const target = kind === "php" ? version : "";
  const unchanged = target === site.php;

  async function save(e: FormEvent) {
    e.preventDefault();
    if (kind === "php" && !version) {
      setError("No PHP version is installed. Install one under PHP first.");
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      await setPHP(site.name, target);
      await onChange();
      setDone(target ? `${site.name} is a PHP site with PHP ${target} now.` : `${site.name} is a static site now. Its PHP pool is stopped.`);
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Site type">
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <SiteTypeChoice value={kind} onChange={setKind} name={`${site.name}-type`} disabled={busy} />
        {kind === "php" && (
          <Field label="PHP version">
            <select className="ui-select" value={version} onChange={(e) => setVersion(e.target.value)}>
              <PHPOptions php={php} current={site.php} withoutNone />
            </select>
          </Field>
        )}
        {kind !== current && (
          <p className="ui-hint ui-flush">
            {kind === "static"
              ? "Files, databases, cron jobs, SSH logins and backups stay. Only the PHP pool is stopped; .php files are no longer run or sent. Your PHP settings are kept for a switch back."
              : "Files, databases, cron jobs, SSH logins and backups stay. The site gets its own PHP pool."}
          </p>
        )}
        <p className="ui-hint ui-flush">
          More types (Node.js, own app) follow; reverse proxy and redirect are chosen when a site is created. <a href={formatHash({ page: "php" })}>Install PHP versions</a>
        </p>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            Save type
          </Button>
        </div>
      </form>
    </Card>
  );
}

function WebRootCard({ site, onChange }: Omit<Props, "php">) {
  const web = site.web ?? { dir: "", spa: false, assetCache: false };
  const isStatic = siteType(site) === "static";
  const [dir, setDir] = useState(web.dir);
  const [spa, setSpa] = useState(web.spa);
  const [assets, setAssets] = useState(web.assetCache);
  const initial = splitSeconds(web.assetCacheSeconds ?? assetCacheDefaults.seconds);
  const [amount, setAmount] = useState(String(initial.amount));
  const [unit, setUnit] = useState(initial.unit);
  const savedExts = (web.assetCacheExts ?? assetCacheDefaults.exts).join(" ");
  const [exts, setExts] = useState(savedExts);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const seconds = Number(amount) * unit;
  const unchanged =
    dir === web.dir &&
    spa === web.spa &&
    assets === web.assetCache &&
    seconds === (web.assetCacheSeconds ?? assetCacheDefaults.seconds) &&
    parseExts(exts).join(" ") === savedExts;

  async function save(e: FormEvent) {
    e.preventDefault();
    const list = parseExts(exts);
    const problem = webDirProblem(dir) || (assets ? assetCacheProblem(seconds, list) : "");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      const cache = assets;
      await setWebOptions(site.name, {
        dir,
        spa: isStatic && spa,
        assetCache: cache,
        // Kept as they are while the cache is off, so turning it on again restores them.
        ...(cache ? { assetCacheSeconds: seconds, assetCacheExts: list } : pick(web)),
      });
      await onChange();
      setDone(dir ? `The site serves htdocs/${dir} now.` : "The site serves htdocs now.");
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Web root">
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <Field
          label="Folder"
          hint="The folder the site serves, below htdocs. Leave it empty for htdocs itself, or enter for example dist, public or build. A folder that does not exist yet answers 404 until it is there."
        >
          <div className="ui-input-prefix">
            <span className="ui-mono" aria-hidden="true">
              htdocs/
            </span>
            <input
              className="ui-input ui-mono"
              value={dir}
              onChange={(e) => setDir(e.target.value.trim())}
              placeholder="dist"
              aria-label="Folder below htdocs"
              autoComplete="off"
              spellCheck={false}
            />
          </div>
        </Field>
        {!isStatic && <p className="ui-hint ui-flush">For Laravel and similar frameworks, enter public.</p>}
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>Options</legend>
          {isStatic && (
            <label className="ui-check ui-check-hint">
              <input type="checkbox" checked={spa} disabled={busy} onChange={(e) => setSpa(e.target.checked)} />
              <span>
                Single-page app
                <span className="ui-hint ui-flush">Addresses without a file get index.html instead of 404, for apps with their own routing (React, Vue, Svelte).</span>
              </span>
            </label>
          )}
          <label className="ui-check ui-check-hint">
            <input type="checkbox" checked={assets} disabled={busy} onChange={(e) => setAssets(e.target.checked)} />
            <span>
              Cache assets in the browser
              <span className="ui-hint ui-flush">
                Browsers keep the listed file types for the chosen time without asking again. Turn it on only if file names or addresses change with
                their content, as with most build tools and WordPress; otherwise visitors see old files after a deploy.
              </span>
            </span>
          </label>
          {assets && (
            <div className="ui-stack ui-indent">
              <Field label="Cache time" hint="Between 1 minute and 365 days. Default: 30 days.">
                <div className="ui-row ui-row-tight">
                  <input
                    className="ui-input ui-input-num"
                    type="number"
                    min={1}
                    value={amount}
                    onChange={(e) => setAmount(e.target.value)}
                    aria-label="Cache time"
                  />
                  <select className="ui-select" value={unit} onChange={(e) => setUnit(Number(e.target.value))} aria-label="Unit">
                    {assetCacheUnits.map((u) => (
                      <option key={u.value} value={u.value}>
                        {u.label}
                      </option>
                    ))}
                  </select>
                </div>
              </Field>
              <Field label="File types" hint="Separated by spaces or commas, without the dot. HTML stays fresh unless you add html.">
                <textarea
                  className="ui-input ui-textarea ui-mono"
                  value={exts}
                  onChange={(e) => setExts(e.target.value)}
                  rows={2}
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <div className="ui-row">
                <Button
                  disabled={busy}
                  onClick={() => {
                    const d = splitSeconds(assetCacheDefaults.seconds);
                    setAmount(String(d.amount));
                    setUnit(d.unit);
                    setExts(assetCacheDefaults.exts.join(" "));
                  }}
                >
                  Use the defaults
                </Button>
              </div>
            </div>
          )}
        </fieldset>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            Save web root
          </Button>
        </div>
      </form>
    </Card>
  );
}

// pick keeps the stored cache values of a site when the cache is off.
function pick(web: { assetCacheSeconds?: number; assetCacheExts?: string[] }) {
  return {
    ...(web.assetCacheSeconds !== undefined && { assetCacheSeconds: web.assetCacheSeconds }),
    ...(web.assetCacheExts !== undefined && { assetCacheExts: web.assetCacheExts }),
  };
}
