// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { accountEmailMessage, getAccountEmail, removeAccountEmail, setAccountEmail, type AccountEmail } from "./mail";
import { Button, Card, Field, Notice, RelTime } from "./ui";

// EmailCard holds the account's own email address (ADR-0044). A new
// address is used only after the link sent to it is opened.
export function EmailCard() {
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
      <Card title="Email">{error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading…</p>}</Card>
    );
  }

  const canAdd = state.mail_enabled;
  const showForm = canAdd && (editing || (state.email === "" && state.pending === ""));

  return (
    <Card title="Email">
      <div className="ui-stack">
        <p className="ui-flush ui-hint">
          For invitations, security notes about your account and the notifications you choose. Hostashi uses it only after you open the link it
          sends there.
        </p>
        {state.email !== "" && (
          <p className="ui-flush">
            Your address: <strong>{state.email}</strong>
          </p>
        )}
        {state.pending !== "" && (
          <Notice tone="info" title={"Waiting for confirmation: " + state.pending}>
            Open the link in the email we sent there
            {state.pending_expires && (
              <>
                {" "}
                before it expires <RelTime iso={state.pending_expires} />
              </>
            )}
            .
          </Notice>
        )}
        {sent && !error && <Notice tone="ok" title="We sent a link to the new address. Check the inbox, and the spam folder if it does not arrive." />}
        {!canAdd && state.email === "" && (
          <Notice tone="info" title="Email is not set up on this panel.">
            An administrator can set up an SMTP relay under Administration, Email.
          </Notice>
        )}
        {showForm ? (
          <form className="ui-stack" onSubmit={(e) => void save(e)}>
            <div className="ui-narrow">
              <Field label={state.email === "" ? "Email address" : "New email address"}>
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
                {busy ? "Sending…" : "Send confirmation link"}
              </Button>
              {editing && (
                <Button disabled={busy} onClick={() => setEditing(false)}>
                  Cancel
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
                    Change address
                  </Button>
                )}
                <Button variant="danger" disabled={busy} onClick={() => void remove()}>
                  Remove address
                </Button>
              </div>
            )}
          </>
        )}
      </div>
    </Card>
  );
}
