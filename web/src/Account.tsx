// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { useTranslation } from "react-i18next";
import { EmailCard } from "./AccountEmail";
import { SecurityCard } from "./AccountSecurity";
import { NotificationsCard } from "./Notifications";
import type { User } from "./auth";
import i18n, { saveLanguage, type Language } from "./i18n";
import { roleLabel } from "./roles";
import { saveColorScheme, type ColorScheme } from "./scheme";
import { TokensCard } from "./Tokens";
import { Card, Notice, PageHead } from "./ui";

const schemes: readonly ColorScheme[] = ["system", "light", "dark"];
const langs: readonly Language[] = ["auto", "en", "de"];

type Props = { user: User; onScheme: (s: ColorScheme) => void; onLanguage: (l: Language) => void };

// Account is the page of the logged-in account: how the panel looks
// (ADR-0037) and which language it speaks (ADR-0071), both of which follow
// the account to every browser, how it logs in (password, authenticator
// app, passkeys), its email address (ADR-0044) and its API tokens
// (ADR-0018).
export function Account({ user, onScheme, onLanguage }: Props) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const current = user.colorScheme ?? "system";
  const language = user.language ?? "auto";

  // save applies a choice at once and then stores it; a failed save keeps
  // the choice until the next reload.
  async function save(apply: () => void, store: () => Promise<unknown>, failed: () => string) {
    apply();
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      await store();
      setSaved(true);
    } catch {
      setError(failed());
    } finally {
      setBusy(false);
    }
  }

  const pickScheme = (s: ColorScheme) => save(() => onScheme(s), () => saveColorScheme(s), () => i18n.t("account.scheme.failed"));
  // The message is looked up after the switch, so it is in the new language.
  const pickLanguage = (l: Language) => save(() => onLanguage(l), () => saveLanguage(l), () => i18n.t("account.language.failed"));

  return (
    <>
      <PageHead title={t("account.title")} description={t("account.loggedInAs", { username: user.username, role: roleLabel(user.role) })} />
      <Card title={t("account.appearance")}>
        <div className="ui-stack">
          <fieldset className="ui-stack-tight ui-fieldset" disabled={busy}>
            <legend>{t("account.scheme.legend")}</legend>
            {schemes.map((s) => (
              <label className="ui-check" key={s}>
                <input type="radio" name="scheme" value={s} checked={current === s} onChange={() => void pickScheme(s)} />
                <span>
                  {t(`account.scheme.${s}`)}
                  {s === "system" && <span className="ui-hint"> {t("account.scheme.systemHint")}</span>}
                </span>
              </label>
            ))}
          </fieldset>
          <fieldset className="ui-stack-tight ui-fieldset" disabled={busy}>
            <legend>{t("account.language.legend")}</legend>
            {langs.map((l) => (
              <label className="ui-check" key={l}>
                <input type="radio" name="language" value={l} checked={language === l} onChange={() => void pickLanguage(l)} />
                <span lang={l === "auto" ? undefined : l}>
                  {t(`account.language.${l}`)}
                  {l === "auto" && <span className="ui-hint"> {t("account.language.autoHint")}</span>}
                </span>
              </label>
            ))}
          </fieldset>
          {error !== "" && <Notice tone="err" title={error} />}
          {error === "" && saved && <Notice tone="ok" title={t("account.saved")} />}
        </div>
      </Card>
      <SecurityCard user={user} />
      <EmailCard />
      <NotificationsCard />
      <TokensCard admin={user.role === "admin"} />
    </>
  );
}
