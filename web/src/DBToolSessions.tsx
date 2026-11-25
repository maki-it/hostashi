// SPDX-License-Identifier: AGPL-3.0-or-later

import { ClockClockwise, StopCircle } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { endToolSession, extendToolSession, listToolSessions, toolMessage, type DBToolOpen } from "./dbtool";
import { Badge, Button, Card, exactTime, IconButton, Notice, RelTime, RowActions } from "./ui";

// minutesLeft is the rounded-up time until an ISO date, never below 0.
export const minutesLeft = (iso: string, now = Date.now()) => Math.max(0, Math.ceil((Date.parse(iso) - now) / 60000));

// ToolSessions lists the Adminer sessions open on one database (ADR-0038
// addendum 1). Extending moves the end 30 minutes later; ending drops the
// temporary user at once and cuts its connections. Nothing shows while no
// session is open.
export function ToolSessions({ db, reload = 0 }: { db: string; reload?: number }) {
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
    <Card title="Adminer sessions">
      {error && <Notice tone="err" title={error} />}
      <ul className="ui-plain-list">
        {live.map((s) => {
          const left = minutesLeft(s.expires);
          return (
            <li key={s.id} className="ui-list-row">
              <span className="ui-tool-who">
                <span>
                  <strong>{s.user || "A removed account"}</strong> opened it <RelTime iso={s.opened} />
                </span>
                <Badge tone={left <= 5 ? "warn" : "ok"}>{left <= 5 ? "Ends soon" : "Open"}</Badge>
              </span>
              <span className="ui-tool-end">
                <span className="ui-hint" title={exactTime(new Date(s.expires))}>
                  ends in {left} min
                </span>
                <RowActions>
                  <IconButton
                    label="Extend by 30 minutes"
                    text="Extend" icon={<ClockClockwise size={18} />}
                    disabled={busy !== ""}
                    onClick={() => void act(s.id, () => extendToolSession(db, s.id))}
                  />
                  <IconButton
                    label="End now"
                    danger
                    text="End now" icon={<StopCircle size={18} />}
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
        setDone(`The session now ends at ${exactTime(new Date(s.expires))}. Go back to the Adminer tab; it shows the new time.`);
      } else {
        await endToolSession(db, session);
        setDone("The temporary database user is gone and its connections are closed.");
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
        {extend ? `Extend the Adminer session on ${db}` : `End the Adminer session on ${db}`}
      </h2>
      {done ? (
        <Notice tone="ok" title={extend ? "Extended by 30 minutes." : "Session ended."}>
          {done}
        </Notice>
      ) : (
        <p className="ui-flush">
          {extend
            ? "The session gets 30 more minutes, up to two hours ahead."
            : "Adminer logs out, the temporary database user is removed and its open connections are closed. Queries still running stop."}
        </p>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        {!done && (
          <Button variant={extend ? "primary" : "danger"} disabled={busy} onClick={() => void run()}>
            {extend ? "Extend by 30 minutes" : "End session"}
          </Button>
        )}
        <Button disabled={busy} onClick={onClose}>
          {done ? "Close" : "Cancel"}
        </Button>
      </div>
    </section>
  );
}
