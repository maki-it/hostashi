// SPDX-License-Identifier: AGPL-3.0-or-later

import { PencilSimple, Power } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { PanelCertificate } from "./Certificate";
import { LogRetentionCard } from "./Logs";
import { MalwareEngineCard } from "./Malware";
import { PanelRealIPCard } from "./RealIP";
import { formatDateTime } from "./format";
import { formatHash } from "./routes";
import { serviceMessage } from "./services";
import {
  clockIn,
  getSystem,
  hostnameError,
  listTimezones,
  pinsDate,
  rebootServer,
  releaseUrl,
  setAutoUpdate,
  setHostname,
  setTimezone,
  uptime,
  versionLabel,
  waitForPanel,
  type SystemInfo,
  type SystemUpdates,
} from "./system";
import { Badge, Button, Card, Field, Notice, PageHead, Switch } from "./ui";

type RebootState = { phase: "idle" } | { phase: "going-down" | "starting" } | { phase: "back" } | { phase: "lost" };

/**
 * System shows the server's hostname, time zone, clock and uptime, lets an
 * admin change the first two and reboot the server after typing its
 * hostname (ADR-0060). The panel's certificate (ADR-0039) and its real
 * visitor address (ADR-0059) are settings too and live here; Health only
 * warns about the certificate.
 */
