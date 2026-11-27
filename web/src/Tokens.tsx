// SPDX-License-Identifier: AGPL-3.0-or-later

import { Plus, Prohibit } from "@phosphor-icons/react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatHash } from "./routes";
import { GrantFields, grantReady, useGrant } from "./TokenGrant";
import { createToken, listTokens, revokeToken, tokenMessage, tokenState, type APIToken, type CreatedToken, type TokenState } from "./tokens";
import { Badge, Button, Card, Field, IconButton, Notice, RelTime, RowActions, Table, type Column, type Tone } from "./ui";

// The label of a state is tokens.states.<state>.
const tones: Record<TokenState, Tone> = { active: "ok", expires_soon: "warn", expired: "off", revoked: "off" };

// TokensCard lists the account's API tokens and creates and revokes them
// (ADR-0018). A token never gets more than its owner: only the scopes of the
// role and only sites the account reaches (ADR-0035).
export function TokensCard({ admin = false }: { admin?: boolean }) {
  const { t: tr } = useTranslation();
  const [tokens, setTokens] = useState<APIToken[] | null>(null);
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [revoking, setRevoking] = useState<APIToken | null>(null);
  const [showOld, setShowOld] = useState(false);
  const [busy, setBusy] = useState(false);

  async function load() {
    try {
      setTokens(await listTokens());
    } catch (err) {
      setError(tokenMessage(err));
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function revoke(t: APIToken) {
    setBusy(true);
    setError("");
    try {
      await revokeToken(t.id);
      setRevoking(null);
      await load();
    } catch (err) {
      setError(tokenMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const now = Date.now();
  const all = tokens ?? [];
  const old = all.filter((t) => tokenState(t, now) === "revoked" || tokenState(t, now) === "expired");
  const shown = showOld ? all : all.filter((t) => !old.includes(t));

  const columns: Column<APIToken>[] = [
    {
      key: "name",
      header: tr("tokens.columns.name"),
      cell: (t) => (
        <span className="ui-cell-main">
          <strong>{t.name}</strong>
          <span>{t.prefix}…</span>
        </span>
      ),
    },
    { key: "state", header: tr("tokens.columns.state"), cell: (t) => <Badge tone={tones[tokenState(t, now)]}>{tr(`tokens.states.${tokenState(t, now)}`)}</Badge> },
    { key: "scopes", header: tr("tokens.columns.scopes"), cell: (t) => <ScopeList scopes={t.scopes} /> },
    { key: "sites", header: tr("tokens.columns.sites"), cell: (t) => (t.sites === null ? tr("tokens.allSites") : t.sites.join(", ")) },
    {
      key: "expires",
      header: tr("tokens.columns.expires"),
      cell: (t) => (t.expiresAt === null ? <Badge tone="warn">{tr("common.status.never")}</Badge> : <RelTime iso={t.expiresAt} />),
    },
    {
      key: "used",
      header: tr("tokens.columns.used"),
      cell: (t) =>
        t.lastUsedAt ? (
          <span className="ui-cell-main">
            <RelTime iso={t.lastUsedAt} />
            {t.lastUsedIp && <span className="ui-hint">{t.lastUsedIp}</span>}
          </span>
        ) : (
          <span className="ui-hint">{tr("common.status.never")}</span>
        ),
    },
    {
      key: "actions",
      header: tr("tokens.columns.actions"),
      srHeader: true,
      cell: (t) =>
        tokenState(t, now) === "revoked" ? null : (
          <RowActions>
            <IconButton
              label={tr("tokens.revokeLabel", { name: t.name })}
              text={tr("tokens.revoke")}
              icon={<Prohibit size={16} />}
              danger
              disabled={busy}
              onClick={() => {
                setCreated(null);
                setRevoking(t);
              }}
            />
          </RowActions>
        ),
    },
  ];

  return (
    <>
      {created && <SecretNotice token={created} onClose={() => setCreated(null)} />}
      {creating && (
        <NewToken
          onCancel={() => setCreating(false)}
          onCreated={(t) => {
            setCreating(false);
            setCreated(t);
            void load();
          }}
        />
      )}
      <Card
        title={tr("tokens.title")}
        padded={false}
        actions={
          !creating && (
            <Button
              variant="primary"
              icon={<Plus size={16} />}
              onClick={() => {
                setCreated(null);
                setRevoking(null);
                setCreating(true);
              }}
            >
              {tr("tokens.new")}
            </Button>
          )
        }
      >
        <div className="ui-card-body ui-stack">
          <p className="ui-flush ui-hint">
            {tr("tokens.intro")}
            {admin && (
              <>
                {" "}
                <Trans i18nKey="tokens.introAdmin" components={{ a: <a href={formatHash({ page: "firewall" })} /> }} />
              </>
            )}
          </p>
          {revoking && (
            <Notice tone="warn" title={tr("tokens.revokeTitle", { name: revoking.name })}>
              <p className="ui-flush">
                <Trans i18nKey="tokens.revokeText" values={{ prefix: revoking.prefix }} components={{ code: <code /> }} />
              </p>
              <div className="ui-row">
                <Button variant="danger" disabled={busy} onClick={() => void revoke(revoking)}>
                  {tr("tokens.revokeSubmit")}
                </Button>
                <Button disabled={busy} onClick={() => setRevoking(null)}>
                  {tr("common.actions.cancel")}
                </Button>
              </div>
            </Notice>
          )}
          {error && <Notice tone="err" title={error} />}
          {tokens === null && !error && <p aria-busy="true">{tr("common.status.loading")}</p>}
          {old.length > 0 && (
            <label className="ui-check">
              <input type="checkbox" checked={showOld} onChange={(e) => setShowOld(e.target.checked)} />
              {tr("tokens.showOld", { count: old.length })}
            </label>
          )}
        </div>
        {tokens !== null && (
          <Table
            label={tr("tokens.title")}
            columns={columns}
            rows={shown}
            rowKey={(t) => String(t.id)}
            empty={old.length > 0 ? tr("tokens.noWorking") : tr("tokens.none")}
          />
        )}
      </Card>
    </>
  );
}

const scopesShown = 3;

function ScopeList({ scopes }: { scopes: string[] }) {
  const { t } = useTranslation();
  const rest = scopes.length - scopesShown;
  return (
    <span title={scopes.join(", ")}>
      {scopes.slice(0, scopesShown).map((s, i) => (
        <span key={s}>
          {i > 0 && ", "}
          <code>{s}</code>
        </span>
      ))}
      {rest > 0 && <span className="ui-hint"> {t("common.andMore", { count: rest })}</span>}
    </span>
  );
}

function NewToken({ onCancel, onCreated }: { onCancel: () => void; onCreated: (t: CreatedToken) => void }) {
  const { t } = useTranslation();
  const { scopes, sites, error: loadError, grant, setGrant } = useGrant("90");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      onCreated(
        await createToken({
          name: name.trim(),
          scopes: [...grant.scopes],
          sites: grant.allSites ? null : [...grant.sites],
          expiresInDays: Number(grant.days),
          unlimited: grant.unlimited,
        }),
      );
    } catch (err) {
      setError(tokenMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const nameOK = name.trim().length >= 1 && name.trim().length <= 64;

  return (
    <Card title={t("tokens.create.title")}>
      <form className="ui-stack" onSubmit={(e) => void submit(e)}>
        <div className="ui-narrow">
          <Field label={t("tokens.create.name")} hint={t("tokens.create.nameHint")}>
            <input className="ui-input" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} autoComplete="off" autoFocus />
          </Field>
        </div>
        {loadError !== null && <Notice tone="err" title={tokenMessage(loadError)} />}
        <GrantFields scopes={loadError ? [] : scopes} sites={sites} grant={grant} onChange={setGrant} allowUnlimited />
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !nameOK || !grantReady(grant)}>
            {busy ? t("tokens.create.creating") : t("tokens.create.submit")}
          </Button>
          <Button disabled={busy} onClick={onCancel}>
            {t("common.actions.cancel")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

// SecretNotice shows a new token once. The panel stores only its hash and
// cannot show it again; closing the notice drops it from memory.
function SecretNotice({ token, onClose }: { token: CreatedToken; onClose: () => void }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const highRisk = token.warnings.filter((w) => w.startsWith("high_risk_scope: ")).map((w) => w.slice("high_risk_scope: ".length));

  useEffect(() => {
    heading.current?.scrollIntoView?.({ block: "start" });
    heading.current?.focus();
  }, [token]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(token.token);
      setCopied(t("common.status.copied"));
    } catch {
      setCopied(t("tokens.secret.copyFailed"));
    }
  }

  return (
    <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="token-secret">
      <h2 id="token-secret" ref={heading} tabIndex={-1}>
        {t("tokens.secret.title")}
      </h2>
      <p className="ui-flush">{t("tokens.secret.text", { name: token.name })}</p>
      <Field label={t("tokens.secret.token")}>
        <input
          readOnly
          value={token.token}
          onFocus={(e) => e.currentTarget.select()}
          autoComplete="off"
          spellCheck={false}
          className="ui-input ui-mono"
        />
      </Field>
      <div className="ui-row">
        <Button onClick={() => void copy()}>{t("tokens.secret.copy")}</Button>
        <Button variant="primary" onClick={onClose}>
          {t("tokens.secret.saved")}
        </Button>
      </div>
      {copied && <Notice tone="ok" title={copied} />}
      {token.neverExpires && <Notice tone="warn" title={t("tokens.secret.neverExpires")} />}
      {highRisk.length > 0 && (
        <Notice tone="warn" title={t("tokens.secret.highRisk")}>
          {highRisk.map((s, i) => (
            <span key={s}>
              {i > 0 && ", "}
              <code>{s}</code>
            </span>
          ))}
          {t("tokens.secret.highRiskKeep")}
        </Notice>
      )}
      <p className="ui-flush ui-hint">
        <Trans i18nKey="tokens.secret.use" values={{ origin: window.location.origin }} components={{ code1: <code />, code2: <code /> }} />
      </p>
    </section>
  );
}
