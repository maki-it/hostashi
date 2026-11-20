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
import { canSee } from "./nav";
import { formatHash, parseHash, type Page } from "./routes";
import { Shell } from "./Shell";
import { SitePage } from "./SitePage";
import { Users } from "./Users";

type View =
  | { kind: "loading" }
  | { kind: "setup" }
  | { kind: "login"; notice?: string }
  | { kind: "invite"; token: string }
  | { kind: "home"; user: User }
  | { kind: "error" };

// inviteToken returns the token of an invitation link (#/invite/<token>).
const inviteToken = (): string | null => {
  const m = /^#\/invite\/([A-Za-z0-9_-]{20,100})$/.exec(window.location.hash);
  return m ? (m[1] ?? null) : null;
};

export function App() {
  const [view, setView] = useState<View>({ kind: "loading" });
  const [route, setRoute] = useState(() => parseHash(window.location.hash));

  useEffect(() => {
    const onHash = () => setRoute(parseHash(window.location.hash));
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
  if (view.kind === "home" && !needsSecondFactor(view.user)) {
    const user = view.user;
    const page: Page = canSee(user.role, route.page) ? route.page : "home";
    const show = (p: Page) => page === p;
    // replaceQuery changes the query of the address without a new history
    // entry, so that typing in a filter does not fill the back button.
    const replaceQuery = (query: URLSearchParams) => {
      const next = { ...route, page, query };
      window.history.replaceState(null, "", formatHash(next));
      setRoute(next);
    };
    return (
      <Shell user={user} route={{ ...route, page }} onLogout={() => setView({ kind: "login" })}>
        {show("sites") &&
          (route.site ? (
            <SitePage
              key={route.site.name}
              name={route.site.name}
              tab={route.site.tab}
              onTab={(tab) => {
                window.location.hash = formatHash({ page: "sites", site: { name: route.site!.name, tab } });
              }}
            />
          ) : (
            <Sites query={route.query} onQuery={replaceQuery} />
          ))}
        <div className="app-legacy">
          {show("home") && <Home user={user} />}
          {show("health") && <Doctor />}
          {show("firewall") && <Firewall />}
          {show("php") && <PHPPage />}
          {show("limits") && <Presets />}
          {show("pages") && <Pages />}
          {show("parked") && <Parked />}
          {show("databases") && <Databases />}
          {show("backups") && <Backups />}
          {show("import") && <Migrate />}
          {show("users") && <Users me={user.username} />}
        </div>
      </Shell>
    );
  }

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
      {view.kind === "invite" && <Invite token={view.token} onDone={(user) => setView({ kind: "home", user })} />}
      {view.kind === "login" && (
        <Login {...(view.notice ? { notice: view.notice } : {})} onLogin={(user) => setView({ kind: "home", user })} />
      )}
      {view.kind === "home" && (
        <>
          <Home user={view.user} onLogout={() => setView({ kind: "login" })} />
          <SecondFactor onDone={() => setView({ kind: "home", user: { ...view.user, secondFactor: true } })} />
        </>
      )}
    </main>
  );
}
