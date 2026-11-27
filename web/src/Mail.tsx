// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { clearMail, defaultPort, getMail, mailMessage, setMail, testMail, type MailInput, type MailSettings, type TLSMode } from "./mail";
import { Button, Card, Field, Notice, PageHead } from "./ui";

type Draft = {
  host: string;
  port: string;
  tls: TLSMode;
  username: string;
  password: string;
  /** The stored password is replaced, or removed when password is empty. */
  replacePassword: boolean;
  from: string;
  fromName: string;
  replyTo: string;
  caPEM: string;
};

const toDraft = (s: MailSettings): Draft => ({
  host: s.host,
  port: s.configured ? String(s.port) : "587",
  tls: s.tls === "" ? "starttls" : s.tls,
  username: s.username,
  password: "",
  replacePassword: !s.password_set,
  from: s.from,
  fromName: s.configured ? s.from_name : "Hostashi",
  replyTo: s.reply_to,
  caPEM: s.ca_pem,
});

type Flash = { alert: boolean; text: string } | null;

// Mail sets up the SMTP relay the panel sends notifications and invitation
// links through (ADR-0044). Without one, the panel works as before.
export function Mail() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<MailSettings | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setSettings(await getMail());
      setError("");
    } catch (err) {
      setError(mailMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <>
      <PageHead
        title={t("mail.title")}
        description={t("mail.description")}
      />
      {!settings ? (
        error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>{t("common.actions.tryAgain")}</Button>
          </Notice>
        ) : (
          <p aria-busy="true">{t("common.status.loading")}</p>
        )
      ) : (
        <Editor key={JSON.stringify(settings)} settings={settings} onSaved={setSettings} />
      )}
    </>
  );
}

