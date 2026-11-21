// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, ArrowsCounterClockwise, Play, Stop, type Icon } from "@phosphor-icons/react";
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
import { Badge, Button, Card, Field, IconButton, Notice, PageHead, RelTime, RowActions, Table, type Column } from "./ui";

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

type Pending = { row: ServiceRow; action: ServiceAction };

/**
 * Services lists the system services of the server and of each site
 * (ADR-0052) with their state and automatic restarts, and runs the few
 * actions the catalog allows. With site set it shows only that site's
 * services, for the site's overview tab.
 */
export function Services({ site }: { site?: string }) {
  const [rows, setRows] = useState<ServiceRow[] | null>(null);
  const [events, setEvents] = useState<ServiceEvent[]>([]);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [busy, setBusy] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);

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
      sortValue: (r) => serviceName(r.id),
      cell: (r) => (
        <div className="ui-stack-tight">
          <strong>{serviceName(r.id)}</strong>
          <code className="ui-hint">{r.unit}</code>
        </div>
      ),
    },
    ...(site
      ? []
      : [
          {
            key: "where",
            header: "Where",
            sortValue: (r: ServiceRow) => (r.site ? "1" + r.site : "0"),
            cell: (r: ServiceRow) =>
              r.site ? (
                <a className="ui-nowrap" href={formatHash({ page: "sites", site: { name: r.site, tab: "overview" } })}>
                  Site {r.site}
                </a>
              ) : (
                "Server"
              ),
          },
        ]),
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
          <span className="ui-hint">Managed by Hostashi</span>
        ) : (
          <RowActions>
            {r.actions.map((a) => {
              const I = icons[a];
              return (
                <IconButton
                  key={a}
                  label={`${actionWord(a)} ${serviceName(r.id)}${r.site ? " of " + r.site : ""}`}
                  icon={<I size={16} aria-hidden="true" />}
                  danger={a === "stop"}
                  disabled={busy !== "" || !usable(r, a)}
                  onClick={() => ask(r, a)}
                />
              );
            })}
          </RowActions>
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
        <Card padded={false} {...(site ? { title: "Services" } : {})}>
          <Table label="Services" columns={columns} rows={rows} rowKey={rowKey} empty="No services." />
        </Card>
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

function Events({ events }: { events: ServiceEvent[] }) {
  return (
    <Card title="Recent events" padded={events.length === 0}>
      {events.length === 0 ? (
        <p className="ui-flush ui-hint">No restarts, failures or actions in the last 30 days.</p>
      ) : (
        <ul className="ui-attention-list" aria-label="Recent events">
          {events.map((e, i) => (
            <li key={e.at + e.service + (e.site ?? "") + i}>
              <Badge tone={e.kind === "failed" ? "err" : e.kind === "crash-restart" ? "warn" : "off"}>
                {e.kind === "failed" ? "Failed" : e.kind === "crash-restart" ? "Restart" : e.kind === "started" ? "Started" : "Action"}
              </Badge>
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
