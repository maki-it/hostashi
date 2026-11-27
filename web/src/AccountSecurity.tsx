// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Trash } from "@phosphor-icons/react";
import { ApiError } from "./api";
import i18n from "./i18n";
import { addPasskey, changePassword, deletePasskey, listPasskeys, message, totpDisable, totpReplace, totpReplaceConfirm, type Passkey, type User } from "./auth";
import { TotpEnrolment } from "./SecondFactor";
import { Button, Card, Field, IconButton, Notice, RelTime } from "./ui";
import { passkeysSupported } from "./webauthn";

// securityMessage explains the errors of the account security forms.
export function securityMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `account.security.errors.${err.code}`;
    if (i18n.exists(key)) return (i18n.t as (k: string) => string)(key);
  }
  return message(err);
}

// SecurityCard lets the logged-in account change its password, set up or
// turn off the authenticator app (TOTP) and manage its passkeys. Every
// change needs this browser session; the server sends a security email.
export function SecurityCard({ user }: { user: User }) {
  const { t } = useTranslation();
  return (
    <Card title={t("account.security.title")}>
      <div className="ui-stack">
        <PasswordSection />
        <TotpSection user={user} />
        <PasskeySection />
      </div>
    </Card>
  );
}

function PasswordSection() {
  const { t } = useTranslation();
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
      setError(t("account.security.password.mismatch"));
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
      setError(err instanceof ApiError && err.code === "invalid_credentials" ? t("account.security.password.wrongCurrent") : securityMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ui-stack" aria-labelledby="sec-password">
      <h3 id="sec-password" className="ui-flush">
        {t("account.security.password.title")}
      </h3>
      <form className="ui-stack ui-narrow" onSubmit={(e) => void submit(e)} noValidate>
        <Field label={t("account.security.password.current")}>
          <input className="ui-input" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </Field>
        <Field label={t("account.security.password.new")} hint={t("account.security.password.newHint")}>
          <input className="ui-input" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
        <Field label={t("account.security.password.repeat")}>
          <input className="ui-input" type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} />
        </Field>
        {error && <Notice tone="err" title={error} />}
        {done && !error && <Notice tone="ok" title={t("account.security.password.changed")} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || current === "" || next === "" || repeat === ""}>
            {busy ? t("account.security.password.changing") : t("account.security.password.submit")}
          </Button>
        </div>
      </form>
    </section>
  );
}

