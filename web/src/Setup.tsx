// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { message, setup } from "./auth";
import { Button, Card, Field, Notice } from "./ui";

type Props = { onDone: () => void };

// Setup creates the first administrator. The token is in the file
// "setup-token" next to the database and is printed by the installer.
export function Setup({ onDone }: Props) {
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (password !== repeat) {
      setError("The passwords do not match.");
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
    <Card title="First-time setup">
      <div className="ui-stack">
        <p className="ui-flush">Create the administrator account. You need the setup token from the server.</p>
        <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
          <Field label="Setup token">
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
          <Field label="Username">
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
          <Field label="Password">
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
          <Field label="Repeat password">
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
              Create account
            </Button>
          </div>
        </form>
      </div>
    </Card>
  );
}
