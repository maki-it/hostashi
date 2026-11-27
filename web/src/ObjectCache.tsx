// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { formatBytes } from "./format";
import {
  cacheMessage,
  cacheState,
  flushObjectCache,
  getObjectCache,
  setObjectCache,
  setObjectCacheSize,
  sizeChoices,
  wpConfig,
  type ObjectCache,
} from "./objcache";
import type { Site } from "./sites";
import { AppliesAtOnce, Badge, Button, Card, CopyButton, Field, Notice, Switch } from "./ui";

type Props = {
  site: Site;
  /** Admins and managers may change the size (ADR-0045). */
  canSize: boolean;
};

// ObjectCacheSettings turns a site's object cache on or off, flushes it,
// and shows how apps connect to it.
export function ObjectCacheSettings({ site, canSize }: Props) {
  const { t } = useTranslation();
  const [c, setC] = useState<ObjectCache | null>(null);
  const [size, setSize] = useState(0);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      const v = await getObjectCache(site.name);
      setC(v);
      setSize(v.memoryMb);
      setLoadError("");
    } catch (err) {
      setLoadError(cacheMessage(err));
    }
  }, [site.name]);

  useEffect(() => {
    void load();
  }, [load]);

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      setDone(await f());
    } catch (err) {
      setError(cacheMessage(err));
    } finally {
      await load();
      setBusy(false);
    }
  }

  const toggle = (on: boolean) =>
    act(async () => {
      await setObjectCache(site.name, on);
      return on ? t("siteApp.objectCache.on") : t("siteApp.objectCache.off");
    });

  const flush = () =>
    act(async () => {
      await flushObjectCache(site.name);
      return t("siteApp.objectCache.flushed");
    });

  function saveSize(e: FormEvent) {
    e.preventDefault();
    void act(async () => {
      await setObjectCacheSize(site.name, size);
      return c?.enabled ? t("siteApp.objectCache.resized", { n: size }) : t("siteApp.objectCache.sizeLater", { n: size });
    });
  }

  if (loadError) {
    return (
      <Card title={t("siteApp.objectCache.title")}>
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!c) {
    return (
      <Card title={t("siteApp.objectCache.title")}>
        <p aria-busy="true">{t("common.status.loading")}</p>
      </Card>
    );
  }

  const state = cacheState(c);

  return (
    <Card title={t("siteApp.objectCache.title")} actions={<AppliesAtOnce />}>
      <div className="ui-stack">
        <p className="ui-row">
          <Badge tone={state.tone}>{state.word}</Badge>
          {c.enabled && c.status && c.status.usedBytes > 0 && (
            <span className="ui-hint ui-flush">{t("siteApp.objectCache.used", { used: formatBytes(c.status.usedBytes), total: c.memoryMb })}</span>
          )}
        </p>
        <Switch
          checked={c.enabled}
          disabled={busy || (site.disabled && !c.enabled)}
          onChange={(on) => void toggle(on)}
          hint={t("siteApp.objectCache.switchHint")}
        >
          {t("siteApp.objectCache.switch")}
        </Switch>
        {site.disabled && !c.enabled && <Notice tone="warn" title={t("siteApp.objectCache.enableSiteFirst")} />}
        {c.status?.paused && <Notice tone="warn" title={t("siteApp.objectCache.paused")} />}
        {canSize && (
          <form className="ui-row ui-row-field" onSubmit={saveSize} noValidate>
            <Field label={t("siteApp.objectCache.size")}>
              <select className="ui-select" value={size} onChange={(e) => setSize(Number(e.target.value))}>
                {sizeChoices(c).map((n) => (
                  <option key={n} value={n}>
                    {t("siteApp.objectCache.mib", { n })}
                  </option>
                ))}
              </select>
            </Field>
            <Button type="submit" disabled={busy || size === c.memoryMb}>
              {t("siteApp.objectCache.saveSize")}
            </Button>
          </form>
        )}
        {!canSize && <p className="ui-hint">{t("siteApp.objectCache.sizeInfo", { n: c.memoryMb })}</p>}
        {c.enabled && (
          <>
            <Field label={t("siteApp.objectCache.socket")} hint={t("siteApp.objectCache.socketHint")}>
              <input className="ui-input ui-mono" readOnly value={c.socket} onFocus={(e) => e.target.select()} />
            </Field>
            <div className="ui-row">
              <CopyButton text={c.socket} label={t("siteApp.objectCache.copySocket")} />
              <CopyButton text={wpConfig(c.socket)} label={t("siteApp.objectCache.copyWP")} />
            </div>
            <Field label={t("siteApp.objectCache.wpLabel")} hint={t("siteApp.objectCache.wpHint")}>
              <textarea className="ui-input ui-textarea ui-mono" readOnly rows={2} value={wpConfig(c.socket)} onFocus={(e) => e.target.select()} />
            </Field>
            <div className="ui-row">
              <Button disabled={busy || !!c.status?.paused} onClick={() => void flush()}>
                {t("siteApp.objectCache.flush")}
              </Button>
            </div>
          </>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </div>
    </Card>
  );
}