export function System({ wait = waitForPanel }: { wait?: typeof waitForPanel }) {
  const { t } = useTranslation();
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [zones, setZones] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [reboot, setReboot] = useState<RebootState>({ phase: "idle" });

  const load = useCallback(async () => {
    try {
      setInfo(await getSystem());
      setError("");
    } catch (err) {
      setError(serviceMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
    listTimezones().then(setZones, () => setZones([]));
  }, [load]);

  const changed = (next: SystemInfo, text: string) => {
    setInfo(next);
    setDone(text);
    setError("");
  };

  async function startReboot(confirm: string) {
    setDone("");
    setError("");
    try {
      await rebootServer(confirm);
    } catch (err) {
      setError(serviceMessage(err));
      return;
    }
    setReboot({ phase: "going-down" });
    const ok = await wait({ onPhase: (phase) => setReboot({ phase }) });
    if (!ok) {
      setReboot({ phase: "lost" });
      return;
    }
    setReboot({ phase: "back" });
    await load();
  }

  return (
    <>
      <PageHead
        title={t("server.system.title")}
        description={t("server.system.description")}
      />
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
      {reboot.phase !== "idle" && <RebootProgress state={reboot} />}
      {info === null ? (
        !error && <p aria-busy="true">{t("server.system.loading")}</p>
      ) : (
        <>
          <Overview
            info={info}
            zones={zones}
            onHostname={(n) => changed(n, t("server.system.hostnameDone", { name: n.hostname }))}
            onTimezone={(n) => changed(n, t("server.system.timezoneDone", { zone: n.timezone }))}
            onError={setError}
          />
          <UpdatesCard updates={info.updates} onChanged={changed} onError={setError} />
          <PanelCertificate />
          <PanelRealIPCard />
          <LogRetentionCard />
          <MalwareEngineCard />
          <RebootCard info={info} busy={reboot.phase === "going-down" || reboot.phase === "starting"} onReboot={(c) => void startReboot(c)} />
        </>
      )}
    </>
  );
}

// Overview shows the server's facts once; hostname and time zone open
// their form in place instead of repeating the value in a card of its own.
function Overview({
  info,
  zones,
  onHostname,
  onTimezone,
  onError,
}: {
  info: SystemInfo;
  zones: string[];
  onHostname: (i: SystemInfo) => void;
  onTimezone: (i: SystemInfo) => void;
  onError: (m: string) => void;
}) {
  const { t } = useTranslation();
  const [edit, setEdit] = useState<"" | "hostname" | "timezone">("");
  const change = (what: "hostname" | "timezone", label: string) =>
    edit !== what && (
      <Button small icon={<PencilSimple size={16} aria-hidden />} aria-label={label} onClick={() => setEdit(what)}>
        {t("server.system.server.change")}
      </Button>
    );
  return (
    <Card
      title={t("server.system.server.title")}
      actions={
        info.rebootRequired ? (
          <Badge tone="warn">{t("server.system.server.rebootNeeded")}</Badge>
        ) : (
          <Badge tone="ok">{t("server.system.server.upToDate")}</Badge>
        )
      }
    >
      <div className="ui-stack">
        <dl className="ui-facts">
          <dt>{t("server.system.server.hostname")}</dt>
          <dd className="ui-row">
            <span>
              <code>{info.hostname}</code>
              {info.transientHostname && info.transientHostname !== info.hostname && (
                <span className="ui-hint">{t("server.system.server.kernelName", { name: info.transientHostname })}</span>
              )}
            </span>
            {change("hostname", t("server.system.server.changeHostname"))}
          </dd>
          <dt>{t("server.system.server.timezone")}</dt>
          <dd className="ui-row">
            <span>{info.timezone || t("server.system.server.notSet")}</span>
            {change("timezone", t("server.system.server.changeTimezone"))}
          </dd>
          <dt>{t("server.system.server.time")}</dt>
          <dd>
            {clockIn(info.time, info.timezone || "UTC") || info.time}{" "}
            {info.ntpSynchronized ? (
              <Badge tone="ok">{t("server.system.server.synced")}</Badge>
            ) : (
              <Badge tone="warn">{t("server.system.server.notSynced")}</Badge>
            )}
          </dd>
          <dt>{t("server.system.server.uptime")}</dt>
          <dd>{uptime(info.uptimeSeconds)}</dd>
        </dl>
        {edit === "hostname" && (
          <HostnameForm
            info={info}
            onDone={(i) => {
              setEdit("");
              onHostname(i);
            }}
            onCancel={() => setEdit("")}
            onError={onError}
          />
        )}
        {edit === "timezone" && (
          <TimezoneForm
            info={info}
            zones={zones}
            onDone={(i) => {
              setEdit("");
              onTimezone(i);
            }}
            onCancel={() => setEdit("")}
            onError={onError}
          />
        )}
        {info.rebootRequired && (
          <p className="ui-flush">
            {info.rebootPackages && info.rebootPackages.length > 0
              ? t("server.system.server.rebootWaitsPackages", { packages: info.rebootPackages.join(", ") })
              : t("server.system.server.rebootWaits")}
          </p>
        )}
      </div>
    </Card>
  );
}

// UpdatesCard says which Hostashi version runs and whether a newer one is
// out, and switches the automatic installation of new versions
// (--auto-update, ADR-0050) on and off.
function UpdatesCard({
  updates,
  onChanged,
  onError,
}: {
  updates: SystemUpdates | undefined;
  onChanged: (i: SystemInfo, text: string) => void;
  onError: (m: string) => void;
}) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  if (!updates) return null;
  const version = (v: string) => (
    <a href={releaseUrl(v)} target="_blank" rel="noopener noreferrer" title={t("server.system.updates.releaseNotes")}>
      {versionLabel(v)}
    </a>
  );
  async function toggle(on: boolean) {
    setBusy(true);
    try {
      const next = await setAutoUpdate(on);
      onChanged(
        next,
        on ? t("server.system.updates.on") : t("server.system.updates.off"),
      );
    } catch (err) {
      onError(serviceMessage(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card
      title={t("server.system.updates.title")}
      actions={
        updates.update ? (
          <Badge tone="warn">{t("server.system.updates.available")}</Badge>
        ) : updates.available ? (
          <Badge tone="ok">{t("server.system.updates.upToDate")}</Badge>
        ) : undefined
      }
    >
      <div className="ui-stack">
        <dl className="ui-facts">
          <dt>Hostashi</dt>
          <dd>
            {version(updates.installed)}
            {updates.update && updates.available && <Trans i18nKey="server.system.updates.newest" components={{ link: version(updates.available) }} />}
          </dd>
          <dt>{t("server.system.updates.runtimes")}</dt>
          <dd>
            {updates.runtimes && updates.runtimesAuto ? (
              <>
                <Badge tone="ok">{t("server.system.updates.automatic")}</Badge>{" "}
                <span className="ui-hint">{t("server.system.updates.pins", { date: pinsDate(updates.runtimes) || updates.runtimes })}</span>
              </>
            ) : (
              <>
                <Badge tone="warn">{t("server.system.updates.notSetUp")}</Badge>{" "}
                <span className="ui-hint">{t("server.system.updates.notSetUpHint")}</span>
              </>
            )}
          </dd>
          {updates.listedAt && (
            <>
              <dt>{t("server.system.updates.lastChecked")}</dt>
              <dd>{formatDateTime(new Date(updates.listedAt))}</dd>
            </>
          )}
        </dl>
        <Switch
          checked={updates.auto}
          disabled={busy}
          hint={
            updates.auto ? t("server.system.updates.hintOn") : <Trans i18nKey="server.system.updates.hintOff" components={{ code: <code /> }} />
          }
          onChange={(on) => void toggle(on)}
        >
          {t("server.system.updates.switch")}
        </Switch>
      </div>
    </Card>
  );
}

function HostnameForm({
  info,
  onDone,
  onCancel,
  onError,
}: {
  info: SystemInfo;
  onDone: (i: SystemInfo) => void;
  onCancel: () => void;
  onError: (m: string) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(info.hostname);
  const [busy, setBusy] = useState(false);
  const problem = hostnameError(name);
  const same = name === info.hostname;

  return (
    <section className="ui-edit" aria-label={t("server.system.hostname.label")}>
      <form
        className="ui-stack"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          try {
            onDone(await setHostname(name));
          } catch (err) {
            onError(serviceMessage(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="ui-narrow">
          <Field
            label={t("server.system.hostname.label")}
            hint={t("server.system.hostname.hint")}
            {...(!same && problem ? { error: problem } : {})}
          >
            <input className="ui-input" value={name} onChange={(e) => setName(e.target.value.trim())} autoComplete="off" spellCheck={false} />
          </Field>
        </div>
        {info.cloudInit && (
          <p className="ui-flush ui-hint">{t("server.system.hostname.cloudInit")}</p>
        )}
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy || same || problem !== ""}>
            {t("server.system.hostname.save")}
          </Button>
          <Button disabled={busy} onClick={onCancel}>
            {t("common.actions.cancel")}
          </Button>
        </div>
      </form>
    </section>
  );
}

function TimezoneForm({
  info,
  zones,
  onDone,
  onCancel,
  onError,
}: {
  info: SystemInfo;
  zones: string[];
  onDone: (i: SystemInfo) => void;
  onCancel: () => void;
  onError: (m: string) => void;
}) {
  const { t } = useTranslation();
  const [zone, setZone] = useState(info.timezone);
  const [busy, setBusy] = useState(false);
  const known = zones.length === 0 || zones.includes(zone);
  const same = zone === info.timezone;
  const preview = known && !same ? clockIn(info.time, zone) : "";

  return (
    <section className="ui-edit" aria-label={t("server.system.timezone.label")}>
      <form
        className="ui-stack"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          try {
            onDone(await setTimezone(zone));
          } catch (err) {
            onError(serviceMessage(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="ui-narrow">
          <Field
            label={t("server.system.timezone.label")}
            hint={preview ? t("server.system.timezone.preview", { time: preview }) : t("server.system.timezone.hint")}
            {...(!known ? { error: t("server.system.timezone.unknown") } : {})}
          >
            <input
              className="ui-input"
              list="system-zones"
              value={zone}
              onChange={(e) => setZone(e.target.value.trim())}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <datalist id="system-zones">
            {zones.map((z) => (
              <option key={z} value={z} />
            ))}
          </datalist>
        </div>
        <Notice tone="info" title={t("server.system.timezone.followsTitle")}>
          <Trans i18nKey="server.system.timezone.follows" components={{ a: <a href={formatHash({ page: "services" })} />, code: <code /> }} />
        </Notice>
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy || same || !known || zone === ""}>
            {t("server.system.timezone.save")}
          </Button>
          <Button disabled={busy} onClick={onCancel}>
            {t("common.actions.cancel")}
          </Button>
        </div>
      </form>
    </section>
  );
}

function RebootCard({ info, busy, onReboot }: { info: SystemInfo; busy: boolean; onReboot: (confirm: string) => void }) {
  const { t } = useTranslation();
  const [asking, setAsking] = useState(false);
  const [typed, setTyped] = useState("");

  return (
    <Card title={t("server.system.reboot.title")}>
      <div className="ui-stack">
        <p className="ui-flush">{t("server.system.reboot.text")}</p>
        {!asking ? (
          <div className="ui-row">
            <Button variant="danger" disabled={busy} onClick={() => setAsking(true)}>
              <Power size={16} aria-hidden="true" /> {t("server.system.reboot.button")}
            </Button>
          </div>
        ) : (
          <form
            className="ui-stack"
            role="alertdialog"
            aria-labelledby="reboot-confirm-title"
            onSubmit={(e) => {
              e.preventDefault();
              setAsking(false);
              setTyped("");
              onReboot(typed);
            }}
          >
            <h3 id="reboot-confirm-title" className="ui-flush">
              {t("server.system.reboot.confirmTitle", { name: info.hostname })}
            </h3>
            <div className="ui-narrow">
              <Field
                label={
                  <Trans i18nKey="server.system.reboot.type" values={{ name: info.hostname }} components={{ code: <code className="ui-confirm-phrase" /> }} />
                }
              >
                <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
            </div>
            <div className="ui-row">
              <Button variant="danger" type="submit" disabled={busy || typed !== info.hostname}>
                {t("server.system.reboot.now")}
              </Button>
              <Button
                onClick={() => {
                  setAsking(false);
                  setTyped("");
                }}
              >
                {t("common.actions.cancel")}
              </Button>
            </div>
          </form>
        )}
      </div>
    </Card>
  );
}

function RebootProgress({ state }: { state: RebootState }) {
  const { t } = useTranslation();
  switch (state.phase) {
    case "going-down":
      return (
        <Notice tone="warn" title={t("server.system.reboot.goingDown")}>
          {t("server.system.reboot.goingDownText")}
        </Notice>
      );
    case "starting":
      return (
        <Notice tone="warn" title={t("server.system.reboot.starting")}>
          {t("server.system.reboot.startingText")}
        </Notice>
      );
    case "back":
      return <Notice tone="ok" title={t("server.system.reboot.back")} />;
    case "lost":
      return (
        <Notice tone="err" title={t("server.system.reboot.lost")}>
          {t("server.system.reboot.lostText")}
        </Notice>
      );
    default:
      return null;
  }
}
