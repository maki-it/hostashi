// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, ArrowsCounterClockwise, DotsThree, Play, Stop, X, type Icon } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { formatHash } from "./routes";
import {
  actionWord,
  bytes,
  eventText,
  listServiceEvents,
  listServices,
  runServiceAction,
  serviceMessage,
  serviceName,
  stateWord,
  type ServiceAction,
  type ServiceEvent,
  type ServiceRow,
} from "./services";
import { Badge, Button, Card, Field, IconButton, Menu, Notice, PageHead, RelTime, RowActions, Table, type Column, type Tone } from "./ui";

const icons: Record<ServiceAction, Icon> = { start: Play, stop: Stop, restart: ArrowClockwise, reload: ArrowsCounterClockwise };

const rowKey = (r: Pick<ServiceRow, "id" | "site">) => (r.site ? r.id + "/" + r.site : r.id);

// usable reports whether an action makes sense in the current state:
// start only when stopped, stop and reload only when running.
function usable(r: ServiceRow, a: ServiceAction): boolean {
  if (!r.installed) return false;
  const running = stateWord(r).tone === "ok";
  if (a === "start") return !running;
  if (a === "stop" || a === "reload") return running;
  return true;
}

// shown hides the action that does not fit the state: Start while the
// service runs, Stop while it does not, so the two share one place in the
// row (Kim, 2026-11-25). A service without Stop (nginx, PHP, Valkey, the
// virus scanner) shows only Restart and Reload while it runs.
function shown(r: ServiceRow, a: ServiceAction): boolean {
  const running = r.installed && stateWord(r).tone === "ok";
  if (a === "start") return !running;
  if (a === "stop") return running;
  return true;
}

type Pending = { row: ServiceRow; action: ServiceAction };

/** problem: the service failed or systemd restarted it today. */
export const problem = (r: ServiceRow) => stateWord(r).tone === "err" || r.restarts24h > 0;

type Group = "all" | "server" | "sites" | "problems";

/**
 * Services lists the system services of the server and of each site
 * (ADR-0052) with their state and automatic restarts, and runs the few
 * actions the catalog allows. With site set it shows only that site's
 * services, for the site's overview tab.
 */
