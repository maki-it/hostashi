// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
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
        title="Email"
        description="Hostashi sends notifications and invitation links through an SMTP relay you already use. It is not a mail server and receives no mail."
      />
      {!settings ? (
        error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>Try again</Button>
          </Notice>
        ) : (
          <p aria-busy="true">Loading…</p>
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
      setFlash({ alert: true, text: "Enter a port between 1 and 65535, usually 587 for STARTTLS or 465 for TLS." });
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
        <Notice tone="info" title="Email is off.">
          Invitation links are shown to copy, and notifications appear only in the panel. Enter a relay to send them by email as well.
        </Notice>
      )}
      <form className="ui-stack" onSubmit={(e) => void save(e)}>
        <Card title="SMTP relay">
          <div className="ui-stack">
            <div className="ui-fields">
              <Field label="Host" hint="For example smtp.example.com.">
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
              <Field label="Encryption" hint="Only encrypted connections. The certificate is always checked.">
                <select className="ui-select" value={d.tls} onChange={(e) => setTLS(e.target.value as TLSMode)}>
                  <option value="starttls">STARTTLS (port 587)</option>
                  <option value="tls">TLS (port 465)</option>
                </select>
              </Field>
              <Field label="Port">
                <input className="ui-input ui-mono" inputMode="numeric" value={d.port} onChange={(e) => set({ port: e.target.value })} required />
              </Field>
            </div>
            <div className="ui-fields">
              <Field label="Username (optional)" hint="Leave empty if the relay trusts this server without a login.">
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
                  <Field label="Password" hint="Stored encrypted with the server key and never shown again.">
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
                    <span className="ui-hint">A password is stored.</span>
                    <div className="ui-row">
                      <Button onClick={() => set({ replacePassword: true, password: "" })}>Replace password</Button>
                    </div>
                  </div>
                ))}
            </div>
          </div>
        </Card>
        <Card title="Sender">
          <div className="ui-fields">
            <Field label="From address" hint="An address the relay may send as, for example panel@example.com.">
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
            <Field label="From name (optional)">
              <input className="ui-input" value={d.fromName} onChange={(e) => set({ fromName: e.target.value })} maxLength={64} />
            </Field>
            <Field label="Reply-to (optional)" hint="Where replies go, for example your own address.">
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
          <summary className="ui-card-body">Private certificate authority (optional)</summary>
          <div className="ui-card-body">
            <Field label="CA certificate (PEM)" hint="Only for a relay whose certificate comes from your own CA. The usual public CAs are always trusted.">
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
            {busy ? "Saving…" : "Save"}
          </Button>
          {settings.configured && (
            <Button variant="danger" disabled={busy} onClick={() => void remove()}>
              Turn email off
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

  const send = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setFlash(null);
    try {
      await testMail(to.trim());
      setFlash({ alert: false, text: "The relay took the email. Check the inbox, and the spam folder if it does not arrive." });
    } catch (err) {
      setFlash({ alert: true, text: mailMessage(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title="Send a test email">
      <form className="ui-stack" onSubmit={(e) => void send(e)}>
        <div className="ui-narrow">
          <Field label="Send to">
            <input className="ui-input" type="email" value={to} onChange={(e) => setTo(e.target.value)} required maxLength={254} spellCheck={false} />
          </Field>
        </div>
        {flash && <Notice tone={flash.alert ? "err" : "ok"} title={flash.text} />}
        <div className="ui-row">
          <Button type="submit" disabled={busy}>
            {busy ? "Sending…" : "Send test email"}
          </Button>
        </div>
      </form>
    </Card>
  );
}
