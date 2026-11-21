// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { logout, type User } from "./auth";
import { Button, Card, Notice } from "./ui";

// onLogout is set where the page has no sidebar with a logout button: while
// the account still has to set up a second factor.
type Props = { user: User; onLogout?: () => void };

// Home greets an account that still has to set up a second factor; the
// panel itself starts on the Overview.
export function Home({ user, onLogout }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function doLogout() {
    setBusy(true);
    setError("");
    try {
      await logout();
      onLogout?.();
    } catch {
      setError("Logout failed. Try again.");
      setBusy(false);
    }
  }

  return (
    <Card title={`Welcome, ${user.username}`}>
      <div className="ui-stack">
        <p className="ui-flush">You are logged in as {user.role}.</p>
        {error && <Notice tone="err" title={error} />}
        {onLogout && (
          <Button disabled={busy} onClick={() => void doLogout()}>
            Log out
          </Button>
        )}
      </div>
    </Card>
  );
}
