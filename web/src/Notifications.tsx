// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { accountEmailMessage, getNotifications, setNotifications, type Notifications, type NotifyCategory } from "./mail";
import { AppliesAtOnce, Card, Notice, Switch } from "./ui";

// The categories the panel knows; each has a label and a hint under
// account.notifications.categories.<id>. A category from a newer server
// that this page does not know is left out.
const known: readonly NotifyCategory[] = ["backups", "certificates", "doctor", "services", "deploys", "php", "disk"];

// NotificationsCard holds the switches for operations emails (ADR-0044).
// Account emails, such as security notes, cannot be switched off.
export function NotificationsCard() {
  const { t } = useTranslation();
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
      <Card title={t("account.notifications.title")}>{error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{t("common.status.loading")}</p>}</Card>
    );
  }

  const off = !state.mail_enabled || state.email === "";

  return (
    <Card title={t("account.notifications.title")} actions={<AppliesAtOnce />}>
      <div className="ui-stack">
        <p className="ui-flush ui-hint">{t("account.notifications.intro")}</p>
        {!state.mail_enabled && <Notice tone="info" title={t("account.notifications.noMail")} />}
        {state.mail_enabled && state.email === "" && <Notice tone="info" title={t("account.notifications.noAddress")} />}
        <fieldset className="ui-stack-tight ui-fieldset" disabled={busy || off}>
          <legend className="ui-sr-only">{t("account.notifications.legend")}</legend>
          {state.categories.filter((c) => known.includes(c.id)).map((c) => (
            <Switch key={c.id} checked={c.enabled} onChange={(on) => void toggle(c.id, on)} hint={t(`account.notifications.categories.${c.id}.hint`)}>
              {t(`account.notifications.categories.${c.id}.label`)}
            </Switch>
          ))}
        </fieldset>
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}
