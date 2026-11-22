// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import { approveDeviceLogin, deviceMessage, getDeviceLogin, listScopes, normalizeCode, type DeviceLogin, type Scope } from "./device";
import { listSites, type Site } from "./sites";
import { Badge, Button, Card, Field, Notice, PageHead, RelTime } from "./ui";

// Device approves the code that "hostashi login" shows (ADR-0058 addendum 1).
// The CLI then gets an API token with the scopes, sites and lifetime chosen
// here.
export function Device({ code: initial }: { code: string }) {
  const [code, setCode] = useState(normalizeCode(initial));
  const [login, setLogin] = useState<DeviceLogin | null>(null);
  const [done, setDone] = useState<"approved" | "denied" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function lookup(c: string) {
    setBusy(true);
    setError("");
    try {
      setLogin(await getDeviceLogin(c));
    } catch (err) {
      setError(deviceMessage(err));
    } finally {
      setBusy(false);
    }
  }

  // A link from the CLI carries the code: look it up right away.
  useEffect(() => {
    if (initial !== "") void lookup(normalizeCode(initial));
  }, [initial]);

  function submit(e: FormEvent) {
    e.preventDefault();
    void lookup(normalizeCode(code));
  }

  return (
    <>
      <PageHead title="Log in a device" description="Approve the code that hostashi login shows in your terminal." />
      {done === "approved" && (
        <Notice tone="ok" title="Login approved">
          Return to your terminal: the CLI has its token within a few seconds. You can revoke the token under Account.
        </Notice>
      )}
      {done === "denied" && <Notice tone="info" title="Login denied. The CLI gets no token." />}
      {done === null && login === null && (
        <Card title="Code">
          <form className="ui-stack" onSubmit={submit}>
            <Field label="Code from the terminal" hint="Eight letters, for example WDJB-MJHT.">
              <input
                className="ui-input"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                autoComplete="off"
                autoCapitalize="characters"
                spellCheck={false}
                autoFocus
              />
            </Field>
            {error && <Notice tone="err" title={error} />}
            <div className="ui-row">
              <Button type="submit" variant="primary" disabled={busy || code.trim() === ""}>
                {busy ? "Looking up…" : "Continue"}
              </Button>
            </div>
          </form>
        </Card>
      )}
      {done === null && login !== null && <Approve login={login} onDone={setDone} />}
    </>
  );
}

function Approve({ login, onDone }: { login: DeviceLogin; onDone: (d: "approved" | "denied") => void }) {
  const [scopes, setScopes] = useState<Scope[] | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [allSites, setAllSites] = useState(true);
  const [pickedSites, setPickedSites] = useState<Set<string>>(new Set());
  const [days, setDays] = useState("90");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    listScopes()
      .then((s) => {
        setScopes(s);
        // Everything but the high-risk scopes is ticked; those must be
        // chosen on purpose.
        setPicked(new Set(s.filter((x) => !x.highRisk).map((x) => x.name)));
      })
      .catch((err: unknown) => setError(deviceMessage(err)));
    listSites()
      .then(setSites)
      .catch(() => setSites([]));
  }, []);

  const toggle = (set: Set<string>, name: string, on: boolean) => {
    const next = new Set(set);
    if (on) next.add(name);
    else next.delete(name);
    return next;
  };

  async function answer(approve: boolean) {
    setBusy(true);
    setError("");
    try {
      await approveDeviceLogin(
        login.userCode,
        approve,
        [...picked],
        allSites ? null : [...pickedSites],
        Number(days),
      );
      onDone(approve ? "approved" : "denied");
    } catch (err) {
      setError(deviceMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={"Login " + login.userCode}>
      <form
        className="ui-stack"
        onSubmit={(e) => {
          e.preventDefault();
          void answer(true);
        }}
      >
        <dl className="ui-facts">
          <dt>Device</dt>
          <dd>{login.client}</dd>
          <dt>Asked from</dt>
          <dd>{login.ip}</dd>
          <dt>Asked</dt>
          <dd>
            <RelTime iso={login.createdAt} />
          </dd>
        </dl>
        <Notice tone="warn" title="Approve only a login you started yourself">
          Whoever started it gets an API token for your account. If you did not just run hostashi login, deny it.
        </Notice>
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>What the token may do</legend>
          {scopes === null && !error && <p aria-busy="true">Loading…</p>}
          {scopes?.map((s) => (
            <label key={s.name} className="ui-check">
              <input type="checkbox" checked={picked.has(s.name)} onChange={(e) => setPicked(toggle(picked, s.name, e.target.checked))} />
              <span>
                <code>{s.name}</code> {s.description} {s.highRisk && <Badge tone="warn">high risk</Badge>}
              </span>
            </label>
          ))}
        </fieldset>
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>Sites</legend>
          <label className="ui-check">
            <input type="checkbox" checked={allSites} onChange={(e) => setAllSites(e.target.checked)} />
            Every site I can reach, also later ones
          </label>
          {!allSites &&
            sites.map((s) => (
              <label key={s.name} className="ui-check">
                <input
                  type="checkbox"
                  checked={pickedSites.has(s.name)}
                  onChange={(e) => setPickedSites(toggle(pickedSites, s.name, e.target.checked))}
                />
                {s.name}
              </label>
            ))}
        </fieldset>
        <div className="ui-narrow">
          <Field label="Valid for (days)" hint="1 to 365. The token stops working after that.">
            <input className="ui-input" type="number" min={1} max={365} value={days} onChange={(e) => setDays(e.target.value)} />
          </Field>
        </div>
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || picked.size === 0 || (!allSites && pickedSites.size === 0)}>
            Approve
          </Button>
          <Button type="button" disabled={busy} onClick={() => void answer(false)}>
            Deny
          </Button>
        </div>
      </form>
    </Card>
  );
}
