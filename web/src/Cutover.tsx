// SPDX-License-Identifier: AGPL-3.0-or-later

// The DNS cut-over of an imported site (ADR-0031): the person changes the
// records at their DNS provider; the panel checks every minute and turns
// HTTPS on once every domain resolves only to this server.

import { Fragment, useCallback, useEffect, useState } from "react";
import {
  cancelCutover,
  cutoverMessage,
  getCutover,
  startCutover,
  ttlLabel,
  type Cutover as CutoverView,
  type DomainAnswer,
} from "./cloudpanel";
import { Badge, Button, Notice, RelTime, type Tone } from "./ui";

// The panel checks once a minute; looking twice as often is enough.
export const cutoverPollMs = 30_000;

// A TTL above this keeps old answers cached too long for a quick switch.
const maxTTL = 300;

const stateLabel: Record<CutoverView["state"], string> = {
  waiting: "Waiting for DNS",
  switched: "Done, HTTPS is on",
  failed: "Failed",
  canceled: "Canceled",
};

const stateTone: Record<CutoverView["state"], Tone> = { waiting: "warn", switched: "ok", failed: "err", canceled: "off" };

export function answerLabel(a: DomainAnswer): string {
  if (a.error) return "Lookup failed: " + a.error;
  if (a.here) return a.addrs.join(", ");
  if (a.addrs.length === 0) return "No address";
  return "Points elsewhere: " + a.addrs.join(", ");
}

export function Cutover({ site }: { site: string }) {
  const [c, setC] = useState<CutoverView | null | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setC(await getCutover(site));
      setError("");
    } catch (err) {
      setError(cutoverMessage(err));
    }
  }, [site]);

  useEffect(() => {
    void load();
  }, [load]);

  const waiting = c?.state === "waiting";
  useEffect(() => {
    if (!waiting) return;
    const t = setInterval(() => void load(), cutoverPollMs);
    return () => clearInterval(t);
  }, [waiting, load]);

  async function run(f: (site: string) => Promise<CutoverView>) {
    setBusy(true);
    setError("");
    try {
      setC(await f(site));
    } catch (err) {
      setError(cutoverMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const v4 = c?.server.filter((a) => !a.includes(":")) ?? [];
  const v6 = c?.server.filter((a) => a.includes(":")) ?? [];
  const longTTL = waiting && (c?.answers.some((a) => a.ttl !== undefined && a.ttl > maxTTL) ?? false);

  return (
    <div className="ui-stack" role="group" aria-label={"DNS switch of " + site}>
      {c === undefined ? (
        !error && <p aria-busy="true">Loading the DNS check…</p>
      ) : c === null ? (
        <p className="ui-flush">
          When the files and databases are in place, point the domains of {site} at this server. The panel checks the domains every minute
          and turns HTTPS on once all of them point here.
        </p>
      ) : (
        <>
          <dl className="ui-facts">
            <dt>State</dt>
            <dd>
              <span className="ui-row">
                <Badge tone={stateTone[c.state]}>{stateLabel[c.state]}</Badge>
                {c.doneAt && <RelTime iso={c.doneAt} />}
              </span>
            </dd>
            <dt>Started</dt>
            <dd>
              <RelTime iso={c.startedAt} />
            </dd>
            {waiting && (
              <>
                <dt>Gives up</dt>
                <dd>
                  <RelTime iso={c.deadline} />
                </dd>
              </>
            )}
            <dt>Last check</dt>
            <dd>{c.checkedAt ? <RelTime iso={c.checkedAt} /> : "Not yet"}</dd>
          </dl>
          {waiting && (
            <div className="ui-stack-tight" aria-label={"Records for " + site} role="group">
              <p className="ui-flush">Set these records for every domain below at your DNS provider and remove all other A and AAAA records:</p>
              {c.server.length === 0 ? (
                <p className="ui-hint ui-flush">This server&apos;s public addresses are not known yet.</p>
              ) : (
                <dl className="ui-facts">
                  {v4.length > 0 && (
                    <>
                      <dt>A</dt>
                      <dd>
                        {v4.map((a) => (
                          <div key={a}>
                            <code>{a}</code>
                          </div>
                        ))}
                      </dd>
                    </>
                  )}
                  {v6.length > 0 && (
                    <>
                      <dt>AAAA</dt>
                      <dd>
                        {v6.map((a) => (
                          <div key={a}>
                            <code>{a}</code>
                          </div>
                        ))}
                      </dd>
                    </>
                  )}
                </dl>
              )}
            </div>
          )}
          {c.answers.length > 0 ? (
            <>
              <p className="ui-flush">What the domains resolve to now:</p>
              <dl className="ui-facts" aria-label={"Domains of " + site}>
                {c.answers.map((a) => (
                  <Fragment key={a.domain}>
                    <dt>{a.domain}</dt>
                    <dd>
                      <span className="ui-row">
                        <Badge tone={a.here ? "ok" : "warn"}>{a.here ? "Here" : "Not yet"}</Badge> {answerLabel(a)}
                      </span>
                      {a.ttl !== undefined && (
                        <span className={a.ttl > maxTTL ? "ui-row" : "ui-row ui-hint"}>
                          TTL {ttlLabel(a.ttl)}
                          {a.ttl > maxTTL && waiting && <Badge tone="warn">Lower to 300 s</Badge>}
                        </span>
                      )}
                    </dd>
                  </Fragment>
                ))}
              </dl>
            </>
          ) : (
            waiting && <p className="ui-hint ui-flush">The first check runs in a moment.</p>
          )}
          {longTTL && (
            <Notice tone="warn" title="Some records are cached for more than 5 minutes.">
              Lower their TTL to 300 seconds and wait for the old TTL to pass before you change the addresses, so that visitors switch over
              quickly.
            </Notice>
          )}
          {c.error && <Notice tone="err" title={c.error.charAt(0).toUpperCase() + c.error.slice(1) + "."} />}
        </>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        {c !== undefined && !waiting && c?.state !== "switched" && (
          <Button variant="primary" disabled={busy} onClick={() => void run(startCutover)}>
            {busy ? "Starting…" : c === null ? "Start DNS check" : "Start DNS check again"}
          </Button>
        )}
        {waiting && (
          <>
            <Button disabled={busy} onClick={() => void load()}>
              Reload
            </Button>
            <Button disabled={busy} onClick={() => void run(cancelCutover)}>
              {busy ? "Canceling…" : "Cancel DNS check"}
            </Button>
          </>
        )}
      </div>
    </div>
  );
}
