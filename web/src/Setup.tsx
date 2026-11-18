// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { message, setup } from "./auth";

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
    <section className="card" aria-labelledby="setup-title">
      <h2 id="setup-title">First-time setup</h2>
      <p>Create the administrator account. You need the setup token from the server.</p>
      <form onSubmit={(e) => void submit(e)} noValidate>
        <label>
          Setup token
          <input
            name="token"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            required
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
        </label>
        <label>
          Username
          <input
            name="username"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            required
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </label>
        <label>
          Password
          <input
            name="password"
            type="password"
            autoComplete="new-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <label>
          Repeat password
          <input
            name="repeat"
            type="password"
            autoComplete="new-password"
            required
            value={repeat}
            onChange={(e) => setRepeat(e.target.value)}
          />
        </label>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <button type="submit" disabled={busy || !token.trim() || !username || !password || !repeat}>
          Create account
        </button>
      </form>
    </section>
  );
}
