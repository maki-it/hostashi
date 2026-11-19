// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from "react";
import { currentUser, setupRequired, type User } from "./auth";
import { Backups } from "./Backups";
import { Databases } from "./Databases";
import { Home } from "./Home";
import { Login } from "./Login";
import { Setup } from "./Setup";

type View =
  | { kind: "loading" }
  | { kind: "setup" }
  | { kind: "login"; notice?: string }
  | { kind: "home"; user: User }
  | { kind: "error" };

type Page = "home" | "databases" | "backups";

const pageFromHash = (): Page => {
  switch (window.location.hash) {
    case "#/databases":
      return "databases";
    case "#/backups":
      return "backups";
    default:
      return "home";
  }
};

export function App() {
  const [view, setView] = useState<View>({ kind: "loading" });
  const [page, setPage] = useState<Page>(pageFromHash);

  useEffect(() => {
    const onHash = () => setPage(pageFromHash());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

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
    <main className={view.kind === "home" ? "shell wide" : "shell"}>
      <header className="brand">
        <h1>Hostashi</h1>
        {view.kind === "home" && (
          <nav aria-label="Main">
            <a href="#/" aria-current={page === "home" ? "page" : undefined}>
              Overview
            </a>
            <a href="#/databases" aria-current={page === "databases" ? "page" : undefined}>
              Databases
            </a>
            <a href="#/backups" aria-current={page === "backups" ? "page" : undefined}>
              Backups
            </a>
          </nav>
        )}
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
      {view.kind === "home" && page === "home" && <Home user={view.user} onLogout={() => setView({ kind: "login" })} />}
      {view.kind === "home" && page === "databases" && <Databases />}
      {view.kind === "home" && page === "backups" && <Backups />}
    </main>
  );
}
