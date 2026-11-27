// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
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
  const { t } = useTranslation();
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
      return c.enabled ? t("siteApp.pageCache.on") : t("siteApp.pageCache.off");
    });
  }

  const purge = () =>
    run(async () => {
      await purgePageCache(site.name);
      return t("siteApp.pageCache.emptied");
    });

  if (!cache) {
    return (
      <Card title={t("siteApp.pageCache.title")}>
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{t("common.status.loading")}</p>}
      </Card>
    );
  }

  const stopped = site.disabled || site.maintenance.enabled;

  return (
    <Card title={t("siteApp.pageCache.title")}>
      <form className="ui-stack" onSubmit={save} noValidate>
        <p className="ui-flush">{t("siteApp.pageCache.intro")}</p>
        <label className="ui-check">
          <input type="checkbox" checked={enabled} disabled={busy} onChange={(e) => setEnabled(e.target.checked)} />
          {t("siteApp.pageCache.enable")}
        </label>
        {enabled && !cache.enabled && (
          <Notice tone="warn" title={t("siteApp.pageCache.warnTitle")}>
            {t("siteApp.pageCache.warnBody")}
          </Notice>
        )}
        <Field label={t("siteApp.pageCache.application")} hint={presetHint[preset]}>
          <select className="ui-select" value={preset} disabled={busy} onChange={(e) => setPreset(e.target.value as Preset)}>
            {cache.choices.presets.map((p) => (
              <option key={p} value={p}>
                {presetLabel[p]}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("siteApp.pageCache.cacheTime")} hint={t("siteApp.pageCache.cacheTimeHint")}>
          <select className="ui-select" value={minutes} disabled={busy} onChange={(e) => setMinutes(Number(e.target.value))}>
            {cache.choices.minutes.map((m) => (
              <option key={m} value={m}>
                {t("siteApp.pageCache.minutes", { count: m })}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("siteApp.pageCache.excluded")} hint={t("siteApp.pageCache.excludedHint")}>
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
          <Field label={t("siteApp.pageCache.size")} hint={t("siteApp.pageCache.sizeHint")}>
            <select className="ui-select" value={size} disabled={busy} onChange={(e) => setSize(Number(e.target.value))}>
              {cache.choices.sizesMB.map((s) => (
                <option key={s} value={s}>
                  {formatSize(s)}
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <p className="ui-hint">{t("siteApp.pageCache.sizeInfo", { size: formatSize(cache.sizeMB) })}</p>
        )}
        {cache.enabled && stopped && <p className="ui-hint">{t("siteApp.pageCache.stopped")}</p>}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {t("siteApp.pageCache.save")}
          </Button>
          {cache.enabled && (
            <Button disabled={busy} onClick={() => void purge()}>
              {t("siteApp.pageCache.empty")}
            </Button>
          )}
        </div>
        {cache.enabled && (
          <p className="ui-hint">
            <Trans i18nKey="siteApp.pageCache.headerHint" values={{ site: site.name }} components={{ mono: <span className="ui-mono" /> }} />
          </p>
        )}
      </form>
    </Card>
  );
}
