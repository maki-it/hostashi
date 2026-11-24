// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { listScopes, type Scope } from "./device";
import { listSites, type Site } from "./sites";
import { Badge, Button, Field, Notice } from "./ui";

// Grant is what a new API token may do: its scopes, its sites and how long
// it lives. The device login and the token form on the account page share it.
export type Grant = {
  scopes: Set<string>;
  allSites: boolean;
  sites: Set<string>;
  days: string;
  unlimited: boolean;
};

/** grantReady reports whether a grant can be sent. */
export function grantReady(g: Grant): boolean {
  const d = Number(g.days);
  return g.scopes.size > 0 && (g.allSites || g.sites.size > 0) && (g.unlimited || (Number.isInteger(d) && d >= 1 && d <= 365));
}

function toggle(set: Set<string>, name: string, on: boolean): Set<string> {
  const next = new Set(set);
  if (on) next.add(name);
  else next.delete(name);
  return next;
}

/**
 * useGrant loads the scopes the account's role can use and its sites. Every
 * scope but the high-risk ones starts ticked; those must be chosen on
 * purpose.
 */
export function useGrant(days = "90") {
  const [scopes, setScopes] = useState<Scope[] | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [grant, setGrant] = useState<Grant>({ scopes: new Set(), allSites: true, sites: new Set(), days, unlimited: false });

  useEffect(() => {
    listScopes()
      .then((s) => {
        setScopes(s);
        setGrant((g) => ({ ...g, scopes: new Set(s.filter((x) => !x.highRisk).map((x) => x.name)) }));
      })
      .catch((err: unknown) => setError(err));
    listSites()
      .then(setSites)
      .catch(() => setSites([]));
  }, []);

  return { scopes, sites, error, grant, setGrant };
}

export function GrantFields({
  scopes,
  sites,
  grant,
  onChange,
  allowUnlimited = false,
}: {
  scopes: Scope[] | null;
  sites: Site[];
  grant: Grant;
  onChange: (g: Grant) => void;
  /** Offers a token that never expires (only on the account page). */
  allowUnlimited?: boolean;
}) {
  const pick = (keep: (s: Scope) => boolean) => onChange({ ...grant, scopes: new Set((scopes ?? []).filter(keep).map((s) => s.name)) });

  return (
    <>
      <fieldset className="ui-fieldset ui-stack-tight">
        <legend>What the token may do</legend>
        {scopes === null && <p aria-busy="true">Loading…</p>}
        {scopes !== null && scopes.length > 0 && (
          <div className="ui-row">
            <Button onClick={() => pick((s) => s.name.endsWith(":read"))}>Only reading</Button>
            <Button onClick={() => pick((s) => !s.highRisk)}>All but high risk</Button>
            <Button onClick={() => pick(() => false)}>None</Button>
          </div>
        )}
        {scopes?.map((s) => (
          <label key={s.name} className="ui-check">
            <input
              type="checkbox"
              checked={grant.scopes.has(s.name)}
              onChange={(e) => onChange({ ...grant, scopes: toggle(grant.scopes, s.name, e.target.checked) })}
            />
            <span>
              <code>{s.name}</code> {s.description} {s.highRisk && <Badge tone="warn">high risk</Badge>}
            </span>
          </label>
        ))}
      </fieldset>
      <fieldset className="ui-fieldset ui-stack-tight">
        <legend>Sites</legend>
        <label className="ui-check">
          <input type="checkbox" checked={grant.allSites} onChange={(e) => onChange({ ...grant, allSites: e.target.checked })} />
          Every site I can reach, also later ones
        </label>
        {!grant.allSites && sites.length === 0 && <p className="ui-hint ui-flush">You cannot reach any site yet.</p>}
        {!grant.allSites &&
          sites.map((s) => (
            <label key={s.name} className="ui-check">
              <input
                type="checkbox"
                checked={grant.sites.has(s.name)}
                onChange={(e) => onChange({ ...grant, sites: toggle(grant.sites, s.name, e.target.checked) })}
              />
              {s.name}
            </label>
          ))}
      </fieldset>
      <div className="ui-narrow">
        <Field label="Valid for (days)" hint="1 to 365. The token stops working after that.">
          <input
            className="ui-input"
            type="number"
            min={1}
            max={365}
            value={grant.days}
            disabled={grant.unlimited}
            onChange={(e) => onChange({ ...grant, days: e.target.value })}
          />
        </Field>
      </div>
      {allowUnlimited && (
        <label className="ui-check">
          <input type="checkbox" checked={grant.unlimited} onChange={(e) => onChange({ ...grant, unlimited: e.target.checked })} />
          <span>
            Never expires
            <span className="ui-hint"> Only for a monitoring or deploy job you look after.</span>
          </span>
        </label>
      )}
      {grant.unlimited && (
        <Notice tone="warn" title="This token works until you revoke it">
          If it leaks, it stays usable for as long as nobody notices. Prefer a lifetime and renew it.
        </Notice>
      )}
    </>
  );
}
