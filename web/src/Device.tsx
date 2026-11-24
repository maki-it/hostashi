// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import { approveDeviceLogin, deviceMessage, getDeviceLogin, normalizeCode, type DeviceLogin } from "./device";
import { GrantFields, grantReady, useGrant } from "./TokenGrant";
import { Button, Card, Field, Notice, PageHead, RelTime } from "./ui";

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
          Return to your terminal: the CLI has its token within a few seconds. You can see and revoke the token under Your account.
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
  const { scopes, sites, error: loadError, grant, setGrant } = useGrant();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (loadError) setError(deviceMessage(loadError));
  }, [loadError]);

  async function answer(approve: boolean) {
    setBusy(true);
    setError("");
    try {
      await approveDeviceLogin(
        login.userCode,
        approve,
        [...grant.scopes],
        grant.allSites ? null : [...grant.sites],
        Number(grant.days),
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
        <GrantFields scopes={loadError ? [] : scopes} sites={sites} grant={grant} onChange={setGrant} />
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !grantReady(grant)}>
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
