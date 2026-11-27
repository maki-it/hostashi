// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { message, setup } from "./auth";
import { Button, Card, Field, Notice } from "./ui";

type Props = { initialToken?: string; onDone: () => void };

// Setup creates the first administrator. The token is in the file
// "setup-token" next to the database and is printed by the installer, also
// as a link that fills it in (initialToken).
export function Setup({ initialToken = "", onDone }: Props) {
  const { t } = useTranslation();
  const [token, setToken] = useState(initialToken);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (password !== repeat) {
      setError(t("auth.setup.mismatch"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await setup(token.trim(), username, password);
      onDone();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("auth.setup.title")}>
      <div className="ui-stack">
        <p className="ui-flush">
          {initialToken
            ? t("auth.setup.introLink")
            : t("auth.setup.intro")}
        </p>
        <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
          <Field label={t("auth.setup.token")}>
            <input
              className="ui-input"
              name="token"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              required
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
          </Field>
          <Field label={t("auth.setup.username")}>
            <input
              className="ui-input"
              name="username"
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field label={t("auth.setup.password")}>
            <input
              className="ui-input"
              name="password"
              type="password"
              autoComplete="new-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Field label={t("auth.setup.repeat")}>
            <input
              className="ui-input"
              name="repeat"
              type="password"
              autoComplete="new-password"
              required
              value={repeat}
              onChange={(e) => setRepeat(e.target.value)}
            />
          </Field>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-auth-actions">
            <Button variant="primary" type="submit" disabled={busy || !token.trim() || !username || !password || !repeat}>
              {t("auth.setup.submit")}
            </Button>
          </div>
        </form>
      </div>
    </Card>
  );
}
