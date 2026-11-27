// SPDX-License-Identifier: AGPL-3.0-or-later

// The DNS cut-over of an imported site (ADR-0031): the person changes the
// records at their DNS provider; the panel checks every minute and turns
// HTTPS on once every domain resolves only to this server.

import { Fragment, useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
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

// DNS record types read the same in every language.
const recordA = "A";
const recordAAAA = "AAAA";

// A TTL above this keeps old answers cached too long for a quick switch.
const maxTTL = 300;

const stateTone: Record<CutoverView["state"], Tone> = { waiting: "warn", switched: "ok", failed: "err", canceled: "off" };

export function answerLabel(a: DomainAnswer): string {
  if (a.error) return i18n.t("sites.cutover.lookupFailed", { error: a.error });
  if (a.here) return a.addrs.join(", ");
  if (a.addrs.length === 0) return i18n.t("sites.cutover.noAddress");
  return i18n.t("sites.cutover.elsewhere", { addrs: a.addrs.join(", ") });
}

export function Cutover({ site }: { site: string }) {
  const { t } = useTranslation();
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
    <div className="ui-stack" role="group" aria-label={t("sites.cutover.label", { site })}>
      {c === undefined ? (
        !error && <p aria-busy="true">{t("sites.cutover.loading")}</p>
      ) : c === null ? (
        <p className="ui-flush">{t("sites.cutover.intro", { site })}</p>
      ) : (
        <>
          <dl className="ui-facts">
            <dt>{t("sites.cutover.state")}</dt>
            <dd>
              <span className="ui-row">
                <Badge tone={stateTone[c.state]}>{t(`sites.cutover.states.${c.state}`)}</Badge>
                {c.doneAt && <RelTime iso={c.doneAt} />}
              </span>
            </dd>
            <dt>{t("sites.cutover.started")}</dt>
            <dd>
              <RelTime iso={c.startedAt} />
            </dd>
            {waiting && (
              <>
                <dt>{t("sites.cutover.givesUp")}</dt>
                <dd>
                  <RelTime iso={c.deadline} />
                </dd>
              </>
            )}
            <dt>{t("sites.cutover.lastCheck")}</dt>
            <dd>{c.checkedAt ? <RelTime iso={c.checkedAt} /> : t("sites.cutover.notYet")}</dd>
          </dl>
          {waiting && (
            <div className="ui-stack-tight" aria-label={t("sites.cutover.records", { site })} role="group">
              <p className="ui-flush">{t("sites.cutover.setRecords")}</p>
              {c.server.length === 0 ? (
                <p className="ui-hint ui-flush">{t("sites.cutover.noServer")}</p>
              ) : (
                <dl className="ui-facts">
                  {v4.length > 0 && (
                    <>
                      <dt>{recordA}</dt>
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
                      <dt>{recordAAAA}</dt>
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
              <p className="ui-flush">{t("sites.cutover.now")}</p>
              <dl className="ui-facts" aria-label={t("sites.cutover.domains", { site })}>
                {c.answers.map((a) => (
                  <Fragment key={a.domain}>
                    <dt>{a.domain}</dt>
                    <dd>
                      <span className="ui-row">
                        <Badge tone={a.here ? "ok" : "warn"}>{a.here ? t("sites.cutover.here") : t("sites.cutover.notYet")}</Badge> {answerLabel(a)}
                      </span>
                      {a.ttl !== undefined && (
                        <span className={a.ttl > maxTTL ? "ui-row" : "ui-row ui-hint"}>
                          {t("sites.cutover.ttl", { ttl: ttlLabel(a.ttl) })}
                          {a.ttl > maxTTL && waiting && <Badge tone="warn">{t("sites.cutover.lower")}</Badge>}
                        </span>
                      )}
                    </dd>
                  </Fragment>
                ))}
              </dl>
            </>
          ) : (
            waiting && <p className="ui-hint ui-flush">{t("sites.cutover.firstCheck")}</p>
          )}
          {longTTL && (
            <Notice tone="warn" title={t("sites.cutover.longTtl")}>
              {t("sites.cutover.longTtlText")}
            </Notice>
          )}
          {c.error && <Notice tone="err" title={c.error.charAt(0).toUpperCase() + c.error.slice(1) + "."} />}
        </>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        {c !== undefined && !waiting && c?.state !== "switched" && (
          <Button variant="primary" disabled={busy} onClick={() => void run(startCutover)}>
            {busy ? t("sites.cutover.starting") : c === null ? t("sites.cutover.start") : t("sites.cutover.startAgain")}
          </Button>
        )}
        {waiting && (
          <>
            <Button disabled={busy} onClick={() => void load()}>
              {t("sites.cutover.reload")}
            </Button>
            <Button disabled={busy} onClick={() => void run(cancelCutover)}>
              {busy ? t("sites.cutover.canceling") : t("sites.cutover.cancel")}
            </Button>
          </>
        )}
      </div>
    </div>
  );
}
