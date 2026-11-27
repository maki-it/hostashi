// SPDX-License-Identifier: AGPL-3.0-or-later

import { Plus, Prohibit } from "@phosphor-icons/react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { formatHash } from "./routes";
import { GrantFields, grantReady, useGrant } from "./TokenGrant";
import { createToken, listTokens, revokeToken, tokenMessage, tokenState, type APIToken, type CreatedToken, type TokenState } from "./tokens";
import { Badge, Button, Card, Field, IconButton, Notice, RelTime, RowActions, Table, type Column, type Tone } from "./ui";

const states: Record<TokenState, { label: string; tone: Tone }> = {
  active: { label: "Active", tone: "ok" },
  expires_soon: { label: "Expires soon", tone: "warn" },
  expired: { label: "Expired", tone: "off" },
  revoked: { label: "Revoked", tone: "off" },
};

// TokensCard lists the account's API tokens and creates and revokes them
// (ADR-0018). A token never gets more than its owner: only the scopes of the
// role and only sites the account reaches (ADR-0035).
export function TokensCard({ admin = false }: { admin?: boolean }) {
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
      header: "Name",
      cell: (t) => (
        <span className="ui-cell-main">
          <strong>{t.name}</strong>
          <span>{t.prefix}…</span>
        </span>
      ),
    },
    { key: "state", header: "State", cell: (t) => <Badge tone={states[tokenState(t, now)].tone}>{states[tokenState(t, now)].label}</Badge> },
    { key: "scopes", header: "Scopes", cell: (t) => <ScopeList scopes={t.scopes} /> },
    { key: "sites", header: "Sites", cell: (t) => (t.sites === null ? "All" : t.sites.join(", ")) },
    {
      key: "expires",
      header: "Expires",
      cell: (t) => (t.expiresAt === null ? <Badge tone="warn">Never</Badge> : <RelTime iso={t.expiresAt} />),
    },
    {
      key: "used",
      header: "Last used",
      cell: (t) =>
        t.lastUsedAt ? (
          <span className="ui-cell-main">
            <RelTime iso={t.lastUsedAt} />
            {t.lastUsedIp && <span className="ui-hint">{t.lastUsedIp}</span>}
          </span>
        ) : (
          <span className="ui-hint">Never</span>
        ),
    },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (t) =>
        tokenState(t, now) === "revoked" ? null : (
          <RowActions>
            <IconButton
              label={`Revoke ${t.name}`}
              text="Revoke" icon={<Prohibit size={16} />}
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
        title="API tokens"
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
              New token
            </Button>
          )
        }
      >
        <div className="ui-card-body ui-stack">
          <p className="ui-flush ui-hint">
            Tokens let scripts, monitoring and the hostashi command line use the API as you. Each has its own scopes, sites and lifetime, and
            you can revoke each one on its own.
            {admin && (
              <>
                {" "}
                Where tokens are accepted from is set under <a href={formatHash({ page: "firewall" })}>Firewall</a>.
              </>
            )}
          </p>
          {revoking && (
            <Notice tone="warn" title={`Revoke the token “${revoking.name}”?`}>
              <p className="ui-flush">
                Everything that uses <code>{revoking.prefix}…</code> stops working at once. This cannot be undone.
              </p>
              <div className="ui-row">
                <Button variant="danger" disabled={busy} onClick={() => void revoke(revoking)}>
                  Revoke token
                </Button>
                <Button disabled={busy} onClick={() => setRevoking(null)}>
                  Cancel
                </Button>
              </div>
            </Notice>
          )}
          {error && <Notice tone="err" title={error} />}
          {tokens === null && !error && <p aria-busy="true">Loading…</p>}
          {old.length > 0 && (
            <label className="ui-check">
              <input type="checkbox" checked={showOld} onChange={(e) => setShowOld(e.target.checked)} />
              Show revoked and expired tokens ({old.length})
            </label>
          )}
        </div>
        {tokens !== null && (
          <Table
            label="API tokens"
            columns={columns}
            rows={shown}
            rowKey={(t) => String(t.id)}
            empty={old.length > 0 ? "No working tokens." : "No tokens yet. Create one with New token."}
          />
        )}
      </Card>
    </>
  );
}

const scopesShown = 3;

function ScopeList({ scopes }: { scopes: string[] }) {
  const rest = scopes.length - scopesShown;
  return (
    <span title={scopes.join(", ")}>
      {scopes.slice(0, scopesShown).map((s, i) => (
        <span key={s}>
          {i > 0 && ", "}
          <code>{s}</code>
        </span>
      ))}
      {rest > 0 && <span className="ui-hint"> and {rest} more</span>}
    </span>
  );
}

function NewToken({ onCancel, onCreated }: { onCancel: () => void; onCreated: (t: CreatedToken) => void }) {
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
    <Card title="New API token">
      <form className="ui-stack" onSubmit={(e) => void submit(e)}>
        <div className="ui-narrow">
          <Field label="Name" hint="What uses it, for example “Uptime monitoring” or “Deploy from CI”. Up to 64 characters.">
            <input className="ui-input" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} autoComplete="off" autoFocus />
          </Field>
        </div>
        {loadError !== null && <Notice tone="err" title={tokenMessage(loadError)} />}
        <GrantFields scopes={loadError ? [] : scopes} sites={sites} grant={grant} onChange={setGrant} allowUnlimited />
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !nameOK || !grantReady(grant)}>
            {busy ? "Creating…" : "Create token"}
          </Button>
          <Button disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
        </div>
      </form>
    </Card>
  );
}

// SecretNotice shows a new token once. The panel stores only its hash and
// cannot show it again; closing the notice drops it from memory.
function SecretNotice({ token, onClose }: { token: CreatedToken; onClose: () => void }) {
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
      setCopied("Copied.");
    } catch {
      setCopied("Copying is not possible here. Select the token and copy it by hand.");
    }
  }

  return (
    <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="token-secret">
      <h2 id="token-secret" ref={heading} tabIndex={-1}>
        Save this token now
      </h2>
      <p className="ui-flush">
        Hostashi stores only a hash of “{token.name}” and cannot show it again. If it is lost, revoke it and create a new one.
      </p>
      <Field label="Token">
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
        <Button onClick={() => void copy()}>Copy token</Button>
        <Button variant="primary" onClick={onClose}>
          I have saved it
        </Button>
      </div>
      {copied && <Notice tone="ok" title={copied} />}
      {token.neverExpires && <Notice tone="warn" title="This token never expires. Revoke it when the job that uses it ends." />}
      {highRisk.length > 0 && (
        <Notice tone="warn" title="This token has high-risk scopes">
          {highRisk.map((s, i) => (
            <span key={s}>
              {i > 0 && ", "}
              <code>{s}</code>
            </span>
          ))}
          . Keep it where only you can read it.
        </Notice>
      )}
      <p className="ui-flush ui-hint">
        Send it as <code>Authorization: Bearer …</code>, or give it to the command line with{" "}
        <code>hostashi login -token {window.location.origin}</code> on stdin.
      </p>
    </section>
  );
}
