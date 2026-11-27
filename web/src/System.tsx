// SPDX-License-Identifier: AGPL-3.0-or-later

import { PencilSimple, Power } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { PanelCertificate } from "./Certificate";
import { LogRetentionCard } from "./Logs";
import { MalwareEngineCard } from "./Malware";
import { PanelRealIPCard } from "./RealIP";
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
        title="System"
        description="The server's name, time zone and clock, and the panel's own address. A reboot stops every site until the server is back."
      />
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
      {reboot.phase !== "idle" && <RebootProgress state={reboot} />}
      {info === null ? (
        !error && <p aria-busy="true">Loading the server settings…</p>
      ) : (
        <>
          <Overview
            info={info}
            zones={zones}
            onHostname={(n) => changed(n, `The hostname is now ${n.hostname}.`)}
            onTimezone={(n) => changed(n, `The time zone is now ${n.timezone}.`)}
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
  const [edit, setEdit] = useState<"" | "hostname" | "timezone">("");
  const change = (what: "hostname" | "timezone", label: string) =>
    edit !== what && (
      <Button small icon={<PencilSimple size={16} aria-hidden />} aria-label={`Change ${label}`} onClick={() => setEdit(what)}>
        Change
      </Button>
    );
  return (
    <Card
      title="Server"
      actions={info.rebootRequired ? <Badge tone="warn">Reboot needed</Badge> : <Badge tone="ok">Up to date</Badge>}
    >
      <div className="ui-stack">
        <dl className="ui-facts">
          <dt>Hostname</dt>
          <dd className="ui-row">
            <span>
              <code>{info.hostname}</code>
              {info.transientHostname && info.transientHostname !== info.hostname && (
                <span className="ui-hint"> (the kernel still uses {info.transientHostname})</span>
              )}
            </span>
            {change("hostname", "hostname")}
          </dd>
          <dt>Time zone</dt>
          <dd className="ui-row">
            <span>{info.timezone || "not set"}</span>
            {change("timezone", "time zone")}
          </dd>
          <dt>Server time</dt>
          <dd>
            {clockIn(info.time, info.timezone || "UTC") || info.time}{" "}
            {info.ntpSynchronized ? <Badge tone="ok">Synchronized</Badge> : <Badge tone="warn">Not synchronized</Badge>}
          </dd>
          <dt>Uptime</dt>
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
            Updates wait for a reboot
            {info.rebootPackages && info.rebootPackages.length > 0 ? <>: {info.rebootPackages.join(", ")}.</> : "."} The server never
            reboots by itself.
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
  const [busy, setBusy] = useState(false);
  if (!updates) return null;
  const version = (v: string) => (
    <a href={releaseUrl(v)} target="_blank" rel="noopener noreferrer" title="Release notes">
      {versionLabel(v)}
    </a>
  );
  async function toggle(on: boolean) {
    setBusy(true);
    try {
      const next = await setAutoUpdate(on);
      onChanged(
        next,
        on
          ? "Automatic updates are on. New Hostashi versions install with the next daily security updates."
          : "Automatic updates are off. New Hostashi versions wait for sudo apt upgrade.",
      );
    } catch (err) {
      onError(serviceMessage(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card
      title="Updates"
      actions={updates.update ? <Badge tone="warn">Update available</Badge> : updates.available ? <Badge tone="ok">Up to date</Badge> : undefined}
    >
      <div className="ui-stack">
        <dl className="ui-facts">
          <dt>Hostashi</dt>
          <dd>
            {version(updates.installed)}
            {updates.update && updates.available && <> (newest: {version(updates.available)})</>}
          </dd>
          <dt>Node.js and Python patches</dt>
          <dd>
            {updates.runtimes && updates.runtimesAuto ? (
              <>
                <Badge tone="ok">Automatic</Badge>{" "}
                <span className="ui-hint">pins of {pinsDate(updates.runtimes) || updates.runtimes}, always on</span>
              </>
            ) : (
              <>
                <Badge tone="warn">Not set up</Badge> <span className="ui-hint">the installer sets them up on its next run</span>
              </>
            )}
          </dd>
          {updates.listedAt && (
            <>
              <dt>Last checked</dt>
              <dd>{new Date(updates.listedAt).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}</dd>
            </>
          )}
        </dl>
        <Switch
          checked={updates.auto}
          disabled={busy}
          hint={
            updates.auto ? (
              "unattended-upgrades installs new Hostashi versions with the daily security updates, after a copy of the panel database."
            ) : (
              <>
                New Hostashi versions wait for <code>sudo apt upgrade</code>. Health and an email say when one is out.
              </>
            )
          }
          onChange={(on) => void toggle(on)}
        >
          Automatic updates
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
  const [name, setName] = useState(info.hostname);
  const [busy, setBusy] = useState(false);
  const problem = hostnameError(name);
  const same = name === info.hostname;

  return (
    <section className="ui-edit" aria-label="Hostname">
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
            label="Hostname"
            hint="Lowercase letters, digits and hyphens; a full name such as web2.example.org works too. The panel's address and the sites do not change."
            {...(!same && problem ? { error: problem } : {})}
          >
            <input className="ui-input" value={name} onChange={(e) => setName(e.target.value.trim())} autoComplete="off" spellCheck={false} />
          </Field>
        </div>
        {info.cloudInit && (
          <p className="ui-flush ui-hint">cloud-init is installed: Hostashi tells it to keep this name at the next boot.</p>
        )}
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy || same || problem !== ""}>
            Save hostname
          </Button>
          <Button disabled={busy} onClick={onCancel}>
            Cancel
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
  const [zone, setZone] = useState(info.timezone);
  const [busy, setBusy] = useState(false);
  const known = zones.length === 0 || zones.includes(zone);
  const same = zone === info.timezone;
  const preview = known && !same ? clockIn(info.time, zone) : "";

  return (
    <section className="ui-edit" aria-label="Time zone">
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
            label="Time zone"
            hint={preview ? `The server's clock would show ${preview}.` : "Type to search, for example Europe/Berlin."}
            {...(!known ? { error: "The server does not know this time zone." } : {})}
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
        <Notice tone="info" title="What follows the time zone">
          The sites' cron jobs run by the new time at once. MariaDB uses it after its next restart on the{" "}
          <a href={formatHash({ page: "services" })}>Services</a> page. PHP keeps its own <code>date.timezone</code> setting.
        </Notice>
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy || same || !known || zone === ""}>
            Save time zone
          </Button>
          <Button disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
        </div>
      </form>
    </section>
  );
}

function RebootCard({ info, busy, onReboot }: { info: SystemInfo; busy: boolean; onReboot: (confirm: string) => void }) {
  const [asking, setAsking] = useState(false);
  const [typed, setTyped] = useState("");

  return (
    <Card title="Reboot">
      <div className="ui-stack">
        <p className="ui-flush">
          Every site, the databases and this panel stop until the server has started again, usually within one or two minutes.
        </p>
        {!asking ? (
          <div className="ui-row">
            <Button variant="danger" disabled={busy} onClick={() => setAsking(true)}>
              <Power size={16} aria-hidden="true" /> Reboot server…
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
              Reboot {info.hostname}?
            </h3>
            <div className="ui-narrow">
              <Field
                label={
                  <>
                    Type <code className="ui-confirm-phrase">{info.hostname}</code> to confirm
                  </>
                }
              >
                <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
            </div>
            <div className="ui-row">
              <Button variant="danger" type="submit" disabled={busy || typed !== info.hostname}>
                Reboot now
              </Button>
              <Button
                onClick={() => {
                  setAsking(false);
                  setTyped("");
                }}
              >
                Cancel
              </Button>
            </div>
          </form>
        )}
      </div>
    </Card>
  );
}

function RebootProgress({ state }: { state: RebootState }) {
  switch (state.phase) {
    case "going-down":
      return <Notice tone="warn" title="The server is shutting down…">This page waits and reconnects by itself.</Notice>;
    case "starting":
      return <Notice tone="warn" title="The server is starting…">This page reconnects as soon as the panel answers.</Notice>;
    case "back":
      return <Notice tone="ok" title="The server is back." />;
    case "lost":
      return (
        <Notice tone="err" title="The panel has not answered for ten minutes.">
          Check the server in your hosting provider's console.
        </Notice>
      );
    default:
      return null;
  }
}