function TotpSection({ user }: { user: User }) {
  const { t } = useTranslation();
  const [on, setOn] = useState(!!user.totp);
  const [mode, setMode] = useState<"" | "setup" | "off" | "move">("");
  const [moving, setMoving] = useState<{ secret: string; uri: string } | null>(null);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");

  function open(next: "setup" | "off" | "move") {
    setError("");
    setNote("");
    setMoving(null);
    setMode(next);
  }

  function close() {
    setMoving(null);
    setMode("");
  }

  // verify sends password and code for turning off or for moving.
  async function verify(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (mode === "move") {
        setMoving(await totpReplace(password, code.trim()));
      } else {
        await totpDisable(password, code.trim());
        setOn(false);
        setMode("");
        setNote(t("account.security.totp.turnedOff"));
      }
    } catch (err) {
      setError(securityMessage(err));
    } finally {
      setBusy(false);
      setPassword("");
      setCode("");
    }
  }

  return (
    <section className="ui-stack" aria-labelledby="sec-totp">
      <h3 id="sec-totp" className="ui-flush">
        {t("account.security.totp.title")}
      </h3>
      {mode === "setup" ? (
        <>
          <TotpEnrolment
            intro=""
            autoStart
            onDone={() => {
              setOn(true);
              setMode("");
              setNote(t("account.security.totp.turnedOn"));
            }}
          />
          <div className="ui-row">
            <Button onClick={close}>{t("common.actions.cancel")}</Button>
          </div>
        </>
      ) : mode === "move" && moving ? (
        <>
          <p className="ui-flush">{t("account.security.totp.moveIntro")}</p>
          <TotpEnrolment
            intro=""
            initial={moving}
            confirm={totpReplaceConfirm}
            submitLabel={t("account.security.totp.useNew")}
            doneTitle={t("account.security.totp.moveDone")}
            onDone={() => {
              close();
              setNote(t("account.security.totp.moved"));
            }}
          />
          <div className="ui-row">
            <Button onClick={close}>{t("common.actions.cancel")}</Button>
          </div>
        </>
      ) : mode === "off" || mode === "move" ? (
        <form className="ui-stack ui-narrow" onSubmit={(e) => void verify(e)} noValidate>
          <p className="ui-flush">
            {mode === "move"
              ? t("account.security.totp.verifyMove")
              : t("account.security.totp.verifyOff")}
          </p>
          <Field label={t("account.security.totp.password")}>
            <input className="ui-input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Field label={t("account.security.totp.code")}>
            <input className="ui-input" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} />
          </Field>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button type="submit" variant={mode === "move" ? "primary" : "danger"} disabled={busy || password === "" || code.trim() === ""}>
              {mode === "move" ? t("common.actions.continue") : t("account.security.totp.turnOff")}
            </Button>
            <Button disabled={busy} onClick={close}>
              {t("common.actions.cancel")}
            </Button>
          </div>
        </form>
      ) : (
        <>
          <p className="ui-flush">
            {on ? t("account.security.totp.on") : t("account.security.totp.off")}
          </p>
          {note && <Notice tone="ok" title={note} />}
          <div className="ui-row">
            {on ? (
              <>
                <Button onClick={() => open("move")}>{t("account.security.totp.move")}</Button>
                <Button variant="danger" onClick={() => open("off")}>
                  {t("account.security.totp.turnOff")}
                </Button>
              </>
            ) : (
              <Button variant="primary" onClick={() => open("setup")}>
                {t("account.security.totp.setUp")}
              </Button>
            )}
          </div>
        </>
      )}
    </section>
  );
}
function PasskeySection() {
  const { t } = useTranslation();
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
      setNote(t("account.security.passkeys.addedNote", { name: p.name }));
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
      setNote(t("account.security.passkeys.removedNote", { name: p.name }));
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
        {t("account.security.passkeys.title")}
      </h3>
      <p className="ui-flush ui-hint">{t("account.security.passkeys.intro")}</p>
      {keys === null ? (
        !error && <p aria-busy="true">{t("common.status.loading")}</p>
      ) : keys.length === 0 ? (
        <p className="ui-flush">{t("account.security.passkeys.none")}</p>
      ) : (
        <ul className="ui-plain-list" aria-label={t("account.security.passkeys.list")}>
          {keys.map((p) => (
            <li key={p.id} className="ui-list-row">
              <span>
                <strong>{p.name}</strong>{" "}
                <span className="ui-hint">
                  <Trans i18nKey="account.security.passkeys.added" components={{ time: <RelTime iso={p.createdAt} /> }} />
                  {p.lastUsedAt ? (
                    <Trans i18nKey="account.security.passkeys.lastUsed" components={{ time: <RelTime iso={p.lastUsedAt} /> }} />
                  ) : (
                    t("account.security.passkeys.neverUsed")
                  )}
                </span>
              </span>
              <IconButton label={t("account.security.passkeys.removeLabel", { name: p.name })} text={t("account.security.passkeys.remove")} icon={<Trash size={16} aria-hidden="true" />} danger disabled={busy} onClick={() => void remove(p)} />
            </li>
          ))}
        </ul>
      )}
      {supported ? (
        <form className="ui-stack ui-narrow" onSubmit={(e) => void add(e)} noValidate>
          <Field label={t("account.security.passkeys.name")} hint={t("account.security.passkeys.nameHint")}>
            <input className="ui-input" maxLength={64} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={busy || name.trim() === ""}>
              {busy ? t("account.security.passkeys.waiting") : t("account.security.passkeys.add")}
            </Button>
          </div>
        </form>
      ) : (
        <Notice tone="info" title={t("account.security.passkeys.unsupported")} />
      )}
      {error && <Notice tone="err" title={error} />}
      {note && !error && <Notice tone="ok" title={note} />}
    </section>
  );
}
