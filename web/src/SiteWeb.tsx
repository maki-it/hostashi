// SPDX-License-Identifier: AGPL-3.0-or-later

// The Web tab of a site (ADR-0054): its type and its web root folder, and
// for static sites the fallback for single-page apps and asset caching.

import { useState, type FormEvent } from "react";
import { formatHash } from "./routes";
import { PHPOptions, SiteTypeChoice } from "./Sites";
import { setPHP, setWebOptions, siteMessage, siteType, webDirProblem, type PHPVersions, type Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

type Props = { site: Site; php: PHPVersions | null; onChange: () => Promise<void> };

export function SiteWebSettings({ site, php, onChange }: Props) {
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
          More types (Node.js, own app, reverse proxy, redirect) follow. <a href={formatHash({ page: "php" })}>Install PHP versions</a>
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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const unchanged = dir === web.dir && spa === web.spa && assets === web.assetCache;

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = webDirProblem(dir);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      await setWebOptions(site.name, { dir, spa: isStatic && spa, assetCache: isStatic && assets });
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
        {isStatic ? (
          <fieldset className="ui-fieldset ui-stack-tight">
            <legend>Static site</legend>
            <label className="ui-check ui-check-hint">
              <input type="checkbox" checked={spa} disabled={busy} onChange={(e) => setSpa(e.target.checked)} />
              <span>
                Single-page app
                <span className="ui-hint ui-flush">Addresses without a file get index.html instead of 404, for apps with their own routing (React, Vue, Svelte).</span>
              </span>
            </label>
            <label className="ui-check ui-check-hint">
              <input type="checkbox" checked={assets} disabled={busy} onChange={(e) => setAssets(e.target.checked)} />
              <span>
                Cache assets for 30 days
                <span className="ui-hint ui-flush">
                  Browsers keep stylesheets, scripts, fonts and images for 30 days; HTML is always fresh. Turn it on only if file names change with
                  their content, as with most build tools.
                </span>
              </span>
            </label>
          </fieldset>
        ) : (
          <p className="ui-hint ui-flush">For Laravel and similar frameworks, enter public.</p>
        )}
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
