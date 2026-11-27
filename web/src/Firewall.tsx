// SPDX-License-Identifier: AGPL-3.0-or-later

import { Plus, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { ApiError } from "./api";
import i18n from "./i18n";
import {
  confirmFirewall,
  firewallMessage,
  getFirewall,
  parseList,
  sameList,
  setFirewall,
  type Firewall as FirewallStatus,
  type FirewallRule,
  type FirewallState,
} from "./firewall";
import { Button, Card, Field, IconButton, Notice, PageHead, RelTime, tabPanelProps, Tabs, UnsavedNote } from "./ui";
import { BlockedAddressesCard } from "./BlockedAddresses";
import { TokenAllowlistCard } from "./TokenAllowlist";

type RuleDraft = { proto: "tcp" | "udp"; ports: string; sources: string; note: string };

// The protocols are named the same in every language.
const protocols = ["tcp", "udp"] as const;

const toDraft = (r: FirewallRule): RuleDraft => ({
  proto: r.proto,
  ports: r.to && r.to !== r.from ? r.from + "-" + r.to : String(r.from),
  sources: (r.sources ?? []).join(", "),
  note: r.note ?? "",
});

// parsePorts reads "3000" or "6000-6010"; null when it is neither.
function parsePorts(s: string): { from: number; to?: number } | null {
  const m = /^\s*(\d{1,5})\s*(?:-\s*(\d{1,5})\s*)?$/.exec(s);
  if (!m) return null;
  const from = Number(m[1]);
  const to = m[2] ? Number(m[2]) : undefined;
  return to && to !== from ? { from, to } : { from };
}

function toState(ssh: string, panel: string, rules: RuleDraft[]): FirewallState | string {
  const open: FirewallRule[] = [];
  for (const [i, r] of rules.entries()) {
    const p = parsePorts(r.ports);
    if (!p) return i18n.t("server.firewall.portError", { n: i + 1 });
    const rule: FirewallRule = { proto: r.proto, ...p };
    const sources = parseList(r.sources);
    if (sources.length) rule.sources = sources;
    if (r.note.trim()) rule.note = r.note.trim();
    open.push(rule);
  }
  return { open, sshAllow: parseList(ssh), panelAllow: parseList(panel) };
}

const secondsLeft = (deadline: string) => Math.max(0, Math.ceil((new Date(deadline).getTime() - Date.now()) / 1000));

// Firewall edits the panel's part of the firewall: the allowlists for SSH
// and the panel, and the opened ports. Ports 80 and 443 stay open for the
// sites. A change rolls back unless it is confirmed in time: the browser
// confirms by itself when only the panel's access changed, and asks the
// admin to test SSH first when the SSH allowlist changed.
type Flash = { alert: boolean; text: string } | null;

// The page has two tabs: the rules of the firewall, and the addresses
// fail2ban blocks. The tab is part of the address (?tab=blocked), so Health
// can link to the blocked addresses.
type FirewallTab = "rules" | "blocked";


type FirewallProps = { query: URLSearchParams; onQuery: (q: URLSearchParams) => void };

export function Firewall({ query, onQuery }: FirewallProps) {
  const { t } = useTranslation();
  const tab: FirewallTab = query.get("tab") === "blocked" ? "blocked" : "rules";
  const firewallTabs = [
    { id: "rules", label: t("server.firewall.tabs.rules") },
    { id: "blocked", label: t("server.firewall.tabs.blocked") },
  ] as const;
  const head = <PageHead title={t("server.firewall.title")} description={t("server.firewall.description")} />;

  return (
    <>
      {head}
      <Tabs
        label={t("server.firewall.title")}
        tabs={firewallTabs}
        active={tab}
        onChange={(t) => onQuery(new URLSearchParams(t === "blocked" ? { tab: "blocked" } : {}))}
        idPrefix="firewall"
      />
      <div {...tabPanelProps("firewall", tab)} className="ui-stack">
        {/* The rules stay mounted on the other tab: a change waiting for
            its confirmation keeps its countdown and confirms in time. */}
        <div className="ui-stack" hidden={tab !== "rules"}>
          <FirewallRules />
        </div>
        {tab === "blocked" && <BlockedAddressesCard query={query} onQuery={onQuery} />}
      </div>
    </>
  );
}

function FirewallRules() {
  const { t } = useTranslation();
  const [fw, setFw] = useState<FirewallStatus | null>(null);
  const [error, setError] = useState("");
  // flash outlives the editor, which starts over after every reload.
  const [flash, setFlash] = useState<Flash>(null);

  const load = useCallback(async () => {
    try {
      setFw(await getFirewall());
      setError("");
    } catch (err) {
      setError(firewallMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!fw) {
    return (
      <>
        {error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>{t("common.actions.tryAgain")}</Button>
          </Notice>
        ) : (
          <p aria-busy="true">{t("common.status.loading")}</p>
        )}
      </>
    );
  }

  if (!fw.enabled) {
    return (
      <>
        <Notice tone="warn" title={t("server.firewall.off")}>
          {t("server.firewall.offText")}
        </Notice>
        <TokenAllowlistCard clientIp={fw.clientIp} />
      </>
    );
  }

  return (
    <>
      {error && <Notice tone="err" title={error} />}
      <Editor key={JSON.stringify(fw.state) + (fw.pending?.deadline ?? "")} fw={fw} reload={load} flash={flash} setFlash={setFlash} />
      <TokenAllowlistCard clientIp={fw.clientIp} />
    </>
  );
}

type Waiting = { id: string; deadline: string; ssh: boolean };

type EditorProps = { fw: FirewallStatus; reload: () => Promise<void>; flash: Flash; setFlash: (f: Flash) => void };

function Editor({ fw, reload, flash, setFlash }: EditorProps) {
  const { t } = useTranslation();
  const [ssh, setSSH] = useState(fw.state.sshAllow.join("\n"));
  const [panel, setPanel] = useState(fw.state.panelAllow.join("\n"));
  const [rules, setRules] = useState<RuleDraft[]>(fw.state.open.map(toDraft));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [lockout, setLockout] = useState("");
  const [otherWay, setOtherWay] = useState(false);
  const [waiting, setWaiting] = useState<Waiting | null>(null);
  const [now, setNow] = useState(0);
  const waitRef = useRef<HTMLHeadingElement>(null);

  // The notice sits at the top of a long page: bring it into view, so the
  // admin sees what to do before the rules roll back.
  const waitingID = waiting?.id;
  useEffect(() => {
    if (!waitingID) return;
    waitRef.current?.focus();
    waitRef.current?.scrollIntoView?.({ block: "start" });
  }, [waitingID]);

  const deadline = waiting?.deadline ?? fw.pending?.deadline;
  useEffect(() => {
    if (!deadline) return;
    setNow(secondsLeft(deadline));
    const t = setInterval(() => {
      const left = secondsLeft(deadline);
      setNow(left);
      if (left === 0) {
        clearInterval(t);
        setWaiting(null);
        void reload();
      }
    }, 1000);
    return () => clearInterval(t);
  }, [deadline, reload]);

  const confirm = useCallback(
    async (id: string) => {
      setBusy(true);
      try {
        await confirmFirewall(id);
        setWaiting(null);
        setFlash({ alert: false, text: t("server.firewall.kept") });
        await reload();
      } catch (err) {
        setWaiting(null);
        setFlash({ alert: true, text: firewallMessage(err) });
        await reload();
      } finally {
        setBusy(false);
      }
    },
    [reload, setFlash, t],
  );

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setFlash(null);
    const state = toState(ssh, panel, rules);
    if (typeof state === "string") {
      setError(state);
      return;
    }
    setBusy(true);
    try {
      const res = await setFirewall(state, otherWay);
      setLockout("");
      setOtherWay(false);
      if (!res.pending?.id) {
        setFlash({ alert: false, text: t("server.firewall.applied") });
        await reload();
        return;
      }
      const sshChanged = !sameList(state.sshAllow, fw.state.sshAllow);
      setWaiting({ id: res.pending.id, deadline: res.pending.deadline, ssh: sshChanged });
      // A new request through the new rules: if it arrives, the panel is
      // still reachable. SSH cannot be tested from here, so the admin
      // confirms that change after testing it.
      if (!sshChanged) {
        setBusy(false);
        await confirm(res.pending.id);
        return;
      }
    } catch (err) {
      if (err instanceof ApiError && (err.code === "lockout_panel" || err.code === "lockout_ssh")) {
        setLockout(firewallMessage(err));
      } else {
        setError(firewallMessage(err));
      }
    } finally {
      setBusy(false);
    }
  };

  const addMine = (text: string, set: (v: string) => void) => {
    const list = parseList(text);
    if (!list.includes(fw.clientIp)) set([...list, fw.clientIp].join("\n"));
  };

  const setRule = (i: number, patch: Partial<RuleDraft>) => setRules(rules.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const foreignPending = !!fw.pending && !waiting;
  const dirty =
    ssh !== fw.state.sshAllow.join("\n") || panel !== fw.state.panelAllow.join("\n") || JSON.stringify(rules) !== JSON.stringify(fw.state.open.map(toDraft));

  return (
    <form className="ui-stack" onSubmit={(e) => void save(e)}>
      <Notice tone="info" title={t("server.firewall.yourAddress", { ip: fw.clientIp })}>
        {t("server.firewall.rollsBack", { seconds: fw.confirmWithin })}
      </Notice>
      {fw.rolledBack && !waiting && (
        <Notice tone="warn" title={t("server.firewall.rolledBack")}>
          <Trans i18nKey="server.firewall.rolledBackAt" components={{ time: <RelTime iso={fw.rolledBack} /> }} />
        </Notice>
      )}
      {flash && <Notice tone={flash.alert ? "err" : "ok"} title={flash.text} />}

      {waiting && (
        <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="fw-wait">
          <h2 id="fw-wait" className="ui-flush" ref={waitRef} tabIndex={-1}>
            {t("server.firewall.waiting.title")}
          </h2>
          {waiting.ssh ? (
            <>
              <p className="ui-flush">{t("server.firewall.waiting.ssh")}</p>
              <p className="ui-flush">
                <Trans i18nKey="server.firewall.waiting.countdown" values={{ seconds: now }} components={{ strong: <strong /> }} />
              </p>
              <div className="ui-row">
                <Button variant="primary" disabled={busy} onClick={() => void confirm(waiting.id)}>
                  {t("server.firewall.waiting.keep")}
                </Button>
              </div>
            </>
          ) : (
            <p className="ui-flush" aria-busy="true">
              {t("server.firewall.waiting.checking")}
            </p>
          )}
        </section>
      )}

      {foreignPending && (
        <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="fw-foreign">
          <h2 id="fw-foreign" className="ui-flush">
            {t("server.firewall.foreign.title")}
          </h2>
          <p className="ui-flush">{t("server.firewall.foreign.text", { seconds: now })}</p>
        </section>
      )}

      <Card title={t("server.firewall.allow.title")}>
        <div className="ui-stack">
          <p className="ui-hint ui-flush">{t("server.firewall.allow.hint")}</p>
          <div className="ui-allow">
            <div className="ui-stack-tight">
              <Field label={t("server.firewall.allow.ssh")}>
                <textarea className="ui-input ui-textarea ui-mono" value={ssh} onChange={(e) => setSSH(e.target.value)} rows={3} spellCheck={false} />
              </Field>
              <div className="ui-row">
                <Button icon={<Plus size={16} aria-hidden="true" />} onClick={() => addMine(ssh, setSSH)}>
                  {t("server.firewall.allow.addSSH")}
                </Button>
              </div>
            </div>
            <div className="ui-stack-tight">
              <Field label={t("server.firewall.allow.panel")}>
                <textarea
                  className="ui-input ui-textarea ui-mono"
                  value={panel}
                  onChange={(e) => setPanel(e.target.value)}
                  rows={3}
                  spellCheck={false}
                />
              </Field>
              <div className="ui-row">
                <Button icon={<Plus size={16} aria-hidden="true" />} onClick={() => addMine(panel, setPanel)}>
                  {t("server.firewall.allow.addPanel")}
                </Button>
              </div>
            </div>
          </div>
        </div>
      </Card>

      <Card
        title={t("server.firewall.ports.title")}
        actions={
          <Button
            icon={<Plus size={16} aria-hidden="true" />}
            onClick={() => setRules([...rules, { proto: "tcp", ports: "", sources: "", note: "" }])}
          >
            {t("server.firewall.ports.add")}
          </Button>
        }
      >
        {rules.length === 0 ? (
          <p className="ui-hint ui-flush">{t("server.firewall.ports.none")}</p>
        ) : (
          <ul className="ui-port-rows">
            {rules.map((r, i) => (
              <li key={i} aria-label={t("server.firewall.ports.row", { n: i + 1 })} className="ui-port-row">
                <Field label={t("server.firewall.ports.protocol")}>
                  <select className="ui-select" value={r.proto} onChange={(e) => setRule(i, { proto: e.target.value as "tcp" | "udp" })}>
                    {protocols.map((p) => (
                      <option key={p} value={p}>
                        {p.toUpperCase()}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label={t("server.firewall.ports.ports")}>
                  <input
                    className="ui-input ui-mono"
                    value={r.ports}
                    onChange={(e) => setRule(i, { ports: e.target.value })}
                    placeholder={t("server.firewall.ports.portsPlaceholder")}
                  />
                </Field>
                <Field label={t("server.firewall.ports.sources")}>
                  <input
                    className="ui-input ui-mono"
                    value={r.sources}
                    onChange={(e) => setRule(i, { sources: e.target.value })}
                    placeholder={t("server.firewall.ports.everyone")}
                    spellCheck={false}
                  />
                </Field>
                <Field label={t("server.firewall.ports.note")}>
                  <input className="ui-input" value={r.note} onChange={(e) => setRule(i, { note: e.target.value })} maxLength={64} />
                </Field>
                <IconButton
                  label={t("server.firewall.ports.removeLabel", { n: i + 1 })}
                  text={t("server.firewall.ports.remove")}
                  icon={<Trash size={16} />}
                  danger
                  onClick={() => setRules(rules.filter((_, j) => j !== i))}
                />
              </li>
            ))}
          </ul>
        )}
      </Card>

      {error && <Notice tone="err" title={error} />}
      {lockout && (
        <Notice tone="err" title={lockout}>
          <label className="ui-check">
            <input type="checkbox" checked={otherWay} onChange={(e) => setOtherWay(e.target.checked)} />
            {t("server.firewall.otherWay")}
          </label>
        </Notice>
      )}
      {/* One bar for both cards above: the allowlists and the ports apply together. */}
      <div className="ui-card ui-card-body ui-apply-bar">
        <Button type="submit" variant="primary" disabled={busy || !!waiting || foreignPending || !dirty}>
          {busy ? t("server.firewall.applying") : t("server.firewall.apply")}
        </Button>
        <UnsavedNote dirty={dirty} />
        <span className="ui-hint">{t("server.firewall.applyHint", { seconds: fw.confirmWithin })}</span>
      </div>
    </form>
  );
}
