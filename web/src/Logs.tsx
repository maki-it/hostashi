// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, DownloadSimple, Pause, Play } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatCount } from "./format";
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
import { formatHash } from "./routes";
import { siteType, type Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

// How often the view asks for new lines while it follows a log.
export const followInterval = 2000;

const tailChoices = [100, 200, 500, 1000];

// A line the view inserts itself where the server rotated the file. It is
// marked by identity, not text, so a log line with the same words stays a line.
const rotatedMark = "\u0000rotated";

type Props = {
  site: Site;
  /** Admins change the retention (ADR-0053). */
  admin: boolean;
};

// SiteLogs shows the logs of a site and follows them live.
export function SiteLogs({ site, admin }: Props) {
  const { t } = useTranslation();
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
    const blob = new Blob([shown.map((l) => (l === rotatedMark ? t("logs.rotated") : l)).join("\n") + "\n"], { type: "text/plain;charset=utf-8" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `${site.name}-${kind}.log`;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  return (
    <>
      <Card
        title={t("logs.title")}
        actions={
          <div className="ui-row">
            <span className={follow ? "ui-live on" : "ui-live"} role="status">
              {follow ? t("logs.live") : t("logs.paused")}
            </span>
            <Button
              icon={follow ? <Pause /> : <Play />}
              onClick={() => {
                if (!follow) setError("");
                setFollow(!follow);
              }}
              disabled={lines === null}
            >
              {follow ? t("logs.pause") : t("logs.follow")}
            </Button>
            <Button icon={<ArrowClockwise />} onClick={() => void load()} disabled={lines === null}>
              {t("logs.reload")}
            </Button>
            <Button icon={<DownloadSimple />} onClick={download} disabled={shown.length === 0}>
              {t("logs.download")}
            </Button>
          </div>
        }
      >
        <div className="ui-stack">
          <div className="ui-row ui-log-tools">
            <div className="ui-seg" role="radiogroup" aria-label={t("logs.kindLabel")}>
              {siteLogKinds(siteType(site)).map((k) => (
                <button key={k.id} type="button" role="radio" aria-checked={kind === k.id} className="ui-seg-btn" onClick={() => setKind(k.id)}>
                  {k.label}
                </button>
              ))}
            </div>
            <Field label={t("logs.lastLines")}>
              <select className="ui-select" value={count} onChange={(e) => setCount(Number(e.target.value))}>
                {tailChoices.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("logs.filter")}>
              <input className="ui-input" type="search" value={filter} placeholder={t("logs.filterPlaceholder")} onChange={(e) => setFilter(e.target.value)} />
            </Field>
          </div>
          {info && <p className="ui-hint">{info.hint}</p>}
          {error && <Notice tone="err" title={error} />}
          <div ref={view} className="ui-log" role="log" aria-label={t("logs.viewLabel", { kind: info?.label ?? "", site: site.name })} tabIndex={0} onScroll={onScroll}>
            {lines === null && <p aria-busy="true">{t("logs.loading")}</p>}
            {lines !== null && shown.length === 0 && !error && <p className="ui-hint">{lines.length === 0 ? t("logs.empty") : t("logs.noMatch")}</p>}
            {shown.map((l, i) => (
              <div key={i} className={l === rotatedMark ? "ui-log-mark" : "ui-log-line " + lineTone(kind, l)}>
                {l === rotatedMark ? t("logs.rotated") : l}
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
              {t("logs.autoScroll")}
            </label>
            <p className="ui-hint ui-flush">
              {follow ? t("logs.every", { seconds: followInterval / 1000 }) : t("logs.pausedHint")}{" "}
              {lines !== null && t("logs.shown", { shown: formatCount(shown.length), total: formatCount(lines.length) })}
            </p>
          </div>
        </div>
      </Card>
      <p className="ui-hint">
        {admin ? <Trans i18nKey="logs.retentionAdmin" components={{ a: <a href={formatHash({ page: "system" })} /> }} /> : t("logs.retentionOther")}
      </p>
    </>
  );
}

// LogRetentionCard says how long the server keeps the logs of every site;
// it lives under System, as it is one value for the server (ADR-0053).
export function LogRetentionCard() {
  const { t } = useTranslation();
  const [r, setR] = useState<LogRetention | null>(null);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  useEffect(() => {
    getLogRetention()
      .then((v) => {
        setR(v);
        setText(String(v.days));
      })
      .catch((err: unknown) => setError(logMessage(err)));
  }, []);

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
      setDone(t("logs.retention.done", { count: v.days }));
    } catch (err) {
      setError(logMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("logs.retention.title")}>
      <form className="ui-stack" onSubmit={save} noValidate>
        <p className="ui-hint">{t("logs.retention.intro")}</p>
        {r && (
          <>
            <div className="ui-row ui-log-tools">
              <Field label={t("logs.retention.days")} {...(problem ? { error: problem } : {})}>
                <input className="ui-input ui-log-days" inputMode="numeric" value={text} onChange={(e) => setText(e.target.value)} />
              </Field>
              <Button type="submit" variant="primary" disabled={busy || !!problem || text === String(r.days)}>
                {t("logs.retention.save")}
              </Button>
            </div>
            <p className="ui-hint">{t("logs.retention.range", { min: r.minDays, max: r.maxDays, default: r.defaultDays })}</p>
          </>
        )}
        {!r && !error && <p aria-busy="true">{t("logs.retention.loading")}</p>}
        <p className="ui-hint">{t("logs.retention.system")}</p>

        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </form>
    </Card>
  );
}