function Editor({ settings, onSaved }: { settings: MailSettings; onSaved: (s: MailSettings) => void }) {
  const [d, setD] = useState<Draft>(() => toDraft(settings));
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<Flash>(null);
  const { t } = useTranslation();
  const set = (patch: Partial<Draft>) => setD((x) => ({ ...x, ...patch }));

  const setTLS = (tls: TLSMode) => {
    // Follow the usual port while it was the other mode's usual one.
    const port = d.port === String(defaultPort(d.tls)) ? String(defaultPort(tls)) : d.port;
    set({ tls, port });
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const port = Number(d.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      setFlash({ alert: true, text: t("mail.relay.portProblem") });
      return;
    }
    const input: MailInput = {
      host: d.host.trim(),
      port,
      tls: d.tls,
      username: d.username.trim(),
      from: d.from.trim(),
      from_name: d.fromName.trim(),
      reply_to: d.replyTo.trim(),
      ca_pem: d.caPEM.trim(),
    };
    if (input.username === "") input.password = "";
    else if (d.replacePassword) input.password = d.password;
    setBusy(true);
    try {
      const saved = await setMail(input);
      onSaved(saved);
    } catch (err) {
      setFlash({ alert: true, text: mailMessage(err) });
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await clearMail();
      onSaved(await getMail());
    } catch (err) {
      setFlash({ alert: true, text: mailMessage(err) });
      setBusy(false);
    }
  };

  return (
    <div className="ui-stack">
      {!settings.configured && (
        <Notice tone="info" title={t("mail.off")}>
          {t("mail.offText")}
        </Notice>
      )}
      <form className="ui-stack" onSubmit={(e) => void save(e)}>
        <Card title={t("mail.relay.title")}>
          <div className="ui-stack">
            <div className="ui-fields">
              <Field label={t("mail.relay.host")} hint={t("mail.relay.hostHint")}>
                <input
                  className="ui-input ui-mono"
                  value={d.host}
                  onChange={(e) => set({ host: e.target.value })}
                  required
                  maxLength={253}
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <Field label={t("mail.relay.encryption")} hint={t("mail.relay.encryptionHint")}>
                <select className="ui-select" value={d.tls} onChange={(e) => setTLS(e.target.value as TLSMode)}>
                  <option value="starttls">{t("mail.relay.starttls")}</option>
                  <option value="tls">{t("mail.relay.tls")}</option>
                </select>
              </Field>
              <Field label={t("mail.relay.port")}>
                <input className="ui-input ui-mono" inputMode="numeric" value={d.port} onChange={(e) => set({ port: e.target.value })} required />
              </Field>
            </div>
            <div className="ui-fields">
              <Field label={t("mail.relay.username")} hint={t("mail.relay.usernameHint")}>
                <input
                  className="ui-input"
                  value={d.username}
                  onChange={(e) => set({ username: e.target.value })}
                  maxLength={256}
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              {d.username.trim() !== "" &&
                (d.replacePassword || d.username.trim() !== settings.username ? (
                  <Field label={t("mail.relay.password")} hint={t("mail.relay.passwordHint")}>
                    <input
                      className="ui-input"
                      type="password"
                      value={d.password}
                      onChange={(e) => set({ password: e.target.value, replacePassword: true })}
                      maxLength={1024}
                      autoComplete="new-password"
                    />
                  </Field>
                ) : (
                  <div className="ui-stack-tight">
                    <span className="ui-hint">{t("mail.relay.passwordStored")}</span>
                    <div className="ui-row">
                      <Button onClick={() => set({ replacePassword: true, password: "" })}>{t("mail.relay.replacePassword")}</Button>
                    </div>
                  </div>
                ))}
            </div>
          </div>
        </Card>
        <Card title={t("mail.sender.title")}>
          <div className="ui-fields">
            <Field label={t("mail.sender.from")} hint={t("mail.sender.fromHint")}>
              <input
                className="ui-input"
                type="email"
                value={d.from}
                onChange={(e) => set({ from: e.target.value })}
                required
                maxLength={254}
                spellCheck={false}
              />
            </Field>
            <Field label={t("mail.sender.fromName")}>
              <input className="ui-input" value={d.fromName} onChange={(e) => set({ fromName: e.target.value })} maxLength={64} />
            </Field>
            <Field label={t("mail.sender.replyTo")} hint={t("mail.sender.replyToHint")}>
              <input
                className="ui-input"
                type="email"
                value={d.replyTo}
                onChange={(e) => set({ replyTo: e.target.value })}
                maxLength={254}
                spellCheck={false}
              />
            </Field>
          </div>
        </Card>
        <details className="ui-card">
          <summary className="ui-card-body">{t("mail.ca.title")}</summary>
          <div className="ui-card-body">
            <Field label={t("mail.ca.label")} hint={t("mail.ca.hint")}>
              <textarea
                className="ui-input ui-textarea ui-mono"
                rows={4}
                value={d.caPEM}
                onChange={(e) => set({ caPEM: e.target.value })}
                spellCheck={false}
              />
            </Field>
          </div>
        </details>
        {flash && <Notice tone={flash.alert ? "err" : "ok"} title={flash.text} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {busy ? t("common.status.saving") : t("common.actions.save")}
          </Button>
          {settings.configured && (
            <Button variant="danger" disabled={busy} onClick={() => void remove()}>
              {t("mail.turnOff")}
            </Button>
          )}
        </div>
      </form>
      {settings.configured && <TestMail />}
    </div>
  );
}

function TestMail() {
  const [to, setTo] = useState("");
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<Flash>(null);
  const { t } = useTranslation();

  const send = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setFlash(null);
    try {
      await testMail(to.trim());
      setFlash({ alert: false, text: t("mail.test.sent") });
    } catch (err) {
      setFlash({ alert: true, text: mailMessage(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title={t("mail.test.title")}>
      <form className="ui-stack" onSubmit={(e) => void send(e)}>
        <div className="ui-narrow">
          <Field label={t("mail.test.to")}>
            <input className="ui-input" type="email" value={to} onChange={(e) => setTo(e.target.value)} required maxLength={254} spellCheck={false} />
          </Field>
        </div>
        {flash && <Notice tone={flash.alert ? "err" : "ok"} title={flash.text} />}
        <div className="ui-row">
          <Button type="submit" disabled={busy}>
            {busy ? t("mail.test.sending") : t("mail.test.submit")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
