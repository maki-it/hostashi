// SPDX-License-Identifier: AGPL-3.0-or-later

import { ClockClockwise, StopCircle } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { endToolSession, extendToolSession, listToolSessions, toolMessage, type DBToolOpen } from "./dbtool";
import { Badge, Button, Card, exactTime, IconButton, Notice, RelTime, RowActions } from "./ui";

// minutesLeft is the rounded-up time until an ISO date, never below 0.
export const minutesLeft = (iso: string, now = Date.now()) => Math.max(0, Math.ceil((Date.parse(iso) - now) / 60000));

// ToolSessions lists the Adminer sessions open on one database (ADR-0038
// addendum 1). Extending moves the end 30 minutes later; ending drops the
// temporary user at once and cuts its connections. Nothing shows while no
// session is open.
export function ToolSessions({ db, reload = 0 }: { db: string; reload?: number }) {
  const { t: tr } = useTranslation();
  const [open, setOpen] = useState<DBToolOpen[]>([]);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [, setTick] = useState(0);

  const load = useCallback(() => listToolSessions(db).then(setOpen, () => setOpen([])), [db]);

  useEffect(() => {
    void load();
  }, [load, reload]);

  // The minutes count down; the list itself is fetched again now and then,
  // for sessions opened in another tab.
  useEffect(() => {
    let n = 0;
    const t = setInterval(() => {
      n++;
      setTick(n);
      if (n % 3 === 0) void load();
    }, 20000);
    return () => clearInterval(t);
  }, [load]);

  async function act(id: string, f: () => Promise<unknown>) {
    setBusy(id);
    setError("");
    try {
      await f();
    } catch (err) {
      setError(toolMessage(err));
    } finally {
      await load();
      setBusy("");
    }
  }

  const live = open.filter((s) => minutesLeft(s.expires) > 0);
  if (live.length === 0 && !error) return null;

  return (
    <Card title={tr("databases.sessions.title")}>
      {error && <Notice tone="err" title={error} />}
      <ul className="ui-plain-list">
        {live.map((s) => {
          const left = minutesLeft(s.expires);
          return (
            <li key={s.id} className="ui-list-row">
              <span className="ui-tool-who">
                <span>
                  <Trans
                    i18nKey="databases.sessions.openedBy"
                    values={{ user: s.user || tr("databases.sessions.removedAccount") }}
                    components={{ b: <strong />, time: <RelTime iso={s.opened} /> }}
                  />
                </span>
                <Badge tone={left <= 5 ? "warn" : "ok"}>{left <= 5 ? tr("databases.sessions.endsSoon") : tr("databases.sessions.open")}</Badge>
              </span>
              <span className="ui-tool-end">
                <span className="ui-hint" title={exactTime(new Date(s.expires))}>
                  {tr("databases.sessions.endsIn", { count: left })}
                </span>
                <RowActions>
                  <IconButton
                    label={tr("databases.sessions.extendLabel")}
                    text={tr("databases.sessions.extend")}
                    icon={<ClockClockwise size={18} />}
                    disabled={busy !== ""}
                    onClick={() => void act(s.id, () => extendToolSession(db, s.id))}
                  />
                  <IconButton
                    label={tr("databases.sessions.endNow")}
                    danger
                    text={tr("databases.sessions.endNow")}
                    icon={<StopCircle size={18} />}
                    disabled={busy !== ""}
                    onClick={() => void act(s.id, () => endToolSession(db, s.id))}
                  />
                </RowActions>
              </span>
            </li>
          );
        })}
      </ul>
    </Card>
  );
}

/** What Adminer's "+30 min" and "Log out" ask the panel to do. */
export type ToolAction = "extend" | "end";

/** toolAction reads the request from the address; null for none or a malformed one. */
export function toolAction(query: URLSearchParams): { action: ToolAction; session: string } | null {
  const action = query.get("tool");
  const session = query.get("session") ?? "";
  if ((action !== "extend" && action !== "end") || !/^hostashi_tool_[0-9a-f]{16}$/.test(session)) return null;
  return { action, session };
}

// ToolActionDialog carries out "+30 min" or "Log out" from Adminer
// (ADR-0038 addendum 1): Adminer cannot reach the agent, so it sends the
// browser here, and one click extends or ends the session.
export function ToolActionDialog({ db, action, session, onClose }: { db: string; action: ToolAction; session: string; onClose: () => void }) {
  const { t: tr } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState<string | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    heading.current?.focus();
  }, []);

  async function run() {
    setBusy(true);
    setError("");
    try {
      if (action === "extend") {
        const s = await extendToolSession(db, session);
        setDone(tr("databases.sessions.action.extendedText", { time: exactTime(new Date(s.expires)) }));
      } else {
        await endToolSession(db, session);
        setDone(tr("databases.sessions.action.endedText"));
      }
    } catch (err) {
      setError(toolMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const extend = action === "extend";
  return (
    <section className="ui-card ui-card-body ui-stack" role="dialog" aria-labelledby="dbtool-action-title">
      <h2 id="dbtool-action-title" className="ui-flush" tabIndex={-1} ref={heading}>
        {tr(extend ? "databases.sessions.action.extendTitle" : "databases.sessions.action.endTitle", { db })}
      </h2>
      {done ? (
        <Notice tone="ok" title={tr(extend ? "databases.sessions.action.extended" : "databases.sessions.action.ended")}>
          {done}
        </Notice>
      ) : (
        <p className="ui-flush">
          {tr(extend ? "databases.sessions.action.extendText" : "databases.sessions.action.endText")}
        </p>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        {!done && (
          <Button variant={extend ? "primary" : "danger"} disabled={busy} onClick={() => void run()}>
            {tr(extend ? "databases.sessions.action.extendSubmit" : "databases.sessions.action.endSubmit")}
          </Button>
        )}
        <Button disabled={busy} onClick={onClose}>
          {done ? tr("databases.tool.close") : tr("common.actions.cancel")}
        </Button>
      </div>
    </section>
  );
}
