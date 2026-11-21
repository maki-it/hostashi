// SPDX-License-Identifier: AGPL-3.0-or-later

import { ClockClockwise, StopCircle } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { endToolSession, extendToolSession, listToolSessions, toolMessage, type DBToolOpen } from "./dbtool";
import { Badge, Card, exactTime, IconButton, Notice, RelTime, RowActions } from "./ui";

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
                    icon={<ClockClockwise size={18} />}
                    disabled={busy !== ""}
                    onClick={() => void act(s.id, () => extendToolSession(db, s.id))}
                  />
                  <IconButton
                    label="End now"
                    danger
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
