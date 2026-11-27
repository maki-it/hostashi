// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { accountEmailMessage, getAccountEmail, removeAccountEmail, setAccountEmail, type AccountEmail } from "./mail";
import { Button, Card, Field, Notice, RelTime } from "./ui";

// EmailCard holds the account's own email address (ADR-0044). A new
// address is used only after the link sent to it is opened.
export function EmailCard() {
  const { t } = useTranslation();
  const [state, setState] = useState<AccountEmail | null>(null);
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [sent, setSent] = useState(false);

  const load = useCallback(async () => {
    try {
      setState(await getAccountEmail());
    } catch (err) {
      setError(accountEmailMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      setState(await setAccountEmail(draft.trim()));
      setSent(true);
      setEditing(false);
      setDraft("");
    } catch (err) {
      setError(accountEmailMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    setError("");
    setSent(false);
    try {
      await removeAccountEmail();
      await load();
    } catch (err) {
      setError(accountEmailMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!state) {
    return (
      <Card title={t("account.email.title")}>{error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{t("common.status.loading")}</p>}</Card>
    );
  }

  const canAdd = state.mail_enabled;
  const showForm = canAdd && (editing || (state.email === "" && state.pending === ""));

  return (
    <Card title={t("account.email.title")}>
      <div className="ui-stack">
        <p className="ui-flush ui-hint">{t("account.email.intro")}</p>
        {state.email !== "" && (
          <p className="ui-flush">
            <Trans i18nKey="account.email.yours" values={{ email: state.email }} components={{ b: <strong /> }} />
          </p>
        )}
        {state.pending !== "" && (
          <Notice tone="info" title={t("account.email.pending", { email: state.pending })}>
            {state.pending_expires ? (
              <Trans i18nKey="account.email.openLinkBefore" components={{ time: <RelTime iso={state.pending_expires} /> }} />
            ) : (
              t("account.email.openLink")
            )}
          </Notice>
        )}
        {sent && !error && <Notice tone="ok" title={t("account.email.sent")} />}
        {!canAdd && state.email === "" && (
          <Notice tone="info" title={t("account.email.notSetUp")}>
            {t("account.email.notSetUpHint")}
          </Notice>
        )}
        {showForm ? (
          <form className="ui-stack" onSubmit={(e) => void save(e)}>
            <div className="ui-narrow">
              <Field label={state.email === "" ? t("account.email.address") : t("account.email.newAddress")}>
                <input
                  className="ui-input"
                  type="email"
                  autoComplete="email"
                  required
                  maxLength={254}
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                />
              </Field>
            </div>
            {error && <Notice tone="err" title={error} />}
            <div className="ui-row">
              <Button type="submit" variant="primary" disabled={busy || draft.trim() === ""}>
                {busy ? t("account.email.sending") : t("account.email.send")}
              </Button>
              {editing && (
                <Button disabled={busy} onClick={() => setEditing(false)}>
                  {t("common.actions.cancel")}
                </Button>
              )}
            </div>
          </form>
        ) : (
          <>
            {error && <Notice tone="err" title={error} />}
            {(state.email !== "" || state.pending !== "") && (
              <div className="ui-row">
                {canAdd && (
                  <Button disabled={busy} onClick={() => setEditing(true)}>
                    {t("account.email.change")}
                  </Button>
                )}
                <Button variant="danger" disabled={busy} onClick={() => void remove()}>
                  {t("account.email.remove")}
                </Button>
              </div>
            )}
          </>
        )}
      </div>
    </Card>
  );
}
