// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useLayoutEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Account } from "./Account";
import { ConfirmEmail } from "./ConfirmEmail";
import { currentUser, needsSecondFactor, setupRequired, type User } from "./auth";
import { Audit } from "./Audit";
import { Backups } from "./Backups";
import { Databases } from "./Databases";
import { Device } from "./Device";
import { Doctor } from "./Doctor";
import { Firewall } from "./Firewall";
import { Home } from "./Home";
import { Invite } from "./Invite";
import { Presets } from "./Limits";
import { Profiles } from "./Profiles";
import { Login } from "./Login";
import { Mail } from "./Mail";
import { DNSAccounts } from "./DNSAccounts";
import { Migrate } from "./Migrate";
import { Pages } from "./Pages";
import { Overview } from "./Overview";
import { Parked } from "./Parked";
import { NodePage } from "./NodePage";
import { PythonPage } from "./PythonPage";
import { PHPPage } from "./PHP";
import { SecondFactor } from "./SecondFactor";
import { Services } from "./Services";
import { Setup } from "./Setup";
import { Sites } from "./Sites";
import { Pools } from "./Pools";
import { System } from "./System";
import { canSee } from "./nav";
import { formatHash, parseHash, type Page } from "./routes";
import { Mark, Shell } from "./Shell";
import { SitePage } from "./SitePage";
import { Users } from "./Users";
import { Button, Notice } from "./ui";
import { applyScheme, type ColorScheme } from "./scheme";
import { setLanguage, type Language } from "./i18n";

type View =
  | { kind: "loading" }
  | { kind: "setup" }
  | { kind: "login"; notice?: string }
  | { kind: "invite"; token: string }
  | { kind: "confirm-email"; token: string }
  | { kind: "home"; user: User }
  | { kind: "error" };

// confirmToken returns the token of an email confirmation link
// (#/confirm-email/<token>, ADR-0044).
const confirmToken = (): string | null => {
  const m = /^#\/confirm-email\/([A-Za-z0-9_-]{20,100})$/.exec(window.location.hash);
  return m ? (m[1] ?? null) : null;
};

// inviteToken returns the token of an invitation link (#/invite/<token>).
const inviteToken = (): string | null => {
  const m = /^#\/invite\/([A-Za-z0-9_-]{20,100})$/.exec(window.location.hash);
  return m ? (m[1] ?? null) : null;
};

// linkSetupToken returns the token of the link the installer prints
// (#/setup/<token>). The token sits in the fragment, so the browser never
// sends it to the server and it does not land in nginx's logs.
const linkSetupToken = (): string => {
  const m = /^#\/setup\/([0-9a-f]{48})$/.exec(window.location.hash);
  return m ? (m[1] ?? "") : "";
};

