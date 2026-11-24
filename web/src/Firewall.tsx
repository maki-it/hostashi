// SPDX-License-Identifier: AGPL-3.0-or-later

import { Plus, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { ApiError } from "./api";
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
import { Button, Card, Field, IconButton, Notice, PageHead, RelTime } from "./ui";
import { TokenAllowlistCard } from "./TokenAllowlist";

type RuleDraft = { proto: "tcp" | "udp"; ports: string; sources: string; note: string };

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
    if (!p) return "Port " + (i + 1) + ": enter a port such as 3000 or a range such as 6000-6010.";
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

export function Firewall() {
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

  const head = (
    <PageHead
      title="Firewall"
      description="Ports 80 and 443 are always open for the sites. SSH, the panel and API tokens are open to everyone until you set an allowlist."
    />
  );

  if (!fw) {
    return (
      <>
        {head}
        {error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>Try again</Button>
          </Notice>
        ) : (
          <p aria-busy="true">Loading…</p>
        )}
      </>
    );
  }

  if (!fw.enabled) {
    return (
      <>
        {head}
        <Notice tone="warn" title="Hostashi's firewall is off: the server was installed with --no-firewall.">
          Use the firewall in front of the server (for example at your provider), and let only SSH, 80, 443 and the panel port through.
          Run the installer again without --no-firewall to manage the rules here.
        </Notice>
        <TokenAllowlistCard clientIp={fw.clientIp} />
      </>
    );
  }

  return (
    <>
      {head}
      {error && <Notice tone="err" title={error} />}
      <Editor key={JSON.stringify(fw.state) + (fw.pending?.deadline ?? "")} fw={fw} reload={load} flash={flash} setFlash={setFlash} />
      <TokenAllowlistCard clientIp={fw.clientIp} />
    </>
  );
}

type Waiting = { id: string; deadline: string; ssh: boolean };

type EditorProps = { fw: FirewallStatus; reload: () => Promise<void>; flash: Flash; setFlash: (f: Flash) => void };

function Editor({ fw, reload, flash, setFlash }: EditorProps) {
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
        setFlash({ alert: false, text: "The new rules stay." });
        await reload();
      } catch (err) {
        setWaiting(null);
        setFlash({ alert: true, text: firewallMessage(err) });
        await reload();
      } finally {
        setBusy(false);
      }
    },
    [reload, setFlash],
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
        setFlash({ alert: false, text: "The rules were applied." });
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

  return (
    <form className="ui-stack" onSubmit={(e) => void save(e)}>
      <Notice tone="info" title={"Your address as the panel sees it: " + fw.clientIp}>
        Every change rolls back after {fw.confirmWithin} seconds unless it is confirmed. A firewall in front of the server (for example at
        your provider) still applies: open a port there too.
      </Notice>
      {fw.rolledBack && !waiting && (
        <Notice tone="warn" title="The last change was not confirmed in time and was rolled back.">
          Rolled back <RelTime iso={fw.rolledBack} />.
        </Notice>
      )}
      {flash && <Notice tone={flash.alert ? "err" : "ok"} title={flash.text} />}

      {waiting && (
        <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="fw-wait">
          <h2 id="fw-wait" className="ui-flush" ref={waitRef} tabIndex={-1}>
            Waiting for confirmation
          </h2>
          {waiting.ssh ? (
            <>
              <p className="ui-flush">
                The SSH allowlist changed. Open a new terminal now and log in over SSH. Keep the session you have open until the test
                works.
              </p>
              <p className="ui-flush">
                The rules roll back in <strong>{now} seconds</strong> unless you keep them.
              </p>
              <div className="ui-row">
                <Button variant="primary" disabled={busy} onClick={() => void confirm(waiting.id)}>
                  SSH works, keep the rules
                </Button>
              </div>
            </>
          ) : (
            <p className="ui-flush" aria-busy="true">
              Checking that the panel still answers…
            </p>
          )}
        </section>
      )}

      {foreignPending && (
        <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="fw-foreign">
          <h2 id="fw-foreign" className="ui-flush">
            A change is waiting
          </h2>
          <p className="ui-flush">
            Another browser made a change that waits for its confirmation. It rolls back in {now} seconds unless that browser keeps it.
          </p>
        </section>
      )}

      <Card title="Allowlists">
        <div className="ui-stack">
          <p className="ui-hint ui-flush">
            One IP address or CIDR range per line, IPv4 or IPv6. An empty list leaves the port open to everyone. A home connection often
            gets a new address overnight; a VPN or a fixed office address works better.
          </p>
          <div className="ui-allow">
            <div className="ui-stack-tight">
              <Field label="SSH">
                <textarea className="ui-input ui-textarea ui-mono" value={ssh} onChange={(e) => setSSH(e.target.value)} rows={3} spellCheck={false} />
              </Field>
              <div className="ui-row">
                <Button icon={<Plus size={16} aria-hidden="true" />} onClick={() => addMine(ssh, setSSH)}>
                  Add my address to SSH
                </Button>
              </div>
            </div>
            <div className="ui-stack-tight">
              <Field label="Panel">
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
                  Add my address to the panel
                </Button>
              </div>
            </div>
          </div>
        </div>
      </Card>

      <Card
        title="Opened ports"
        actions={
          <Button
            icon={<Plus size={16} aria-hidden="true" />}
            onClick={() => setRules([...rules, { proto: "tcp", ports: "", sources: "", note: "" }])}
          >
            Add port
          </Button>
        }
      >
        {rules.length === 0 ? (
          <p className="ui-hint ui-flush">No other port is open.</p>
        ) : (
          <ul className="ui-port-rows">
            {rules.map((r, i) => (
              <li key={i} aria-label={"Port " + (i + 1)} className="ui-port-row">
                <Field label="Protocol">
                  <select className="ui-select" value={r.proto} onChange={(e) => setRule(i, { proto: e.target.value as "tcp" | "udp" })}>
                    <option value="tcp">TCP</option>
                    <option value="udp">UDP</option>
                  </select>
                </Field>
                <Field label="Port or range">
                  <input
                    className="ui-input ui-mono"
                    value={r.ports}
                    onChange={(e) => setRule(i, { ports: e.target.value })}
                    placeholder="3000 or 6000-6010"
                  />
                </Field>
                <Field label="Only from (optional)">
                  <input
                    className="ui-input ui-mono"
                    value={r.sources}
                    onChange={(e) => setRule(i, { sources: e.target.value })}
                    placeholder="Everyone"
                    spellCheck={false}
                  />
                </Field>
                <Field label="Note">
                  <input className="ui-input" value={r.note} onChange={(e) => setRule(i, { note: e.target.value })} maxLength={64} />
                </Field>
                <IconButton
                  label={"Remove port " + (i + 1)}
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
            <input type="checkbox" checked={otherWay} onChange={(e) => setOtherWay(e.target.checked)} />I reach the server another way (VPN,
            another address, the provider&apos;s console)
          </label>
        </Notice>
      )}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy || !!waiting || foreignPending}>
          {busy ? "Applying…" : "Apply rules"}
        </Button>
      </div>
    </form>
  );
}
