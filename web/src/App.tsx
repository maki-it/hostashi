// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from "react";
import { currentUser, setupRequired, type User } from "./auth";
import { Home } from "./Home";
import { Login } from "./Login";
import { Setup } from "./Setup";

type View =
  | { kind: "loading" }
  | { kind: "setup" }
  | { kind: "login"; notice?: string }
  | { kind: "home"; user: User }
  | { kind: "error" };

export function App() {
  const [view, setView] = useState<View>({ kind: "loading" });

  const boot = useCallback(async () => {
    setView({ kind: "loading" });
    try {
      if (await setupRequired()) {
        setView({ kind: "setup" });
        return;
      }
      const user = await currentUser();
      setView(user ? { kind: "home", user } : { kind: "login" });
    } catch {
      setView({ kind: "error" });
    }
  }, []);

  useEffect(() => {
    void boot();
  }, [boot]);

  return (
    <main className="shell">
      <header className="brand">
        <h1>Hostashi</h1>
      </header>
      {view.kind === "loading" && <p aria-busy="true">Loading…</p>}
      {view.kind === "error" && (
        <div role="alert" className="card">
          <p>The server is not reachable.</p>
          <button type="button" onClick={() => void boot()}>
            Try again
          </button>
        </div>
      )}
      {view.kind === "setup" && (
        <Setup onDone={() => setView({ kind: "login", notice: "Administrator account created. Log in now." })} />
      )}
      {view.kind === "login" && (
        <Login {...(view.notice ? { notice: view.notice } : {})} onLogin={(user) => setView({ kind: "home", user })} />
      )}
      {view.kind === "home" && <Home user={view.user} onLogout={() => setView({ kind: "login" })} />}
    </main>
  );
}