export function App() {
  const { t } = useTranslation();
  const [view, setView] = useState<View>({ kind: "loading" });
  const [setupToken] = useState(linkSetupToken);
  const [route, setRoute] = useState(() => parseHash(setupToken ? "" : window.location.hash));

  // The setup link leaves the address bar and the history right away, so
  // the token is neither bookmarked nor shown to the next person.
  useEffect(() => {
    if (setupToken) window.history.replaceState(null, "", window.location.pathname + window.location.search);
  }, [setupToken]);

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
      const confirm = confirmToken();
      if (confirm) {
        setView({ kind: "confirm-email", token: confirm });
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

  // The account's colour scheme applies once logged in; before that, and
  // after logout, the panel follows the system (ADR-0037).
  const scheme: ColorScheme = view.kind === "home" ? (view.user.colorScheme ?? "system") : "system";
  useEffect(() => applyScheme(scheme), [scheme]);
  const onScheme = (s: ColorScheme) => setView((v) => (v.kind === "home" ? { ...v, user: { ...v.user, colorScheme: s } } : v));

  // The account's language applies once logged in; the login and setup
  // pages, and the panel after logout, follow the browser (ADR-0071). A
  // layout effect, so the first frame after login is already in it.
  const language: Language = view.kind === "home" ? (view.user.language ?? "auto") : "auto";
  useLayoutEffect(() => {
    setLanguage(language);
  }, [language]);
  const onLanguage = (l: Language) => setView((v) => (v.kind === "home" ? { ...v, user: { ...v.user, language: l } } : v));

  // locked: the account must set up a second factor first, and the server
  // refuses every other page until then.
  if (view.kind === "home" && !needsSecondFactor(view.user)) {
    const user = view.user;
    const page: Page = route.page === "account" || route.page === "device" || canSee(user.role, route.page) ? route.page : "home";
    const show = (p: Page) => page === p;
    // replaceQuery changes the query of the address without a new history
    // entry, so that typing in a filter does not fill the back button.
    const replaceQuery = (query: URLSearchParams) => {
      const next = { ...route, page, query };
      window.history.replaceState(null, "", formatHash(next));
      setRoute(next);
    };
    return (
      <Shell user={user} route={{ ...route, page }} onLogout={() => setView({ kind: "login" })} onScheme={onScheme}>
        {show("home") && <Overview user={user} />}
        {show("sites") &&
          (route.site ? (
            <SitePage
              key={route.site.name}
              name={route.site.name}
              tab={route.site.tab}
              role={user.role}
              onTab={(tab) => {
                window.location.hash = formatHash({ page: "sites", site: { name: route.site!.name, tab } });
              }}
              path={route.query.get("path") ?? ""}
              onPath={(path) => {
                const query = new URLSearchParams(path === "" ? {} : { path });
                window.location.hash = formatHash({ page: "sites", site: { name: route.site!.name, tab: "files" }, query });
              }}
            />
          ) : (
            <Sites query={route.query} onQuery={replaceQuery} role={user.role} />
          ))}
        {show("databases") && <Databases role={user.role} query={route.query} onQuery={replaceQuery} />}
        {show("backups") && <Backups query={route.query} onQuery={replaceQuery} />}
        {show("php") && <PHPPage query={route.query} onQuery={replaceQuery} />}
        {show("node") && <NodePage />}
        {show("python") && <PythonPage />}
        {show("limits") && <Presets query={route.query} onQuery={replaceQuery} />}
        {show("profiles") && <Profiles query={route.query} onQuery={replaceQuery} />}
        {show("health") && <Doctor query={route.query} onQuery={replaceQuery} />}
        {show("services") && <Services />}
        {show("system") && <System />}
        {show("pools") && <Pools />}
        {show("firewall") && <Firewall query={route.query} onQuery={replaceQuery} />}
        {show("parked") && <Parked query={route.query} onQuery={replaceQuery} />}
        {show("pages") && <Pages query={route.query} onQuery={replaceQuery} />}
        {show("users") && <Users me={user.username} query={route.query} onQuery={replaceQuery} />}
        {show("mail") && <Mail />}
        {show("dns") && <DNSAccounts query={route.query} />}
        {show("import") && <Migrate query={route.query} onQuery={replaceQuery} />}
        {show("audit") && <Audit query={route.query} onQuery={replaceQuery} />}
        {show("account") && <Account user={user} onScheme={onScheme} onLanguage={onLanguage} />}
        {show("device") && <Device code={route.query.get("code") ?? ""} />}
      </Shell>
    );
  }

  // The session check usually answers within a few milliseconds. Until it
  // does, nothing of the login page shows, so a reload while logged in does
  // not flash the login brand; a spinner fades in only when the check takes
  // noticeably long (CSS delay in base.css).
  if (view.kind === "loading") {
    return (
      <main className="ui-root ui-boot" aria-busy="true" aria-label={t("auth.boot.loading")}>
        <span className="ui-boot-spinner" aria-hidden="true" />
      </main>
    );
  }

  return (
    <main className="ui-root ui-auth">
      <header className="app-brand ui-auth-brand">
        <Mark />
        <span className="app-brand-name">
          Hostashi
          <span className="app-host">{window.location.hostname}</span>
        </span>
      </header>
      {view.kind === "error" && (
        <Notice tone="err" title={t("common.errors.unreachable")}>
          <Button onClick={() => void boot()}>{t("common.actions.tryAgain")}</Button>
        </Notice>
      )}
      {view.kind === "setup" && (
        <Setup
          initialToken={setupToken}
          onDone={() => setView({ kind: "login", notice: t("auth.setup.done") })}
        />
      )}
      {view.kind === "confirm-email" && <ConfirmEmail token={view.token} onDone={() => void boot()} />}
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
