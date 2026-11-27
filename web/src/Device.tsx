// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { approveDeviceLogin, deviceMessage, getDeviceLogin, normalizeCode, type DeviceLogin } from "./device";
import { GrantFields, grantReady, useGrant } from "./TokenGrant";
import { Button, Card, Field, Notice, PageHead, RelTime } from "./ui";

// Device approves the code that "hostashi login" shows (ADR-0058 addendum 1).
// The CLI then gets an API token with the scopes, sites and lifetime chosen
// here.
export function Device({ code: initial }: { code: string }) {
  const { t } = useTranslation();
  const [code, setCode] = useState(normalizeCode(initial));
  const [login, setLogin] = useState<DeviceLogin | null>(null);
  const [done, setDone] = useState<"approved" | "denied" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function lookup(c: string) {
    setBusy(true);
    setError("");
    try {
      setLogin(await getDeviceLogin(c));
    } catch (err) {
      setError(deviceMessage(err));
    } finally {
      setBusy(false);
    }
  }

  // A link from the CLI carries the code: look it up right away.
  useEffect(() => {
    if (initial !== "") void lookup(normalizeCode(initial));
  }, [initial]);

  function submit(e: FormEvent) {
    e.preventDefault();
    void lookup(normalizeCode(code));
  }

  return (
    <>
      <PageHead title={t("tokens.device.title")} description={t("tokens.device.intro")} />
      {done === "approved" && (
        <Notice tone="ok" title={t("tokens.device.approved")}>
          {t("tokens.device.approvedText")}
        </Notice>
      )}
      {done === "denied" && <Notice tone="info" title={t("tokens.device.denied")} />}
      {done === null && login === null && (
        <Card title={t("tokens.device.code")}>
          <form className="ui-stack" onSubmit={submit}>
            <Field label={t("tokens.device.codeLabel")} hint={t("tokens.device.codeHint")}>
              <input
                className="ui-input"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                autoComplete="off"
                autoCapitalize="characters"
                spellCheck={false}
                autoFocus
              />
            </Field>
            {error && <Notice tone="err" title={error} />}
            <div className="ui-row">
              <Button type="submit" variant="primary" disabled={busy || code.trim() === ""}>
                {busy ? t("tokens.device.lookingUp") : t("common.actions.continue")}
              </Button>
            </div>
          </form>
        </Card>
      )}
      {done === null && login !== null && <Approve login={login} onDone={setDone} />}
    </>
  );
}

function Approve({ login, onDone }: { login: DeviceLogin; onDone: (d: "approved" | "denied") => void }) {
  const { t } = useTranslation();
  const { scopes, sites, error: loadError, grant, setGrant } = useGrant();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (loadError) setError(deviceMessage(loadError));
  }, [loadError]);

  async function answer(approve: boolean) {
    setBusy(true);
    setError("");
    try {
      await approveDeviceLogin(
        login.userCode,
        approve,
        [...grant.scopes],
        grant.allSites ? null : [...grant.sites],
        Number(grant.days),
      );
      onDone(approve ? "approved" : "denied");
    } catch (err) {
      setError(deviceMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("tokens.device.login", { code: login.userCode })}>
      <form
        className="ui-stack"
        onSubmit={(e) => {
          e.preventDefault();
          void answer(true);
        }}
      >
        <dl className="ui-facts">
          <dt>{t("tokens.device.device")}</dt>
          <dd>{login.client}</dd>
          <dt>{t("tokens.device.from")}</dt>
          <dd>{login.ip}</dd>
          <dt>{t("tokens.device.asked")}</dt>
          <dd>
            <RelTime iso={login.createdAt} />
          </dd>
        </dl>
        <Notice tone="warn" title={t("tokens.device.warnTitle")}>
          {t("tokens.device.warnText")}
        </Notice>
        <GrantFields scopes={loadError ? [] : scopes} sites={sites} grant={grant} onChange={setGrant} />
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !grantReady(grant)}>
            {t("tokens.device.approve")}
          </Button>
          <Button type="button" disabled={busy} onClick={() => void answer(false)}>
            {t("tokens.device.deny")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