export function Services({ site, id, onClose }: { site?: string; id?: string; onClose?: () => void }) {
  const [rows, setRows] = useState<ServiceRow[] | null>(null);
  const [events, setEvents] = useState<ServiceEvent[]>([]);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [busy, setBusy] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  // Until someone picks a group, the page opens on the problems when there
  // are any (C4 of the UX review), else on all services.
  const [picked, setGroup] = useState<Group | null>(null);
  const group: Group = picked ?? (rows?.some(problem) ? "problems" : "all");
  const [text, setText] = useState("");

  const load = useCallback(async () => {
    try {
      const [list, ev] = await Promise.all([listServices(site), site ? Promise.resolve([]) : listServiceEvents()]);
      setRows(list);
      setEvents(ev);
      setError("");
    } catch (err) {
      setError(serviceMessage(err));
    }
  }, [site]);

  useEffect(() => {
    void load();
    const t = window.setInterval(() => void load(), 15000);
    return () => window.clearInterval(t);
  }, [load]);

  async function run(row: ServiceRow, action: ServiceAction, confirm?: string) {
    setBusy(rowKey(row) + ":" + action);
    setDone("");
    setError("");
    try {
      await runServiceAction(row, action, confirm);
      setPending(null);
      setDone(`${actionWord(action)}: ${serviceName(row.id)}${row.site ? " of site " + row.site : ""} done.`);
      await load();
    } catch (err) {
      setError(serviceMessage(err));
    } finally {
      setBusy("");
    }
  }

  // Stopping always asks first; a catalog confirm asks to type the action.
  const ask = (row: ServiceRow, action: ServiceAction) => {
    if (action === "stop" || row.confirm?.includes(action)) setPending({ row, action });
    else void run(row, action);
  };

  const columns: Column<ServiceRow>[] = [
    {
      key: "service",
      header: "Service",
      main: true,
      sortValue: (r) => serviceName(r.id),
      cell: (r) => (
        <div className="ui-stack-tight">
          <strong>
            {serviceName(r.id)}
            {r.site && !site && (
              <>
                {" · "}
                <a href={formatHash({ page: "sites", site: { name: r.site, tab: "overview" } })}>{r.site}</a>
              </>
            )}
          </strong>
          <code className="ui-hint ui-phone-hide">{r.unit}</code>
          <span className="ui-phone-only ui-compact-meta">
            <Badge tone={stateWord(r).tone}>{stateWord(r).word}</Badge>
            {[r.restarts24h > 0 ? (r.restarts24h === 1 ? "1 restart today" : `${r.restarts24h} restarts today`) : "", r.memoryKnown && r.memoryBytes !== undefined ? bytes(r.memoryBytes) : ""]
              .filter(Boolean)
              .join(" · ")}
          </span>
        </div>
      ),
    },
    {
      key: "state",
      header: "State",
      sortValue: (r) => stateWord(r).word,
      cell: (r) => {
        const s = stateWord(r);
        return (
          <div className="ui-stack-tight">
            <Badge tone={s.tone}>{s.word}</Badge>
            {r.installed && r.active === "active" && r.activeSince && (
              <span className="ui-hint ui-nowrap">
                since <RelTime iso={r.activeSince} />
              </span>
            )}
            {r.active === "failed" && r.result && <span className="ui-hint">Result: {r.result}</span>}
          </div>
        );
      },
    },
    {
      key: "restarts",
      header: "Restarts (24 h)",
      sortValue: (r) => r.restarts24h,
      cell: (r) => (r.restarts24h > 0 ? <Badge tone="warn">{String(r.restarts24h)}</Badge> : <span className="ui-hint">0</span>),
    },
    {
      key: "memory",
      header: "Memory",
      sortValue: (r) => r.memoryBytes ?? 0,
      cell: (r) => (r.memoryKnown && r.memoryBytes !== undefined ? bytes(r.memoryBytes) : <span className="ui-hint">–</span>),
    },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (r) =>
        r.actions.length === 0 ? (
          <span className="ui-hint ui-phone-hide">Managed by Hostashi</span>
        ) : (
          <>
            <span className="ui-phone-hide">
              <RowActions>
                {r.actions.filter((a) => shown(r, a)).map((a) => {
                  const I = icons[a];
                  return (
                    <IconButton
                      key={a}
                      label={`${actionWord(a)} ${serviceName(r.id)}${r.site ? " of " + r.site : ""}`}
                      text={actionWord(a)}
                      icon={<I size={16} aria-hidden="true" />}
                      danger={a === "stop"}
                      disabled={busy !== "" || !usable(r, a)}
                      onClick={() => ask(r, a)}
                    />
                  );
                })}
              </RowActions>
            </span>
            <span className="ui-phone-only">
              <Menu
                label={`Actions for ${serviceName(r.id)}${r.site ? " of " + r.site : ""}`}
                buttonLabel={`Actions for ${serviceName(r.id)}${r.site ? " of " + r.site : ""}`}
                buttonClass="ui-icon-btn"
                button={<DotsThree size={20} aria-hidden="true" />}
                items={r.actions
                  .filter((a) => shown(r, a) && usable(r, a) && busy === "")
                  .map((a) => {
                    const I = icons[a];
                    return { label: actionWord(a), icon: <I size={16} aria-hidden="true" />, danger: a === "stop", onSelect: () => ask(r, a) };
                  })}
              />
            </span>
          </>
        ),
    },
  ];

  const body = (
    <>
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
      {pending && <ConfirmAction pending={pending} busy={busy !== ""} onRun={(c) => void run(pending.row, pending.action, c)} onCancel={() => setPending(null)} />}
      {rows === null ? (
        !error && <p aria-busy="true">Loading the services…</p>
      ) : (
        site ? (
          <Card padded={false} title="Services" id={id} actions={onClose && <IconButton label="Close" icon={<X size={16} aria-hidden="true" />} onClick={onClose} />}>
            <Table label="Services" columns={columns} rows={rows} rowKey={rowKey} empty="No services." />
          </Card>
        ) : (
          <Grouped rows={rows} columns={columns} group={group} onGroup={setGroup} text={text} onText={setText} />
        )
      )}
    </>
  );

  if (site) return body;

  return (
    <>
      <PageHead
        title="Services"
        description="The system services of the server and the sites. systemd restarts a crashed service on its own; each automatic restart is listed below."
      />
      {body}
      {rows !== null && <Events events={events} />}
    </>
  );
}

// summary says how a group of services is doing.
function summary(rows: ServiceRow[]): string {
  const bad = rows.filter(problem).length;
  if (rows.length === 0) return "";
  if (bad > 0) return `${bad} of ${rows.length} with a problem`;
  const off = rows.filter((r) => stateWord(r).tone !== "ok").length;
  return off === 0 ? `All ${rows.length} running` : `${rows.length - off} of ${rows.length} running`;
}

/**
 * Grouped shows the services of the server and of the sites apart, with a
 * filter for the ones with a problem first (C4 of the UX review).
 */
