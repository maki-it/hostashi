// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, ArrowsClockwise, PencilSimple, Plus, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  addDNSAccount,
  badZone,
  checkDNSAccount,
  deleteDNSAccount,
  dnsAccounts,
  dnsMessage,
  dnsProviders,
  parseZones,
  providerName,
  updateDNSAccount,
  type DNSAccount,
  type DNSField,
  type DNSProvider,
} from "./dns";
import i18n from "./i18n";
import { formatHash } from "./routes";
import { Badge, Button, Card, Field, IconButton, Notice, PageHead, RelTime, RowActions, Table, type Column, type Tone } from "./ui";

export const dnsHref = (q: Record<string, string> = {}) => formatHash({ page: "dns", query: new URLSearchParams(q) });

const stateTone: Record<DNSAccount["state"], Tone> = { ok: "ok", error: "err", unchecked: "off" };

/**
 * DNSAccounts lists the DNS accounts for DNS-01 certificates (ADR-0076)
 * with check, edit and remove, and the form for a new account (new=1) or
 * a change (edit=<id>). Admins only.
 */
export function DNSAccounts({ query }: { query: URLSearchParams }) {
  const { t } = useTranslation();
  const [accounts, setAccounts] = useState<DNSAccount[] | null>(null);
  const [providers, setProviders] = useState<DNSProvider[]>([]);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [busy, setBusy] = useState(0);
  const [confirm, setConfirm] = useState<DNSAccount | null>(null);

  const load = useCallback(async () => {
    try {
      const [a, p] = await Promise.all([dnsAccounts(), dnsProviders()]);
      setAccounts(a);
      setProviders(p);
      setError("");
    } catch (err) {
      setError(dnsMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const back = (
    <a className="ui-btn" href={dnsHref()}>
      <ArrowLeft size={16} aria-hidden="true" />
      {t("dnsAccounts.allAccounts")}
    </a>
  );

  const finish = async (msg: string) => {
    setDone(msg);
    setError("");
    await load();
    window.location.hash = dnsHref();
  };

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead title={t("dnsAccounts.new.title")} description={t("dnsAccounts.new.description")} actions={back} />
        {providers.length > 0 && <AccountForm providers={providers} onSaved={(a) => finish(savedText(a, true))} />}
      </>
    );
  }

  const editing = query.get("edit") && accounts?.find((a) => String(a.id) === query.get("edit"));
  if (editing) {
    return (
      <>
        <PageHead title={t("dnsAccounts.edit.title", { label: editing.label })} actions={back} />
        <AccountForm providers={providers} account={editing} onSaved={(a) => finish(savedText(a, false))} />
      </>
    );
  }

  async function act(a: DNSAccount, what: "check" | "delete") {
    setBusy(a.id);
    setError("");
    setDone("");
    try {
      if (what === "check") {
        const r = await checkDNSAccount(a.id);
        if (r.state === "ok") setDone(t("dnsAccounts.checkOk", { label: a.label }));
        else setError(t("dnsAccounts.checkFailed", { label: a.label, reason: r.lastError ?? "" }));
      } else {
        await deleteDNSAccount(a.id);
        setDone(t("dnsAccounts.removed", { label: a.label }));
        setConfirm(null);
      }
      await load();
    } catch (err) {
      setError(dnsMessage(err));
    } finally {
      setBusy(0);
    }
  }

  const columns: Column<DNSAccount>[] = [
    { key: "label", header: t("dnsAccounts.columns.account"), sortValue: (a) => a.label, cell: (a) => <strong>{a.label}</strong> },
    { key: "provider", header: t("dnsAccounts.columns.provider"), sortValue: (a) => providerName(providers, a.provider), cell: (a) => providerName(providers, a.provider) },
    {
      key: "zones",
      header: t("dnsAccounts.columns.zones"),
      cell: (a) => (
        <span className="ui-stack-tight">
          {a.zones.map((z) => (
            <span key={z} className="ui-mono">
              {z}
            </span>
          ))}
        </span>
      ),
    },
    {
      key: "state",
      header: t("dnsAccounts.columns.state"),
      sortValue: (a) => a.state,
      cell: (a) => (
        <span>
          <Badge tone={stateTone[a.state]}>{t(`dnsAccounts.states.${a.state}`)}</Badge>
          {a.checkedAt && (
            <>
              <br />
              <span className="ui-hint">
                <Trans i18nKey="dnsAccounts.checked" components={{ time: <RelTime iso={a.checkedAt} /> }} />
              </span>
            </>
          )}
        </span>
      ),
    },
    {
      key: "actions",
      header: t("dnsAccounts.columns.actions"),
      srHeader: true,
      cell: (a) => (
        <RowActions>
          <IconButton label={t("dnsAccounts.checkLabel", { label: a.label })} text={t("dnsAccounts.check")} icon={<ArrowsClockwise size={16} />} disabled={busy !== 0} onClick={() => void act(a, "check")} />
          <IconButton
            label={t("dnsAccounts.editLabel", { label: a.label })}
            text={t("dnsAccounts.edit.short")}
            icon={<PencilSimple size={16} />}
            disabled={busy !== 0}
            onClick={() => {
              window.location.hash = dnsHref({ edit: String(a.id) });
            }}
          />
          <IconButton label={t("dnsAccounts.removeLabel", { label: a.label })} text={t("dnsAccounts.remove")} icon={<Trash size={16} />} danger disabled={busy !== 0} onClick={() => setConfirm(a)} />
        </RowActions>
      ),
    },
  ];

  const failing = (accounts ?? []).filter((a) => a.state === "error" && a.lastError);

  return (
    <>
      <PageHead
        title={t("dnsAccounts.title")}
        description={t("dnsAccounts.description")}
        actions={
          <a className="ui-btn ui-btn-primary" href={dnsHref({ new: "1" })}>
            <Plus size={16} aria-hidden="true" />
            {t("dnsAccounts.add")}
          </a>
        }
      />
      {done && <Notice tone="ok" title={done} />}
      {error && <Notice tone="err" title={error} />}
      {failing.map((a) => (
        <Notice key={a.id} tone="warn" title={`${a.label}: ${a.lastError ?? ""}`} />
      ))}
      {confirm && (
        <Card title={t("dnsAccounts.removeTitle", { label: confirm.label })}>
          <p className="ui-flush">{t("dnsAccounts.removeText")}</p>
          <div className="ui-row">
            <Button variant="danger" disabled={busy !== 0} onClick={() => void act(confirm, "delete")}>
              {t("dnsAccounts.removeSubmit")}
            </Button>
            <Button disabled={busy !== 0} onClick={() => setConfirm(null)}>
              {t("common.actions.cancel")}
            </Button>
          </div>
        </Card>
      )}
      {accounts === null ? (
        !error && <p aria-busy="true">{t("dnsAccounts.loading")}</p>
      ) : (
        <Card padded={false}>
          <Table label={t("dnsAccounts.title")} columns={columns} rows={accounts} rowKey={(a) => String(a.id)} empty={t("dnsAccounts.empty")} />
        </Card>
      )}
      <Notice tone="info" title={t("dnsAccounts.cname.title")}>
        <Trans i18nKey="dnsAccounts.cname.text" components={{ code: <code /> }} />
      </Notice>
    </>
  );
}

function savedText(a: DNSAccount, added: boolean): string {
  const vars = { label: a.label, reason: a.lastError ?? "" };
  if (a.state === "error") return added ? i18n.t("dnsAccounts.addedWithError", vars) : i18n.t("dnsAccounts.savedWithError", vars);
  return added ? i18n.t("dnsAccounts.added", vars) : i18n.t("dnsAccounts.saved", vars);
}

/** fieldLabel is the translated label of a provider field, else the API's. */
function useFieldText() {
  const { t, i18n } = useTranslation();
  return {
    label: (p: string, f: DNSField) => {
      const key = `dnsAccounts.fields.${p}.${f.name}`;
      return i18n.exists(key) ? (t as (k: string) => string)(key) : f.label;
    },
    hint: (p: DNSProvider) => {
      const key = `dnsAccounts.hints.${p.id}`;
      return i18n.exists(key) ? (t as (k: string) => string)(key) : p.hint;
    },
  };
}

// AccountForm adds an account, or changes one: label and zones, and all
// credentials at once when "replace" is on. Secrets are never shown.
function AccountForm({ providers, account, onSaved }: { providers: DNSProvider[]; account?: DNSAccount; onSaved: (a: DNSAccount) => Promise<void> }) {
  const { t } = useTranslation();
  const text = useFieldText();
  const [provider, setProvider] = useState(account?.provider ?? providers[0]?.id ?? "");
  const [label, setLabel] = useState(account?.label ?? "");
  const [zones, setZones] = useState((account?.zones ?? []).join("\n"));
  const [creds, setCreds] = useState<Record<string, string>>(account?.settings ?? {});
  const [replace, setReplace] = useState(!account);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const p = providers.find((x) => x.id === provider);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const list = parseZones(zones);
    const missing = replace ? p?.fields.find((f) => !f.optional && (creds[f.name] ?? "") === "") : undefined;
    const problem =
      (label.trim() === "" ? t("dnsAccounts.problems.label") : "") ||
      (list.length === 0 ? t("dnsAccounts.problems.zones") : "") ||
      (badZone(list) ? t("dnsAccounts.problems.zone", { zone: badZone(list) }) : "") ||
      (missing && p ? t("dnsAccounts.problems.field", { field: text.label(p.id, missing) }) : "");
    if (problem) {
      setError(problem);
      return;
    }
    // Only the provider's fields, and only those with a value.
    const sent: Record<string, string> = {};
    for (const f of p?.fields ?? []) {
      const v = creds[f.name] ?? "";
      if (v !== "") sent[f.name] = v;
    }
    setBusy(true);
    setError("");
    try {
      const saved = account
        ? await updateDNSAccount(account.id, { label: label.trim(), zones: list, ...(replace ? { credentials: sent } : {}) })
        : await addDNSAccount({ label: label.trim(), provider, zones: list, credentials: sent });
      await onSaved(saved);
    } catch (err) {
      setError(dnsMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        {!account && (
          <Field label={t("dnsAccounts.new.provider")}>
            <select
              className="ui-select ui-narrow"
              value={provider}
              onChange={(e) => {
                setProvider(e.target.value);
                setCreds({});
              }}
            >
              {providers.map((x) => (
                <option key={x.id} value={x.id}>
                  {x.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {p && replace && <Notice tone="info" title={text.hint(p)} />}
        <Field label={t("dnsAccounts.new.label")} hint={t("dnsAccounts.new.labelHint")}>
          <input className="ui-input" value={label} onChange={(e) => setLabel(e.target.value)} autoComplete="off" maxLength={64} />
        </Field>
        <Field label={t("dnsAccounts.new.zones")} hint={t("dnsAccounts.new.zonesHint")}>
          <textarea className="ui-input ui-mono" rows={3} value={zones} onChange={(e) => setZones(e.target.value)} spellCheck={false} autoCapitalize="off" />
        </Field>
        {account && (
          <label className="ui-check">
            <input type="checkbox" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
            {t("dnsAccounts.edit.replace")}
          </label>
        )}
        {p && replace && (
          <fieldset className="ui-fieldset ui-stack">
            <legend>{t("dnsAccounts.new.credentials", { provider: p.name })}</legend>
            {p.fields.map((f) => (
              <Field key={f.name} label={text.label(p.id, f) + (f.optional ? ` ${t("dnsAccounts.new.optional")}` : "")}>
                <input
                  className={f.secret ? "ui-input" : "ui-input ui-mono"}
                  type={f.secret ? "password" : "text"}
                  value={creds[f.name] ?? ""}
                  onChange={(e) => setCreds({ ...creds, [f.name]: e.target.value })}
                  autoComplete={f.secret ? "new-password" : "off"}
                  spellCheck={false}
                />
              </Field>
            ))}
          </fieldset>
        )}
        {account && !replace && <p className="ui-hint">{t("dnsAccounts.edit.keep")}</p>}
        {error && <Notice tone="err" title={error} />}
        {busy && <p aria-busy="true">{t("dnsAccounts.new.checking")}</p>}
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy}>
            {account ? t("dnsAccounts.edit.submit") : t("dnsAccounts.add")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
