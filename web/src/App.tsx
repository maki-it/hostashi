// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from "react";
import { currentUser, needsSecondFactor, setupRequired, type User } from "./auth";
import { Backups } from "./Backups";
import { Databases } from "./Databases";
import { Doctor } from "./Doctor";
import { Firewall } from "./Firewall";
import { Home } from "./Home";
import { Invite } from "./Invite";
import { Presets } from "./Limits";
import { Login } from "./Login";
import { Migrate } from "./Migrate";
import { Pages } from "./Pages";
import { Parked } from "./Parked";
import { PHPPage } from "./PHP";
import { SecondFactor } from "./SecondFactor";
import { Setup } from "./Setup";
import { Sites } from "./Sites";
import { Users } from "./Users";

type View =
  | { kind: "loading" }
  | { kind: "setup" }
  | { kind: "login"; notice?: string }
  | { kind: "invite"; token: string }
  | { kind: "home"; user: User }
  | { kind: "error" };

type Page = "home" | "health" | "firewall" | "sites" | "php" | "limits" | "pages" | "parked" | "databases" | "backups" | "import" | "users";

// nav lists the pages and the roles that see them (ADR-0035). The server
// decides; this only hides what a role cannot use.
const nav: { page: Page; hash: string; label: string; roles: string[] }[] = [
  { page: "home", hash: "#/", label: "Overview", roles: ["admin", "manager", "user"] },
  { page: "health", hash: "#/health", label: "Health", roles: ["admin"] },
  { page: "firewall", hash: "#/firewall", label: "Firewall", roles: ["admin"] },
  { page: "sites", hash: "#/sites", label: "Sites", roles: ["admin", "manager", "user"] },
  { page: "php", hash: "#/php", label: "PHP", roles: ["admin"] },
  { page: "limits", hash: "#/limits", label: "Limits", roles: ["admin", "manager"] },
  { page: "pages", hash: "#/pages", label: "Pages", roles: ["admin", "manager"] },
  { page: "parked", hash: "#/parked", label: "Parked domains", roles: ["admin", "manager"] },
  { page: "databases", hash: "#/databases", label: "Databases", roles: ["admin", "manager", "user"] },
  { page: "backups", hash: "#/backups", label: "Backups", roles: ["admin"] },
  { page: "import", hash: "#/import", label: "CloudPanel import", roles: ["admin"] },
  { page: "users", hash: "#/users", label: "Users", roles: ["admin"] },
];

const pageFromHash = (): Page => nav.find((n) => n.hash !== "#/" && n.hash === window.location.hash)?.page ?? "home";

// inviteToken returns the token of an invitation link (#/invite/<token>).
const inviteToken = (): string | null => {
  const m = /^#\/invite\/([A-Za-z0-9_-]{20,100})$/.exec(window.location.hash);
  return m ? (m[1] ?? null) : null;
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
      const token = inviteToken();
      if (token) {
        setView({ kind: "invite", token });
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

  // locked: the account must set up a second factor first, and the server
  // refuses every other page until then.
  const locked = view.kind === "home" && needsSecondFactor(view.user);
  const open = view.kind === "home" && !locked;
  const role = view.kind === "home" ? view.user.role : "";
  const allowed = (p: Page) => nav.some((n) => n.page === p && n.roles.includes(role));
  const show = (p: Page) => open && page === p && allowed(p);

  return (
    <main className={view.kind === "home" ? "shell wide" : "shell"}>
      <header className="brand">
        <h1>Hostashi</h1>
        {open && (
          <nav aria-label="Main">
            {nav
              .filter((n) => n.roles.includes(role))
              .map((n) => (
                <a key={n.page} href={n.hash} aria-current={page === n.page ? "page" : undefined}>
                  {n.label}
                </a>
              ))}
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
      {view.kind === "invite" && <Invite token={view.token} onDone={(user) => setView({ kind: "home", user })} />}
      {view.kind === "login" && (
        <Login {...(view.notice ? { notice: view.notice } : {})} onLogin={(user) => setView({ kind: "home", user })} />
      )}
      {view.kind === "home" && (locked || page === "home" || !allowed(page)) && <Home user={view.user} onLogout={() => setView({ kind: "login" })} />}
      {view.kind === "home" && locked && (
        <SecondFactor onDone={() => setView({ kind: "home", user: { ...view.user, secondFactor: true } })} />
      )}
      {show("health") && <Doctor />}
      {show("firewall") && <Firewall />}
      {show("sites") && <Sites />}
      {show("php") && <PHPPage />}
      {show("limits") && <Presets />}
      {show("pages") && <Pages />}
      {show("parked") && <Parked />}
      {show("databases") && <Databases />}
      {show("backups") && <Backups />}
      {show("import") && <Migrate />}
      {show("users") && view.kind === "home" && <Users me={view.user.username} />}
    </main>
  );
}
