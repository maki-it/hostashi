// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { formatBytes } from "./limits";
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
import { AppliesAtOnce, Badge, Button, Card, Field, Notice, Switch } from "./ui";

type Props = {
  site: Site;
  /** Admins and managers may change the size (ADR-0045). */
  canSize: boolean;
};

// ObjectCacheSettings turns a site's object cache on or off, flushes it,
// and shows how apps connect to it.
export function ObjectCacheSettings({ site, canSize }: Props) {
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
      return on ? "The object cache runs now. Point the site's app at the socket below." : "The object cache is off.";
    });

  const flush = () =>
    act(async () => {
      await flushObjectCache(site.name);
      return "The object cache is empty now.";
    });

  function saveSize(e: FormEvent) {
    e.preventDefault();
    void act(async () => {
      await setObjectCacheSize(site.name, size);
      return c?.enabled ? `The cache has ${size} MiB now. Resizing emptied it.` : `The cache gets ${size} MiB when it is turned on.`;
    });
  }

  if (loadError) {
    return (
      <Card title="Object cache">
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!c) {
    return (
      <Card title="Object cache">
        <p aria-busy="true">Loading…</p>
      </Card>
    );
  }

  const state = cacheState(c);

  return (
    <Card title="Object cache" actions={<AppliesAtOnce />}>
      <div className="ui-stack">
        <p className="ui-row">
          <Badge tone={state.tone}>{state.word}</Badge>
          {c.enabled && c.status && c.status.usedBytes > 0 && (
            <span className="ui-hint ui-flush">
              {formatBytes(c.status.usedBytes)} used of {c.memoryMb} MiB
            </span>
          )}
        </p>
        <Switch
          checked={c.enabled}
          disabled={busy || (site.disabled && !c.enabled)}
          onChange={(on) => void toggle(on)}
          hint="Only this site's PHP, cron jobs and SSH logins reach it, over a socket. It keeps nothing on disk: a restart, a flush or a new size empties it. Its memory counts against the site's memory limit."
        >
          Run an object cache (Valkey, compatible with Redis) for this site
        </Switch>
        {site.disabled && !c.enabled && <Notice tone="warn" title="Enable the site first to turn the cache on." />}
        {c.status?.paused && <Notice tone="warn" title="The site is disabled, so its cache is stopped. It starts again with the site." />}
        {canSize && (
          <form className="ui-row ui-row-field" onSubmit={saveSize} noValidate>
            <Field label="Size">
              <select className="ui-select" value={size} onChange={(e) => setSize(Number(e.target.value))}>
                {sizeChoices(c).map((n) => (
                  <option key={n} value={n}>
                    {n} MiB
                  </option>
                ))}
              </select>
            </Field>
            <Button type="submit" disabled={busy || size === c.memoryMb}>
              Save size
            </Button>
          </form>
        )}
        {!canSize && <p className="ui-hint">Size: {c.memoryMb} MiB. An administrator or manager can change it.</p>}
        {c.enabled && (
          <>
            <Field label="Socket" hint="Apps connect here instead of 127.0.0.1:6379. No password.">
              <input className="ui-input ui-mono" readOnly value={c.socket} onFocus={(e) => e.target.select()} />
            </Field>
            <Field label="For WordPress (wp-config.php)" hint="With the Redis Object Cache plugin. Hostashi does not install plugins.">
              <textarea className="ui-input ui-textarea ui-mono" readOnly rows={2} value={wpConfig(c.socket)} onFocus={(e) => e.target.select()} />
            </Field>
            <div className="ui-row">
              <Button disabled={busy || !!c.status?.paused} onClick={() => void flush()}>
                Flush cache
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
