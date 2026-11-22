// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, DownloadSimple, Pause, Play } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import {
  appendLines,
  filterLines,
  followLog,
  getLogRetention,
  lineTone,
  logKinds,
  siteLogKinds,
  logMessage,
  retentionProblem,
  setLogRetention,
  tailLog,
  type LogKind,
  type LogRetention,
} from "./logs";
import { siteType, type Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

// How often the view asks for new lines while it follows a log.
export const followInterval = 2000;

const tailChoices = [100, 200, 500, 1000];

// A line the view inserts itself where the server rotated the file.
const rotatedMark = "— the log was rotated, new file —";

type Props = {
  site: Site;
  /** Admins change the retention (ADR-0053). */
  admin: boolean;
};

// SiteLogs shows the logs of a site and follows them live.
export function SiteLogs({ site, admin }: Props) {
  const [kind, setKind] = useState<LogKind>("access");
  const [count, setCount] = useState(200);
  const [lines, setLines] = useState<string[] | null>(null);
  const [follow, setFollow] = useState(true);
  const [filter, setFilter] = useState("");
  const [error, setError] = useState("");
  const cursor = useRef("");
  const view = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [autoScroll, setAutoScroll] = useState(true);
  const gen = useRef(0);

  const load = useCallback(async () => {
    const my = ++gen.current;
    setLines(null);
    setError("");
    try {
      const c = await tailLog(site.name, kind, count);
      if (my !== gen.current) return;
      cursor.current = c.cursor;
      stick.current = true;
      setLines(c.lines);
    } catch (err) {
      if (my !== gen.current) return;
      setError(logMessage(err));
      setLines([]);
    }
  }, [site.name, kind, count]);

  useEffect(() => {
    void load();
  }, [load]);

  // Follow: ask for what came after the cursor while the page is visible.
  useEffect(() => {
    if (!follow || lines === null || error) return;
    let stopped = false;
    const my = gen.current;
    const tick = async () => {
      if (document.visibilityState !== "visible" || !cursor.current) return;
      try {
        let more = true;
        while (more && !stopped && my === gen.current) {
          const c = await followLog(site.name, kind, cursor.current);
          if (stopped || my !== gen.current) return;
          cursor.current = c.cursor;
          more = c.more;
          if (c.rotated || c.lines.length > 0) {
            setLines((old) => appendLines(old ?? [], c.rotated ? [rotatedMark, ...c.lines] : c.lines));
          }
        }
      } catch (err) {
        if (!stopped) {
          setError(logMessage(err));
          setFollow(false);
        }
      }
    };
    const id = window.setInterval(() => void tick(), followInterval);
    return () => {
      stopped = true;
      window.clearInterval(id);
    };
  }, [follow, lines === null, error, site.name, kind]);

  // Keep the newest line in sight unless the reader scrolled up.
  useEffect(() => {
    const el = view.current;
    if (el && autoScroll && stick.current) el.scrollTop = el.scrollHeight;
  }, [lines, filter, autoScroll]);

  const onScroll = () => {
    const el = view.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  };

  const shown = lines ? filterLines(lines, filter) : [];
  const info = logKinds.find((k) => k.id === kind);

  function download() {
    const blob = new Blob([shown.join("\n") + "\n"], { type: "text/plain;charset=utf-8" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `${site.name}-${kind}.log`;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  return (
    <>
      <Card
        title="Logs"
        actions={
          <div className="ui-row">
            <span className={follow ? "ui-live on" : "ui-live"} role="status">
              {follow ? "Live" : "Paused"}
            </span>
            <Button
              icon={follow ? <Pause /> : <Play />}
              onClick={() => {
                if (!follow) setError("");
                setFollow(!follow);
              }}
              disabled={lines === null}
            >
              {follow ? "Pause" : "Follow"}
            </Button>
            <Button icon={<ArrowClockwise />} onClick={() => void load()} disabled={lines === null}>
              Reload
            </Button>
            <Button icon={<DownloadSimple />} onClick={download} disabled={shown.length === 0}>
              Download
            </Button>
          </div>
        }
      >
        <div className="ui-stack">
          <div className="ui-row ui-log-tools">
            <div className="ui-seg" role="radiogroup" aria-label="Log">
              {siteLogKinds(siteType(site)).map((k) => (
                <button key={k.id} type="button" role="radio" aria-checked={kind === k.id} className="ui-seg-btn" onClick={() => setKind(k.id)}>
                  {k.label}
                </button>
              ))}
            </div>
            <Field label="Last lines">
              <select className="ui-select" value={count} onChange={(e) => setCount(Number(e.target.value))}>
                {tailChoices.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Filter">
              <input className="ui-input" type="search" value={filter} placeholder="e.g. 404 or wp-login" onChange={(e) => setFilter(e.target.value)} />
            </Field>
          </div>
          {info && <p className="ui-hint">{info.hint}</p>}
          {error && <Notice tone="err" title={error} />}
          <div ref={view} className="ui-log" role="log" aria-label={`${info?.label ?? ""} log of ${site.name}`} tabIndex={0} onScroll={onScroll}>
            {lines === null && <p aria-busy="true">Loading…</p>}
            {lines !== null && shown.length === 0 && !error && (
              <p className="ui-hint">{lines.length === 0 ? "The log is empty so far." : "No line matches the filter."}</p>
            )}
            {shown.map((l, i) => (
              <div key={i} className={l === rotatedMark ? "ui-log-mark" : "ui-log-line " + lineTone(kind, l)}>
                {l}
              </div>
            ))}
          </div>
          <div className="ui-row ui-log-foot">
            <label className="ui-check">
              <input
                type="checkbox"
                checked={autoScroll}
                onChange={(e) => {
                  stick.current = e.target.checked;
                  setAutoScroll(e.target.checked);
                }}
              />
              Scroll automatically
            </label>
            <p className="ui-hint ui-flush">
              {follow ? `New lines appear every ${followInterval / 1000} seconds.` : "Paused: press Follow to see new lines."}{" "}
              {lines !== null && `${shown.length} of ${lines.length} lines shown.`}
            </p>
          </div>
        </div>
      </Card>
      <RetentionCard admin={admin} />
    </>
  );
}

// RetentionCard says how long the server keeps the logs; admins change it.
function RetentionCard({ admin }: { admin: boolean }) {
  const [r, setR] = useState<LogRetention | null>(null);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  useEffect(() => {
    if (!admin) return;
    getLogRetention()
      .then((v) => {
        setR(v);
        setText(String(v.days));
      })
      .catch((err: unknown) => setError(logMessage(err)));
  }, [admin]);

  if (!admin) {
    return (
      <p className="ui-hint">
        The server deletes old logs after the days an administrator set. Ask them if you need logs for longer.
      </p>
    );
  }

  const problem = r ? retentionProblem(text, r) : "";

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!r || problem) return;
    setBusy(true);
    setError("");
    setDone("");
    try {
      const v = await setLogRetention(Number(text));
      setR(v);
      setDone(`Logs are kept for ${v.days} ${v.days === 1 ? "day" : "days"} from the next rotation on.`);
    } catch (err) {
      setError(logMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Log retention">
      <form className="ui-stack" onSubmit={save} noValidate>
        <p className="ui-hint">
          One value for every site on this server: access, web server, PHP and app logs, and nginx's logs of requests for no site. Logs are
          rotated daily and deleted after this many days. Match it to what your privacy policy and data processing agreements promise.
        </p>
        {r && (
          <>
            <div className="ui-row ui-log-tools">
              <Field label="Keep logs for (days)" {...(problem ? { error: problem } : {})}>
                <input className="ui-input ui-log-days" inputMode="numeric" value={text} onChange={(e) => setText(e.target.value)} />
              </Field>
              <Button type="submit" variant="primary" disabled={busy || !!problem || text === String(r.days)}>
                Save
              </Button>
            </div>
            <p className="ui-hint">
              {r.minDays} to {r.maxDays} days, default {r.defaultDays}. A shorter time deletes older logs at the next daily rotation.
            </p>
          </>
        )}
        {!r && !error && <p aria-busy="true">Loading…</p>}
        <p className="ui-hint">The panel's own logs and the system journal keep the settings of the distribution.</p>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </form>
    </Card>
  );
}