function Grouped({
  rows,
  columns,
  group,
  onGroup,
  text,
  onText,
}: {
  rows: ServiceRow[];
  columns: Column<ServiceRow>[];
  group: Group;
  onGroup: (g: Group) => void;
  text: string;
  onText: (t: string) => void;
}) {
  const q = text.trim().toLowerCase();
  const hit = (r: ServiceRow) => !q || `${serviceName(r.id)} ${r.unit} ${r.site ?? ""}`.toLowerCase().includes(q);
  const shownRows = rows.filter(hit);
  const server = shownRows.filter((r) => !r.site);
  const sites = shownRows.filter((r) => r.site);
  const problems = shownRows.filter(problem);
  const groups: { id: Group; label: string }[] = [
    { id: "all", label: "All" },
    { id: "problems", label: `Problems (${rows.filter(problem).length})` },
    { id: "sites", label: `Sites (${rows.filter((r) => r.site).length})` },
    { id: "server", label: `Server (${rows.filter((r) => !r.site).length})` },
  ];
  const table = (title: string, list: ServiceRow[], empty: string) => (
    <Card padded={false} title={title} actions={<span className="ui-hint">{summary(list)}</span>}>
      <Table label={title} columns={columns} rows={list} rowKey={rowKey} empty={empty} compact />
    </Card>
  );
  return (
    <>
      <div className="ui-filters ui-filters-bare">
        <div className="ui-seg" role="radiogroup" aria-label="Show">
          {groups.map((g) => (
            <button key={g.id} type="button" role="radio" aria-checked={group === g.id} className="ui-seg-btn" onClick={() => onGroup(g.id)}>
              {g.label}
            </button>
          ))}
        </div>
        <input className="ui-input" type="search" aria-label="Filter services" placeholder="Filter by site or service" value={text} onChange={(e) => onText(e.target.value)} />
      </div>
      {group === "problems" && table("Problems", problems, "No problems. Every service is running.")}
      {(group === "all" || group === "sites") && table("Sites", sites, q ? "No site service matches the filter." : "No site has a service of its own.")}
      {(group === "all" || group === "server") && table("Server", server, q ? "No server service matches the filter." : "No services.")}
    </>
  );
}

function ConfirmAction({ pending, busy, onRun, onCancel }: { pending: Pending; busy: boolean; onRun: (confirm?: string) => void; onCancel: () => void }) {
  const [typed, setTyped] = useState("");
  const { row, action } = pending;
  const name = serviceName(row.id);
  const phrase = `${action} ${row.id}`;
  const typedNeeded = row.confirm?.includes(action) ?? false;
  const effect =
    row.id === "mariadb"
      ? "Every site that uses a local database stops working until MariaDB runs again."
      : row.id === "fail2ban"
        ? "Brute-force attempts are not blocked while it is stopped."
        : row.id === "adminer"
          ? "Open database tool sessions end."
          : "The service stays stopped until you start it again.";

  return (
    <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="service-confirm-title">
      <h2 id="service-confirm-title" className="ui-flush">
        {actionWord(action)} {name}?
      </h2>
      <p className="ui-flush">{effect}</p>
      <form
        className="ui-stack"
        onSubmit={(e) => {
          e.preventDefault();
          onRun(typedNeeded ? typed : undefined);
        }}
      >
        {typedNeeded && (
          <div className="ui-narrow">
            <Field
              label={
                <>
                  Type <code className="ui-confirm-phrase">{phrase}</code> to confirm
                </>
              }
            >
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
        )}
        <div className="ui-row">
          <Button variant="danger" type="submit" disabled={busy || (typedNeeded && typed !== phrase)}>
            {actionWord(action)} {name}
          </Button>
          <Button disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
        </div>
      </form>
    </section>
  );
}

const eventBadges: Record<ServiceEvent["kind"], { word: string; tone: Tone }> = {
  failed: { word: "Failed", tone: "err" },
  "crash-restart": { word: "Restart", tone: "warn" },
  recovered: { word: "Running", tone: "ok" },
  started: { word: "Started", tone: "off" },
  action: { word: "Action", tone: "off" },
};

function Events({ events }: { events: ServiceEvent[] }) {
  return (
    <Card title="Recent events" padded={events.length === 0}>
      {events.length === 0 ? (
        <p className="ui-flush ui-hint">No restarts, failures or actions in the last 30 days.</p>
      ) : (
        <ul className="ui-attention-list" aria-label="Recent events">
          {events.map((e, i) => (
            <li key={e.at + e.service + (e.site ?? "") + i}>
              <Badge tone={eventBadges[e.kind].tone}>{eventBadges[e.kind].word}</Badge>
              <div className="ui-stack-tight">
                <strong>
                  {serviceName(e.service)}
                  {e.site ? " of site " + e.site : ""}
                </strong>
                <span className="ui-hint">
                  {eventText(e)}, <RelTime iso={e.at} />
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
