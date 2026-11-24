// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Trash } from "@phosphor-icons/react";
import { ApiError } from "./api";
import { addPasskey, changePassword, deletePasskey, listPasskeys, message, totpDisable, type Passkey, type User } from "./auth";
import { TotpEnrolment } from "./SecondFactor";
import { Button, Card, Field, IconButton, Notice, RelTime } from "./ui";
import { passkeysSupported } from "./webauthn";

// securityMessage explains the errors of the account security forms.
export function securityMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "invalid_credentials":
        return "Wrong password or code.";
      case "second_factor_required":
        return "Your account must keep a second factor. Add a passkey or keep the authenticator app.";
      case "too_many_passkeys":
        return "You have the most passkeys an account can have. Remove one first.";
    }
  }
  return message(err);
}

// SecurityCard lets the logged-in account change its password, set up or
// turn off the authenticator app (TOTP) and manage its passkeys. Every
// change needs this browser session; the server sends a security email.
export function SecurityCard({ user }: { user: User }) {
  return (
    <Card title="Sign-in and security">
      <div className="ui-stack">
        <PasswordSection />
        <TotpSection user={user} />
        <PasskeySection />
      </div>
    </Card>
  );
}

function PasswordSection() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [repeat, setRepeat] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setDone(false);
    if (next !== repeat) {
      setError("The new passwords do not match.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await changePassword(current, next);
      setDone(true);
      setCurrent("");
      setNext("");
      setRepeat("");
    } catch (err) {
      setError(err instanceof ApiError && err.code === "invalid_credentials" ? "The current password is wrong." : securityMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ui-stack" aria-labelledby="sec-password">
      <h3 id="sec-password" className="ui-flush">
        Password
      </h3>
      <form className="ui-stack ui-narrow" onSubmit={(e) => void submit(e)} noValidate>
        <Field label="Current password">
          <input className="ui-input" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </Field>
        <Field label="New password" hint="At least 12 characters. Your other sessions are logged out.">
          <input className="ui-input" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
        <Field label="Repeat the new password">
          <input className="ui-input" type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} />
        </Field>
        {error && <Notice tone="err" title={error} />}
        {done && !error && <Notice tone="ok" title="Password changed. Your other sessions were logged out." />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || current === "" || next === "" || repeat === ""}>
            {busy ? "Changing…" : "Change password"}
          </Button>
        </div>
      </form>
    </section>
  );
}

function TotpSection({ user }: { user: User }) {
  const [on, setOn] = useState(!!user.totp);
  const [mode, setMode] = useState<"" | "setup" | "off">("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");

  async function turnOff(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await totpDisable(password, code.trim());
      setOn(false);
      setMode("");
      setNote("The authenticator app is off. Its recovery codes no longer work.");
    } catch (err) {
      setError(securityMessage(err));
      setCode("");
    } finally {
      setBusy(false);
      setPassword("");
    }
  }

  return (
    <section className="ui-stack" aria-labelledby="sec-totp">
      <h3 id="sec-totp" className="ui-flush">
        Authenticator app (TOTP)
      </h3>
      {mode === "setup" ? (
        <>
          <TotpEnrolment
            intro=""
            autoStart
            onDone={() => {
              setOn(true);
              setMode("");
              setNote("The authenticator app is on.");
            }}
          />
          <div className="ui-row">
            <Button onClick={() => setMode("")}>Cancel</Button>
          </div>
        </>
      ) : mode === "off" ? (
        <form className="ui-stack ui-narrow" onSubmit={(e) => void turnOff(e)} noValidate>
          <p className="ui-flush">To turn it off, enter your password and a current code from the app or a recovery code.</p>
          <Field label="Your password">
            <input className="ui-input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Field label="Code from the app or a recovery code">
            <input className="ui-input" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} />
          </Field>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button type="submit" variant="danger" disabled={busy || password === "" || code.trim() === ""}>
              Turn off
            </Button>
            <Button disabled={busy} onClick={() => setMode("")}>
              Cancel
            </Button>
          </div>
        </form>
      ) : (
        <>
          <p className="ui-flush">
            {on ? "On. Logging in with your password asks for a code from the app." : "Off. Logging in with your password asks for nothing else."}
          </p>
          {on && (
            <p className="ui-flush ui-hint">
              To move to another app or phone, turn it off and set it up again. If your role needs a second factor, add a passkey first.
            </p>
          )}
          {note && <Notice tone="ok" title={note} />}
          <div className="ui-row">
            {on ? (
              <Button
                variant="danger"
                onClick={() => {
                  setError("");
                  setNote("");
                  setMode("off");
                }}
              >
                Turn off
              </Button>
            ) : (
              <Button
                variant="primary"
                onClick={() => {
                  setNote("");
                  setMode("setup");
                }}
              >
                Set up an authenticator app
              </Button>
            )}
          </div>
        </>
      )}
    </section>
  );
}

function PasskeySection() {
  const [keys, setKeys] = useState<Passkey[] | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const supported = passkeysSupported();

  const load = useCallback(async () => {
    try {
      setKeys(await listPasskeys());
    } catch (err) {
      setError(securityMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function add(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setNote("");
    try {
      const p = await addPasskey(name.trim());
      setNote(`Passkey ${p.name} added.`);
      setName("");
      await load();
    } catch (err) {
      setError(securityMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove(p: Passkey) {
    setBusy(true);
    setError("");
    setNote("");
    try {
      await deletePasskey(p.id);
      setNote(`Passkey ${p.name} removed.`);
      await load();
    } catch (err) {
      setError(securityMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ui-stack" aria-labelledby="sec-passkeys">
      <h3 id="sec-passkeys" className="ui-flush">
        Passkeys
      </h3>
      <p className="ui-flush ui-hint">A passkey logs you in without a password, with your device's lock or a security key. It counts as a second factor.</p>
      {keys === null ? (
        !error && <p aria-busy="true">Loading…</p>
      ) : keys.length === 0 ? (
        <p className="ui-flush">No passkeys yet.</p>
      ) : (
        <ul className="ui-plain-list" aria-label="Your passkeys">
          {keys.map((p) => (
            <li key={p.id} className="ui-list-row">
              <span>
                <strong>{p.name}</strong>{" "}
                <span className="ui-hint">
                  added <RelTime iso={p.createdAt} />
                  {p.lastUsedAt ? (
                    <>
                      , last used <RelTime iso={p.lastUsedAt} />
                    </>
                  ) : (
                    ", never used"
                  )}
                </span>
              </span>
              <IconButton label={`Remove passkey ${p.name}`} icon={<Trash size={16} aria-hidden="true" />} danger disabled={busy} onClick={() => void remove(p)} />
            </li>
          ))}
        </ul>
      )}
      {supported ? (
        <form className="ui-stack ui-narrow" onSubmit={(e) => void add(e)} noValidate>
          <Field label="Name of the new passkey" hint="So you know it again, for example the device.">
            <input className="ui-input" maxLength={64} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={busy || name.trim() === ""}>
              {busy ? "Waiting for the browser…" : "Add passkey"}
            </Button>
          </div>
        </form>
      ) : (
        <Notice tone="info" title="This browser cannot create passkeys." />
      )}
      {error && <Notice tone="err" title={error} />}
      {note && !error && <Notice tone="ok" title={note} />}
    </section>
  );
}
