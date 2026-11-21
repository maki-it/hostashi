// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { accountEmailMessage, getNotifications, setNotifications, type Notifications, type NotifyCategory } from "./mail";
import { Card, Notice } from "./ui";

const labels: Record<NotifyCategory, { label: string; hint: string }> = {
  backups: { label: "Backups", hint: "A backup failed or did not save everything." },
  certificates: { label: "Certificates", hint: "A renewal failed, or a certificate expires in 14 or 3 days." },
  doctor: { label: "Server checks", hint: "Once a day, when the checks on the Health page find errors." },
  php: { label: "PHP versions", hint: "Once a week, when a site uses a PHP version near or past its end of life." },
  disk: { label: "Disk space", hint: "A disk or a site's quota is more than 90 percent full." },
};

// NotificationsCard holds the switches for operations emails (ADR-0044).
// Account emails, such as security notes, cannot be switched off.
export function NotificationsCard() {
  const [state, setState] = useState<Notifications | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    getNotifications().then(setState, (err: unknown) => setError(accountEmailMessage(err)));
  }, []);

  async function toggle(id: NotifyCategory, enabled: boolean) {
    setBusy(true);
    setError("");
    try {
      setState(await setNotifications({ [id]: enabled }));
    } catch (err) {
      setError(accountEmailMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!state) {
    return (
      <Card title="Notifications">{error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading…</p>}</Card>
    );
  }

  const off = !state.mail_enabled || state.email === "";

  return (
    <Card title="Notifications">
      <div className="ui-stack">
        <p className="ui-flush ui-hint">
          Emails about problems on the server and your sites. Security notes about your account are always sent.
        </p>
        {!state.mail_enabled && <Notice tone="info" title="Notifications need email, which is not set up on this panel yet." />}
        {state.mail_enabled && state.email === "" && <Notice tone="info" title="Add and confirm an email address above to get notifications." />}
        <fieldset className="ui-stack-tight ui-fieldset" disabled={busy || off}>
          <legend className="ui-sr-only">Email me about</legend>
          {state.categories.map((c) => (
            <label key={c.id} className="ui-check">
              <input type="checkbox" checked={c.enabled} onChange={(e) => void toggle(c.id, e.target.checked)} />
              <span>
                {labels[c.id].label}
                <span className="ui-hint"> {labels[c.id].hint}</span>
              </span>
            </label>
          ))}
        </fieldset>
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}
