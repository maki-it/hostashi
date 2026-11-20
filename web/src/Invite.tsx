// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import type { User } from "./auth";
import { acceptInvitation, checkInvitation, usersMessage } from "./users";

type Props = { token: string; onDone: (user: User) => void };

// Invite is the page of a one-time invitation link (ADR-0035): the new user
// sets their own password and is logged in.
export function Invite({ token, onDone }: Props) {
  const [username, setUsername] = useState<string | null>(null);
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    checkInvitation(token).then(
      (name) => live && setUsername(name),
      (err: unknown) => live && setError(usersMessage(err)),
    );
    return () => {
      live = false;
    };
  }, [token]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (password !== repeat) {
      setError("The passwords do not match.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const user = await acceptInvitation(token, password);
      // The link is used up; keep it out of the history and the address bar.
      window.history.replaceState(null, "", "#/");
      onDone(user);
    } catch (err) {
      setError(usersMessage(err));
      setBusy(false);
    }
  }

  return (
    <section className="card" aria-labelledby="invite-title">
      <h2 id="invite-title">Set your password</h2>
      {username === null ? (
        error ? (
          <p role="alert" className="error">
            {error}
          </p>
        ) : (
          <p aria-busy="true">Checking the link…</p>
        )
      ) : (
        <form onSubmit={(e) => void submit(e)} noValidate>
          <p>
            You were invited as <strong>{username}</strong>. Choose a password of at least 12 characters.
          </p>
          <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
          <label>
            Password
            <input
              type="password"
              name="password"
              autoComplete="new-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          <label>
            Repeat password
            <input
              type="password"
              name="repeat"
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
          <button type="submit" disabled={busy || password === ""}>
            {busy ? "Saving…" : "Set password and log in"}
          </button>
        </form>
      )}
    </section>
  );
}
