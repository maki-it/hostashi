// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  fromRealIPForm,
  getPanelRealIP,
  getRealIP,
  realIPMessage,
  setPanelRealIP,
  setRealIP,
  splitRanges,
  toRealIPForm,
  type RealIPForm,
  type RealIPView,
  type WebRealIP,
} from "./realip";
import type { Site } from "./sites";
import { Button, Card, Field, Notice, UnsavedNote } from "./ui";

const title = "Real visitor address";

// RealIPCard sets which proxies a site takes the visitor's address from
// (ADR-0059). Everyone with access to the site may switch Cloudflare and
// the header; only admins add own addresses, because they also go into
// fail2ban's never-ban list for the whole server.
export function RealIPCard({ site, admin }: { site: Site; admin: boolean }) {
  const name = site.name;
  const load = useCallback(() => getRealIP(name), [name]);
  const save = useCallback((r: WebRealIP) => setRealIP(name, r), [name]);
  return <RealIPEditor load={load} save={save} admin={admin} text={siteText} />;
}

// PanelRealIPCard is the same setting for the panel's own host name, for
// admins only.
export function PanelRealIPCard() {
  return (
    <RealIPEditor
      load={getPanelRealIP}
      save={setPanelRealIP}
      admin
      text={panelText}
    />
  );
}

type Text = {
  title: string;
  intro: string;
  behind: string;
  direct: string;
};

const siteText: Text = {
  title,
  intro:
    "Behind a CDN or load balancer, every request seems to come from the proxy. With this on, nginx takes the visitor's address from a header, but only for requests that really come from the proxies listed here. Logs, PHP, the app and the maintenance allowlist then see the visitor.",
  behind: "This site is behind a proxy or CDN",
  direct:
    "Visitors can still reach the server directly, past the proxy. Let ports 80 and 443 through only from the proxy if the site must not be reached otherwise.",
};

const panelText: Text = {
  title: "Real visitor address of the panel",
  intro:
    "Only for a panel address behind a CDN or load balancer. nginx then takes the visitor's address from a header, but only for requests from the proxies listed here. The audit log, the login limit and the panel allowlist then see the visitor instead of the proxy.",
  behind: "The panel is behind a proxy or CDN",
  direct:
    "Visitors can still reach the panel directly, past the proxy. The panel allowlist on the Firewall page also checks the visitor's address once this is on, so list the visitors there, not the proxy.",
};

function RealIPEditor({
  load: get,
  save: put,
  admin,
  text,
}: {
  load: () => Promise<RealIPView>;
  save: (r: WebRealIP) => Promise<RealIPView>;
  admin: boolean;
  text: Text;
}) {
  const [view, setView] = useState<RealIPView | null>(null);
  const [form, setForm] = useState<RealIPForm | null>(null);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      const v = await get();
      setView(v);
      setForm(toRealIPForm(v.realIp));
      setLoadError("");
    } catch (err) {
      setLoadError(realIPMessage(err));
    }
  }, [get]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loadError) {
    return (
      <Card title={text.title}>
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!view || !form) {
    return (
      <Card title={text.title}>
        <p aria-busy="true">Loading…</p>
      </Card>
    );
  }

  const set = (next: Partial<RealIPForm>) => setForm({ ...form, ...next });
  const dirty = JSON.stringify(form) !== JSON.stringify(toRealIPForm(view.realIp));
  const stored = view.realIp.ranges ?? [];
  // A user without the admin role may keep or remove the stored
  // addresses, never add one.
  const canEditRanges = admin;

  async function submit(e: FormEvent) {
    e.preventDefault();
    const { realIp, error: problem } = fromRealIPForm(form!);
    setError("");
    setDone("");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    try {
      const v = await put(realIp);
      setView(v);
      setForm(toRealIPForm(v.realIp));
      setDone(
        realIp.cloudflare || realIp.ranges
          ? "Saved. nginx takes the visitor's address from the proxy now."
          : "Saved. No proxy is trusted.",
      );
    } catch (err) {
      setError(realIPMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={text.title}>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <p className="ui-hint">{text.intro}</p>
        <label className="ui-check">
          <input
            type="checkbox"
            checked={form.enabled}
            disabled={busy}
            onChange={(e) => set({ enabled: e.target.checked })}
          />
          {text.behind}
        </label>
        {form.enabled && (
          <fieldset className="ui-fieldset ui-stack" disabled={busy}>
            <legend>Trusted proxies</legend>
            <label className="ui-check">
              <input
                type="checkbox"
                checked={form.cloudflare}
                onChange={(e) => set({ cloudflare: e.target.checked })}
              />
              Cloudflare ({view.cloudflare.length} ranges, as of{" "}
              {view.cloudflareDate})
            </label>
            {form.cloudflare && (
              <details>
                <summary className="ui-hint">Show Cloudflare's ranges</summary>
                <p className="ui-hint ui-mono">{view.cloudflare.join(" ")}</p>
              </details>
            )}
            <Field
              label="Own proxies"
              hint={
                canEditRanges
                  ? `Addresses or ranges of your load balancer or another CDN, one per line, at most ${view.maxRanges}. Every hop in front of this server must be listed.`
                  : stored.length > 0
                    ? "Only admins add addresses. You can remove them."
                    : "Only admins add addresses of other proxies."
              }
            >
              <textarea
                className="ui-input ui-textarea ui-mono"
                rows={3}
                value={form.ranges}
                spellCheck={false}
                placeholder={canEditRanges ? "10.0.0.2\n198.51.100.0/24" : ""}
                readOnly={!canEditRanges}
                onChange={(e) => set({ ranges: e.target.value })}
              />
            </Field>
            {!canEditRanges &&
              stored.length > 0 &&
              splitRanges(form.ranges).length > 0 && (
                <Button onClick={() => set({ ranges: "" })}>
                  Remove own addresses
                </Button>
              )}
            <Field
              label="Header"
              hint="X-Forwarded-For works with Cloudflare and most proxies. Choose another only if your proxy sends the address there."
            >
              <select
                className="ui-select"
                value={form.header}
                onChange={(e) => set({ header: e.target.value })}
              >
                {view.headers.map((h, i) => (
                  <option key={h} value={i === 0 ? "" : h}>
                    {i === 0 ? `${h} (recommended)` : h}
                  </option>
                ))}
              </select>
            </Field>
            <Notice tone="warn" title={text.direct} />
            <p className="ui-hint">
              fail2ban never bans the trusted proxies, so its bans do not stop
              visitors who come through them. Block those in the proxy, for
              example with Cloudflare's WAF rules.
            </p>
          </fieldset>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !dirty}>
            Save
          </Button>
          <UnsavedNote dirty={dirty} />
        </div>
      </form>
    </Card>
  );
}
