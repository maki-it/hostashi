// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, ArrowsCounterClockwise, DotsThree, Play, Stop, X, type Icon } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatBytes } from "./format";
import i18n from "./i18n";
import { formatHash } from "./routes";
import {
  actionVerb,
  actionWord,
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
// ofSite names a service with its site, if it has one.
const ofSite = (r: { id: string; site?: string | undefined }) =>
  r.site ? i18n.t("server.services.ofSite", { name: serviceName(r.id), site: r.site }) : serviceName(r.id);

export function Services({ site, id, onClose }: { site?: string; id?: string; onClose?: () => void }) {
  const { t } = useTranslation();
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
      setDone(t("server.services.done", { action: actionWord(action), name: ofSite(row) }));
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
      header: t("server.services.columns.service"),
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
            {[r.restarts24h > 0 ? t("server.services.restartsToday", { count: r.restarts24h }) : "", r.memoryKnown && r.memoryBytes !== undefined ? formatBytes(r.memoryBytes) : ""]
              .filter(Boolean)
              .join(" · ")}
          </span>
        </div>
      ),
    },
    {
      key: "state",
      header: t("server.services.columns.state"),
      sortValue: (r) => stateWord(r).word,
      cell: (r) => {
        const s = stateWord(r);
        return (
          <div className="ui-stack-tight">
            <Badge tone={s.tone}>{s.word}</Badge>
            {r.installed && r.active === "active" && r.activeSince && (
              <span className="ui-hint ui-nowrap">
                <Trans i18nKey="server.services.since" components={{ time: <RelTime iso={r.activeSince} /> }} />
              </span>
            )}
            {r.active === "failed" && r.result && <span className="ui-hint">{t("server.services.result", { result: r.result })}</span>}
          </div>
        );
      },
    },
    {
      key: "restarts",
      header: t("server.services.columns.restarts"),
      sortValue: (r) => r.restarts24h,
      cell: (r) => (r.restarts24h > 0 ? <Badge tone="warn">{String(r.restarts24h)}</Badge> : <span className="ui-hint">0</span>),
    },
    {
      key: "memory",
      header: t("server.services.columns.memory"),
      sortValue: (r) => r.memoryBytes ?? 0,
      cell: (r) => (r.memoryKnown && r.memoryBytes !== undefined ? formatBytes(r.memoryBytes) : <span className="ui-hint">–</span>),
    },
    {
      key: "actions",
      header: t("server.services.columns.actions"),
      srHeader: true,
      cell: (r) =>
        r.actions.length === 0 ? (
          <span className="ui-hint ui-phone-hide">{t("server.services.managed")}</span>
        ) : (
          <>
            <span className="ui-phone-hide">
              <RowActions>
                {r.actions.filter((a) => shown(r, a)).map((a) => {
                  const I = icons[a];
                  return (
                    <IconButton
                      key={a}
                      label={r.site ? t("server.services.actionLabelSite", { verb: actionVerb(a), name: serviceName(r.id), site: r.site }) : t("server.services.actionLabel", { verb: actionVerb(a), name: serviceName(r.id) })}
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
                label={r.site ? t("server.services.menuLabelSite", { name: serviceName(r.id), site: r.site }) : t("server.services.menuLabel", { name: serviceName(r.id) })}
                buttonLabel={r.site ? t("server.services.menuLabelSite", { name: serviceName(r.id), site: r.site }) : t("server.services.menuLabel", { name: serviceName(r.id) })}
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
        !error && <p aria-busy="true">{t("server.services.loading")}</p>
      ) : (
        site ? (
          <Card padded={false} title={t("server.services.title")} id={id} actions={onClose && <IconButton label={t("server.services.close")} icon={<X size={16} aria-hidden="true" />} onClick={onClose} />}>
            <Table label={t("server.services.title")} columns={columns} rows={rows} rowKey={rowKey} empty={t("server.services.none")} />
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
        title={t("server.services.title")}
        description={t("server.services.description")}
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
  if (bad > 0) return i18n.t("server.services.summary.problems", { bad, count: rows.length });
  const off = rows.filter((r) => stateWord(r).tone !== "ok").length;
  return off === 0
    ? i18n.t("server.services.summary.allRunning", { count: rows.length })
    : i18n.t("server.services.summary.running", { running: rows.length - off, count: rows.length });
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
  const { t } = useTranslation();
  const q = text.trim().toLowerCase();
  const hit = (r: ServiceRow) => !q || `${serviceName(r.id)} ${r.unit} ${r.site ?? ""}`.toLowerCase().includes(q);
  const shownRows = rows.filter(hit);
  const server = shownRows.filter((r) => !r.site);
  const sites = shownRows.filter((r) => r.site);
  const problems = shownRows.filter(problem);
  const groups: { id: Group; label: string }[] = [
    { id: "all", label: t("server.services.groups.all") },
    { id: "problems", label: t("server.services.groups.problems", { count: rows.filter(problem).length }) },
    { id: "sites", label: t("server.services.groups.sites", { count: rows.filter((r) => r.site).length }) },
    { id: "server", label: t("server.services.groups.server", { count: rows.filter((r) => !r.site).length }) },
  ];
  const table = (title: string, list: ServiceRow[], empty: string) => (
    <Card padded={false} title={title} actions={<span className="ui-hint">{summary(list)}</span>}>
      <Table label={title} columns={columns} rows={list} rowKey={rowKey} empty={empty} compact />
    </Card>
  );
  return (
    <>
      <div className="ui-filters ui-filters-bare">
        <div className="ui-seg" role="radiogroup" aria-label={t("server.services.show")}>
          {groups.map((g) => (
            <button key={g.id} type="button" role="radio" aria-checked={group === g.id} className="ui-seg-btn" onClick={() => onGroup(g.id)}>
              {g.label}
            </button>
          ))}
        </div>
        <input className="ui-input" type="search" aria-label={t("server.services.filter")} placeholder={t("server.services.filterPlaceholder")} value={text} onChange={(e) => onText(e.target.value)} />
      </div>
      {group === "problems" && table(t("server.services.tables.problems"), problems, t("server.services.empty.problems"))}
      {(group === "all" || group === "sites") &&
        table(t("server.services.tables.sites"), sites, q ? t("server.services.empty.sitesFilter") : t("server.services.empty.sites"))}
      {(group === "all" || group === "server") &&
        table(t("server.services.tables.server"), server, q ? t("server.services.empty.serverFilter") : t("server.services.none"))}
    </>
  );
}

function ConfirmAction({ pending, busy, onRun, onCancel }: { pending: Pending; busy: boolean; onRun: (confirm?: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const [typed, setTyped] = useState("");
  const { row, action } = pending;
  const name = serviceName(row.id);
  const phrase = `${action} ${row.id}`;
  const typedNeeded = row.confirm?.includes(action) ?? false;
  const effect =
    row.id === "mariadb"
      ? t("server.services.confirm.mariadb")
      : row.id === "fail2ban"
        ? t("server.services.confirm.fail2ban")
        : row.id === "adminer"
          ? t("server.services.confirm.adminer")
          : t("server.services.confirm.other");

  return (
    <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="service-confirm-title">
      <h2 id="service-confirm-title" className="ui-flush">
        {t("server.services.confirm.title", { verb: actionVerb(action), name })}
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
              label={<Trans i18nKey="server.services.confirm.type" values={{ phrase }} components={{ code: <code className="ui-confirm-phrase" /> }} />}
            >
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
        )}
        <div className="ui-row">
          <Button variant="danger" type="submit" disabled={busy || (typedNeeded && typed !== phrase)}>
            {t("server.services.confirm.submit", { verb: actionVerb(action), name })}
          </Button>
          <Button disabled={busy} onClick={onCancel}>
            {t("common.actions.cancel")}
          </Button>
        </div>
      </form>
    </section>
  );
}

const eventTones: Record<ServiceEvent["kind"], Tone> = {
  failed: "err",
  "crash-restart": "warn",
  recovered: "ok",
  started: "off",
  action: "off",
};

/** eventsShown is how many events the card lists at first and adds per click. */
const eventsShown = 10;

function Events({ events }: { events: ServiceEvent[] }) {
  const { t } = useTranslation();
  const [shown, setShown] = useState(eventsShown);
  const visible = events.slice(0, shown);
  const hidden = events.length - visible.length;
  return (
    <Card title={t("server.services.events.title")} padded={events.length === 0}>
      {events.length === 0 ? (
        <p className="ui-flush ui-hint">{t("server.services.events.none")}</p>
      ) : (
        <>
          <ul className="ui-attention-list" aria-label={t("server.services.events.title")}>
            {visible.map((e, i) => (
              <li key={e.at + e.service + (e.site ?? "") + i}>
                <Badge tone={eventTones[e.kind]}>{t(`server.services.events.badges.${e.kind}`)}</Badge>
                <div className="ui-stack-tight">
                  <strong>{ofSite({ id: e.service, site: e.site })}</strong>
                  <span className="ui-hint">
                    <Trans i18nKey="server.services.events.line" values={{ text: eventText(e) }} components={{ time: <RelTime iso={e.at} /> }} />
                  </span>
                </div>
              </li>
            ))}
          </ul>
          {(hidden > 0 || shown > eventsShown) && (
            <div className="ui-card-body ui-row">
              {hidden > 0 && (
                <Button onClick={() => setShown((n) => n + eventsShown)}>
                  {t("server.services.events.more", { count: Math.min(hidden, eventsShown) })}
                </Button>
              )}
              {shown > eventsShown && <Button onClick={() => setShown(eventsShown)}>{t("server.services.events.fewer")}</Button>}
            </div>
          )}
        </>
      )}
    </Card>
  );
}
