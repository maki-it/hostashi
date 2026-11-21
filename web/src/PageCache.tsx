// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import {
  excludeProblem,
  formatSize,
  getPageCache,
  pageCacheMessage,
  parseExclude,
  presetHint,
  presetLabel,
  purgePageCache,
  setPageCache,
  setPageCacheSize,
  type PageCache,
  type Preset,
} from "./pagecache";
import type { Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

type Props = {
  site: Site;
  /** Managers and admins may set the size (ADR-0046); others see it. */
  canSize: boolean;
};

// PageCacheSettings is the page cache card of a site with PHP.
export function PageCacheSettings({ site, canSize }: Props) {
  const [cache, setCache] = useState<PageCache | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [preset, setPreset] = useState<Preset>("wordpress");
  const [minutes, setMinutes] = useState(10);
  const [exclude, setExclude] = useState("");
  const [size, setSize] = useState(256);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  function show(c: PageCache) {
    setCache(c);
    setEnabled(c.enabled);
    setPreset(c.preset);
    setMinutes(c.minutes);
    setExclude(c.exclude.join("\n"));
    setSize(c.sizeMB);
  }

  useEffect(() => {
    let live = true;
    getPageCache(site.name).then(
      (c) => live && show(c),
      (err: unknown) => live && setError(pageCacheMessage(err)),
    );
    return () => {
      live = false;
    };
  }, [site.name]);

  async function run(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      setDone(await f());
    } catch (err) {
      setError(pageCacheMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function save(e: FormEvent) {
    e.preventDefault();
    if (!cache) return;
    const paths = parseExclude(exclude);
    const problem = excludeProblem(paths, cache.choices.maxExclude);
    if (problem) {
      setError(problem);
      setDone("");
      return;
    }
    void run(async () => {
      let c = await setPageCache(site.name, { enabled, preset, minutes, exclude: paths });
      if (canSize && size !== c.sizeMB) c = await setPageCacheSize(site.name, size);
      show(c);
      return c.enabled ? "The page cache is on." : "The page cache is off.";
    });
  }

  const purge = () =>
    run(async () => {
      await purgePageCache(site.name);
      return "The page cache is empty. The next visits fill it again.";
    });

  if (!cache) {
    return (
      <Card title="Page cache">
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading…</p>}
      </Card>
    );
  }

  const stopped = site.disabled || site.maintenance.enabled;

  return (
    <Card title="Page cache">
      <form className="ui-stack" onSubmit={save} noValidate>
        <p className="ui-flush">
          nginx keeps the pages that PHP generates for a few minutes and serves them to the next visitors without starting PHP. Changes show
          up after the cache time at the latest, or right after you empty the cache.
        </p>
        <label className="ui-check">
          <input type="checkbox" checked={enabled} disabled={busy} onChange={(e) => setEnabled(e.target.checked)} />
          Cache pages of this site
        </label>
        {enabled && !cache.enabled && (
          <Notice tone="warn" title="Check that no page shows personal data without a login">
            The cache cannot tell what a page contains. If your application shows personal data without a cookie or a “Cache-Control:
            private” header, for example through a token in the address, another visitor could get that page. Exclude such paths below.
          </Notice>
        )}
        <Field label="Application" hint={presetHint[preset]}>
          <select className="ui-select" value={preset} disabled={busy} onChange={(e) => setPreset(e.target.value as Preset)}>
            {cache.choices.presets.map((p) => (
              <option key={p} value={p}>
                {presetLabel[p]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Cache time" hint="How long a page is kept. Pages with errors other than 404 are never kept.">
          <select className="ui-select" value={minutes} disabled={busy} onChange={(e) => setMinutes(Number(e.target.value))}>
            {cache.choices.minutes.map((m) => (
              <option key={m} value={m}>
                {m === 1 ? "1 minute" : `${m} minutes`}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Excluded paths" hint="One per line. /path is only that page, /path* every address that starts with it, for example /warenkorb*.">
          <textarea
            className="ui-input ui-textarea ui-mono"
            value={exclude}
            disabled={busy}
            onChange={(e) => setExclude(e.target.value)}
            rows={Math.min(10, Math.max(3, exclude.split("\n").length + 1))}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
        {canSize ? (
          <Field label="Size" hint="Cached pages count against the site's disk space.">
            <select className="ui-select" value={size} disabled={busy} onChange={(e) => setSize(Number(e.target.value))}>
              {cache.choices.sizesMB.map((s) => (
                <option key={s} value={s}>
                  {formatSize(s)}
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <p className="ui-hint">
            Size: at most {formatSize(cache.sizeMB)}, counted against the site's disk space. Managers and administrators can change it.
          </p>
        )}
        {cache.enabled && stopped && <p className="ui-hint">Not used while the site is disabled or in maintenance. It starts empty afterwards.</p>}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            Save page cache
          </Button>
          {cache.enabled && (
            <Button disabled={busy} onClick={() => void purge()}>
              Empty cache
            </Button>
          )}
        </div>
        {cache.enabled && (
          <p className="ui-hint">
            The answer header <span className="ui-mono">X-Cache</span> shows HIT, MISS or BYPASS. A CMS can empty the cache on save with an API
            token that has only the scope <span className="ui-mono">pagecache:purge</span> for this site:{" "}
            <span className="ui-mono">POST /api/v1/sites/{site.name}/page-cache/purge</span>.
          </p>
        )}
      </form>
    </Card>
  );
}
