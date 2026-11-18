// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { logout, type User } from "./auth";

type Props = { user: User; onLogout: () => void };

// Home is a placeholder until the dashboard exists.
export function Home({ user, onLogout }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function doLogout() {
    setBusy(true);
    setError("");
    try {
      await logout();
      onLogout();
    } catch {
      setError("Logout failed. Try again.");
      setBusy(false);
    }
  }

  return (
    <section className="card" aria-labelledby="home-title">
      <h2 id="home-title">Welcome, {user.username}</h2>
      <p>You are logged in as {user.role}.</p>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      <button type="button" className="secondary" disabled={busy} onClick={() => void doLogout()}>
        Log out
      </button>
    </section>
  );
}
