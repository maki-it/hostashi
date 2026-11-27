// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, PencilSimple, Plus, Star, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { ApiError } from "./api";
import i18n from "./i18n";
import {
  bounds,
  createPreset,
  deletePreset,
  displayCores,
  fieldsOf,
  formatBytes,
  formatCount,
  getSiteLimits,
  limitsMessage,
  limitsOf,
  listPresets,
  noCaches,
  pageCacheSizes,
  presetCacheProblem,
  presetNameProblem,
  priorityLabel,
  setSiteCustom,
  setSitePreset,
  updatePreset,
  type CPUPriority,
  type LimitFields,
  type Limits,
  type Preset,
  type PresetCaches,
  type DatabaseUsage,
  type SiteLimits,
  type Usage,
} from "./limits";
import { cacheSizes } from "./objcache";
import { formatSize } from "./pagecache";
import { formatHash } from "./routes";
import type { Site } from "./sites";
import { Badge, Button, Card, Field, IconButton, IconLink, Notice, PageHead, RelTime, RowActions, Table, UnsavedNote, type Column, type Sort } from "./ui";

const defaultLimits: Limits = {
  diskBytes: 0,
  diskInodes: 0,
  memoryBytes: 0,
  phpWorkers: 5,
  tasks: 0,
  cpuPriority: "normal",
  cpuQuotaPercent: 0,
  databaseBytes: 0,
  databaseConnections: 0,
};

// messageOf shows a form's own check as it is and turns API errors into
// sentences.
const messageOf = (err: unknown) =>
  err instanceof Error && !(err instanceof ApiError) && !(err instanceof TypeError) ? err.message : limitsMessage(err, "preset");

const cpuCoresText = (percent: number) => i18n.t("limits.cpuCores", { count: Math.round(percent) / 100, cores: displayCores(percent) });

const Unlimited = () => {
  const { t } = useTranslation();
  return <Badge tone="warn">{t("limits.unlimited")}</Badge>;
};

// LimitFacts lists limits; an unlimited one is marked, so it is never
// mistaken for a small number.
export function LimitFacts({ limits, label }: { limits: Limits; label: string }) {
  const { t } = useTranslation();
  return (
    <dl className="ui-facts" aria-label={label}>
      <dt>{t("limits.facts.disk")}</dt>
      <dd>{limits.diskBytes ? formatBytes(limits.diskBytes) : <Unlimited />}</dd>
      <dt>{t("limits.facts.files")}</dt>
      <dd>{limits.diskInodes ? formatCount(limits.diskInodes) : <Unlimited />}</dd>
      <dt>{t("limits.facts.memory")}</dt>
      <dd>{limits.memoryBytes ? formatBytes(limits.memoryBytes) : <Unlimited />}</dd>
      <dt>{t("limits.facts.processes")}</dt>
      <dd>{limits.tasks ? formatCount(limits.tasks) : <Unlimited />}</dd>
      <dt>{t("limits.facts.workers")}</dt>
      <dd>{limits.phpWorkers}</dd>
      <dt>{t("limits.facts.cpu")}</dt>
      <dd>
        {t("limits.facts.priority", { priority: priorityLabel(limits.cpuPriority) })}
        {limits.cpuQuotaPercent > 0 ? t("limits.facts.atMost", { cores: cpuCoresText(limits.cpuQuotaPercent) }) : t("limits.facts.noHardLimit")}
      </dd>
      <dt>{t("limits.facts.dbSize")}</dt>
      <dd>{limits.databaseBytes ? t("limits.facts.warnAt", { size: formatBytes(limits.databaseBytes) }) : <Badge tone="off">{t("limits.facts.noWarning")}</Badge>}</dd>
      <dt>{t("limits.facts.dbConnections")}</dt>
      <dd>{limits.databaseConnections ? t("limits.facts.perUser", { n: formatCount(limits.databaseConnections) }) : <Unlimited />}</dd>
    </dl>
  );
}

// UsageFacts shows the disk use and, when measured, the size of the site's
// local databases (ADR-0057) in one list.
function UsageFacts({ usage, db, limits }: { usage?: Usage | undefined; db?: DatabaseUsage | undefined; limits: Limits }) {
  const { t } = useTranslation();
  if (!usage?.enforced) {
    return (
      <>
        {usage && <p className="ui-hint ui-flush">{t("limits.usage.notEnforced")}</p>}
        {db && (
          <dl className="ui-facts" aria-label={t("limits.usage.label")}>
            {databaseRows(db, limits)}
          </dl>
        )}
        {databaseNotice(db)}
      </>
    );
  }
  const badge =
    usage.level === "critical" ? (
      <Badge tone="warn">{t("limits.usage.almostFull")}</Badge>
    ) : usage.level === "warning" ? (
      <Badge tone="warn">{t("limits.usage.fillingUp")}</Badge>
    ) : null;
  return (
    <>
      <dl className="ui-facts" aria-label={t("limits.usage.label")}>
        <dt>{t("limits.usage.disk")}</dt>
        <dd>
          {formatBytes(usage.bytes)}
          {limits.diskBytes > 0 && t("limits.usage.ofLimit", { limit: formatBytes(limits.diskBytes), percent: formatCount(usage.bytesPercent) })} {badge}
        </dd>
        <dt>{t("limits.usage.files")}</dt>
        <dd>
          {formatCount(usage.inodes)}
          {limits.diskInodes > 0 && t("limits.usage.ofLimit", { limit: formatCount(limits.diskInodes), percent: formatCount(usage.inodesPercent) })}
        </dd>
        {db && databaseRows(db, limits)}
      </dl>
      {databaseNotice(db)}
      {usage.level === "critical" && (
        <Notice tone="err" title={t("limits.usage.full")} />
      )}
    </>
  );
}

// databaseRows shows the size of the site's local databases against the
// warning threshold (ADR-0057).
function databaseRows(usage: DatabaseUsage, limits: Limits) {
  const badge =
    usage.level === "critical" ? (
      <Badge tone="warn">{i18n.t("limits.usage.aboveWarning")}</Badge>
    ) : usage.level === "warning" ? (
      <Badge tone="warn">{i18n.t("limits.usage.growing")}</Badge>
    ) : null;
  return (
    <>
      <dt>{i18n.t("limits.usage.databases")}</dt>
      <dd>
        {usage.databases === 0 ? (
          <span className="ui-hint">{i18n.t("limits.usage.noDatabases")}</span>
        ) : (
          <>
            {formatBytes(usage.bytes)}
            {limits.databaseBytes > 0 && i18n.t("limits.usage.ofLimit", { limit: formatBytes(limits.databaseBytes), percent: formatCount(usage.percent) })} {badge}
            <span className="ui-hint">
              {" "}
              <Trans
                i18nKey="limits.usage.measured"
                count={usage.databases}
                components={{ time: <RelTime iso={new Date(usage.measuredAt * 1000).toISOString()} /> }}
              />
            </span>
          </>
        )}
      </dd>
    </>
  );
}

// databaseNotice: nothing stops a database at the threshold, so the notice
// says what to do instead.
const databaseNotice = (usage: DatabaseUsage | undefined) =>
  usage?.level === "critical" ? (
    <Notice
      tone="warn"
      title={i18n.t("limits.usage.dbFull")}
    />
  ) : null;

// LimitInputs edits the fields of a limit set. Unlimited is a separate
// checkbox per limit, so it is always an explicit choice.
function LimitInputs({
  f,
  set,
  allowUnlimited,
  cpus,
}: {
  f: LimitFields;
  set: (f: LimitFields) => void;
  allowUnlimited: boolean;
  /** The server's CPU cores, when known. */
  cpus?: number | undefined;
}) {
  const { t } = useTranslation();
  const row = (
    label: string,
    unit: string,
    unlimitedLabel: string,
    value: string,
    onValue: (v: string) => void,
    unlimited: boolean,
    onUnlimited: (v: boolean) => void,
  ) => (
    <div className="ui-stack-tight">
      <Field label={label} {...(unit ? { hint: unit } : {})}>
        <input className="ui-input" inputMode="numeric" value={unlimited ? "" : value} disabled={unlimited} onChange={(e) => onValue(e.target.value)} />
      </Field>
      {(allowUnlimited || unlimited) && (
        <label className="ui-check">
          <input type="checkbox" checked={unlimited} onChange={(e) => onUnlimited(e.target.checked)} />
          {unlimitedLabel}
        </label>
      )}
    </div>
  );
  return (
    <>
      <fieldset className="ui-fieldset ui-group ui-stack">
        <legend>{t("limits.inputs.storage")}</legend>
        <div className="ui-form-grid">
          {row(
            t("limits.facts.disk"),
            "GiB",
            t("limits.inputs.diskUnlimited"),
            f.diskGiB,
            (v) => set({ ...f, diskGiB: v }),
            f.diskUnlimited,
            (v) => set({ ...f, diskUnlimited: v }),
          )}
          {row(
            t("limits.facts.files"),
            t("limits.inputs.filesHint"),
            t("limits.inputs.filesUnlimited"),
            f.inodes,
            (v) => set({ ...f, inodes: v }),
            f.inodesUnlimited,
            (v) => set({ ...f, inodesUnlimited: v }),
          )}
        </div>
      </fieldset>
      <fieldset className="ui-fieldset ui-group ui-stack">
        <legend>{t("limits.inputs.memoryProcesses")}</legend>
        <div className="ui-form-grid">
          {row(
            t("limits.facts.memory"),
            t("limits.inputs.memoryHint"),
            t("limits.inputs.memoryUnlimited"),
            f.memoryMiB,
            (v) => set({ ...f, memoryMiB: v }),
            f.memoryUnlimited,
            (v) => set({ ...f, memoryUnlimited: v }),
          )}
          {row(
            t("limits.facts.processes"),
            "",
            t("limits.inputs.processesUnlimited"),
            f.tasks,
            (v) => set({ ...f, tasks: v }),
            f.tasksUnlimited,
            (v) => set({ ...f, tasksUnlimited: v }),
          )}
          <Field label={t("limits.facts.workers")} hint={t("limits.inputs.workersHint", { min: bounds.workers.min, max: bounds.workers.max })}>
            <input className="ui-input" inputMode="numeric" value={f.workers} onChange={(e) => set({ ...f, workers: e.target.value })} />
          </Field>
        </div>
      </fieldset>
      <fieldset className="ui-fieldset ui-group ui-stack">
        <legend>{t("limits.inputs.cpu")}</legend>
        <div className="ui-form-grid">
          <Field label={t("limits.inputs.priority")} hint={t("limits.inputs.priorityHint")}>
            <select className="ui-select" value={f.cpuPriority} onChange={(e) => set({ ...f, cpuPriority: e.target.value as CPUPriority })}>
              <option value="low">{priorityLabel("low")}</option>
              <option value="normal">{priorityLabel("normal")}</option>
              <option value="high">{priorityLabel("high")}</option>
            </select>
          </Field>
          <Field
            label={t("limits.inputs.cores")}
            hint={t("limits.inputs.coresHint") + (cpus ? " " + t("limits.inputs.serverCores", { count: cpus }) : "")}
          >
            <input className="ui-input" inputMode="decimal" value={f.cpuCores} onChange={(e) => set({ ...f, cpuCores: e.target.value })} />
          </Field>
        </div>
      </fieldset>
      <fieldset className="ui-fieldset ui-group ui-stack">
        <legend>{t("limits.inputs.database")}</legend>
        <div className="ui-form-grid">
          {row(
            t("limits.facts.dbSize"),
            t("limits.inputs.dbSizeHint"),
            t("limits.inputs.dbSizeUnlimited"),
            f.dbGiB,
            (v) => set({ ...f, dbGiB: v }),
            f.dbUnlimited,
            (v) => set({ ...f, dbUnlimited: v }),
          )}
          {row(
            t("limits.facts.dbConnections"),
            t("limits.inputs.dbConnectionsHint"),
            t("limits.inputs.dbConnectionsUnlimited"),
            f.dbConnections,
            (v) => set({ ...f, dbConnections: v }),
            f.dbConnectionsUnlimited,
            (v) => set({ ...f, dbConnectionsUnlimited: v }),
          )}
        </div>
      </fieldset>
    </>
  );
}

const unlimitedWarning = (f: LimitFields) =>
  f.diskUnlimited || f.inodesUnlimited || f.memoryUnlimited || f.tasksUnlimited ? (
    <p className="ui-hint ui-flush">{i18n.t("limits.inputs.unlimitedWarning")}</p>
  ) : null;

// LimitSettings shows a site's limits and disk use and lets an admin give
// it a preset or custom values.
export function LimitSettings({ site }: { site: Site }) {
  const { t } = useTranslation();
  const [state, setState] = useState<SiteLimits | null>(null);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [cpus, setCPUs] = useState<number | undefined>(undefined);
  const [loadError, setLoadError] = useState("");
  const [choice, setChoice] = useState(""); // a preset name or "" for custom values
  const [fields, setFields] = useState<LimitFields>(fieldsOf(defaultLimits));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      const [s, list] = await Promise.all([getSiteLimits(site.name), listPresets()]);
      const p = list.presets;
      setState(s);
      setPresets(p);
      setCPUs(list.cpus);
      setChoice(s.set ? s.preset : (p.find((x) => x.default)?.name ?? ""));
      setFields(fieldsOf(s.set ? s.limits : (p.find((x) => x.default)?.limits ?? defaultLimits)));
      setLoadError("");
    } catch (err) {
      setLoadError(limitsMessage(err));
    }
  }, [site.name]);

  useEffect(() => {
    void load();
  }, [load]);

  async function save(e: FormEvent) {
    e.preventDefault();
    setError("");
    setDone("");
    let custom: Limits | null = null;
    if (choice === "") {
      const l = limitsOf(fields);
      if (typeof l === "string") {
        setError(l);
        return;
      }
      custom = l;
    }
    setBusy(true);
    try {
      const s = custom ? await setSiteCustom(site.name, custom) : await setSitePreset(site.name, choice);
      setState({ ...s, ...(state?.usage ? { usage: state.usage } : {}), ...(state?.databaseUsage ? { databaseUsage: state.databaseUsage } : {}) });
      setDone(custom ? t("limits.site.customSaved") : t("limits.site.nowUses", { site: site.name, preset: choice }));
      await load();
    } catch (err) {
      setError(limitsMessage(err));
      await load();
    } finally {
      setBusy(false);
    }
  }

  // Saving needs a change, except for a site without limits yet or limits
  // that did not apply, where saving again is the fix.
  const savedChoice = state?.set ? state.preset : null;
  const dirty =
    !state?.set ||
    Boolean(state.applyError) ||
    choice !== savedChoice ||
    (choice === "" && JSON.stringify(fields) !== JSON.stringify(fieldsOf(state.limits)));

  return (
    <Card title={t("limits.site.card")}>
      <div className="ui-stack">
        {!state ? (
          loadError ? (
            <Notice tone="err" title={loadError} />
          ) : (
            <p aria-busy="true">{t("limits.site.loading")}</p>
          )
        ) : (
          <>
            {(state.usage || state.databaseUsage) && (
              <UsageFacts usage={state.usage} db={state.databaseUsage} limits={state.set ? state.limits : defaultLimits} />
            )}
            {state.set ? (
              <>
                <p className="ui-flush">{state.preset ? t("limits.site.preset", { name: state.preset }) : t("limits.site.custom")}</p>
                <LimitFacts limits={state.limits} label={t("limits.site.limitsOf", { site: site.name })} />
                {state.applyError && (
                  <Notice tone="err" title={t("limits.site.applyError", { error: state.applyError })} />
                )}
              </>
            ) : (
              <p className="ui-hint ui-flush">{t("limits.site.notSet")}</p>
            )}
            <form className="ui-stack" onSubmit={save} noValidate>
              <Field label={t("limits.site.field")}>
                <select className="ui-select" value={choice} onChange={(e) => setChoice(e.target.value)}>
                  {presets.map((p) => (
                    <option key={p.name} value={p.name}>
                      {p.default ? t("limits.site.optionDefault", { name: p.name }) : t("limits.site.option", { name: p.name })}
                    </option>
                  ))}
                  <option value="">{t("limits.site.custom")}</option>
                </select>
              </Field>
              {choice === "" ? (
                <>
                  <LimitInputs f={fields} set={setFields} allowUnlimited cpus={cpus} />
                  {unlimitedWarning(fields)}
                </>
              ) : (
                // The values of the current preset are shown above already.
                presets
                  .filter((p) => p.name === choice && !(state.set && state.preset === choice))
                  .map((p) => <LimitFacts key={p.name} limits={p.limits} label={t("limits.site.valuesOf", { name: p.name })} />)
              )}
              <div className="ui-row">
                <Button variant="primary" type="submit" disabled={busy || !dirty}>
                  {t("limits.site.save")}
                </Button>
                <UnsavedNote dirty={dirty} />
              </div>
            </form>
            {done && <Notice tone="ok" title={done} />}
            {error && <Notice tone="err" title={error} />}
          </>
        )}
      </div>
    </Card>
  );
}

type QueryProps = {
  /** The address query: q, sort, dir; new=1 or edit=<name> for the form. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

const limitCell = (v: number, text: (v: number) => string) => (v ? text(v) : <Unlimited />);

// Presets is the page of the limit presets (ADR-0037): a table that can be
// sorted and filtered, with the CPU limit in cores, and a form for a new
// preset (?new=1) or a change (?edit=<name>).
export function Presets({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const { t } = useTranslation();
  const [presets, setPresets] = useState<Preset[] | null>(null);
  const [cpus, setCPUs] = useState<number | undefined>(undefined);
  const [loadError, setLoadError] = useState("");
  const [done, setDone] = useState("");
  const [deleting, setDeleting] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const list = await listPresets();
      setPresets(list.presets);
      setCPUs(list.cpus);
      setLoadError("");
    } catch (err) {
      setLoadError(limitsMessage(err, "preset"));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  const changed = async (msg: string) => {
    setDone(msg);
    set({ new: "", edit: "" });
    await load();
  };

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const msg = await f();
      setDeleting(null);
      await changed(msg);
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }

  const listLink = (
    <a className="ui-btn" href={formatHash({ page: "limits" })}>
      <ArrowLeft size={16} aria-hidden="true" />
      {t("limits.presets.allLink")}
    </a>
  );

  if (!presets) {
    return (
      <>
        <PageHead title={t("limits.presets.title")} />
        {loadError ? <Notice tone="err" title={loadError} /> : <p aria-busy="true">{t("limits.presets.loading")}</p>}
      </>
    );
  }

  const editName = query.get("edit");
  if (query.get("new") === "1" || editName !== null) {
    const initial = editName !== null ? presets.find((p) => p.name === editName) : undefined;
    if (editName !== null && !initial) {
      return (
        <>
          <PageHead title={t("limits.presets.title")} actions={listLink} />
          <Notice tone="warn" title={t("limits.presets.noSuch", { name: editName })} />
        </>
      );
    }
    const title = initial ? t("limits.presets.change", { name: initial.name }) : t("limits.presets.new");
    return (
      <>
        <PageHead
          title={title}
          description={initial ? t("limits.presets.changeDescription") : t("limits.presets.newDescription")}
          actions={listLink}
        />
        <Card>
          <PresetForm
            key={editName ?? "new"}
            title={title}
            submitLabel={initial ? t("limits.presets.save") : t("limits.presets.add")}
            cpus={cpus}
            onCancel={() => set({ new: "", edit: "" })}
            {...(initial ? { initial } : {})}
            onSubmit={async (name, isDefault, limits, caches) => {
              if (initial) {
                await updatePreset(initial.name, name, isDefault, limits, caches);
                await changed(t("limits.presets.saved", { name }));
              } else {
                await createPreset(name, isDefault, limits, caches);
                await changed(t("limits.presets.added", { name }));
              }
            }}
          />
        </Card>
      </>
    );
  }

  const columns: Column<Preset>[] = [
    {
      key: "name",
      header: t("limits.presets.columns.name"),
      sortValue: (p) => p.name,
      cell: (p) => (
        <span className="ui-row ui-nowrap">
          <strong>{p.name}</strong>
          {p.default && <Badge tone="ok">{t("limits.presets.default")}</Badge>}
        </span>
      ),
    },
    {
      key: "disk",
      header: t("limits.presets.columns.disk"),
      sortValue: (p) => p.limits.diskBytes || Infinity,
      cell: (p) => (
        <div className="ui-cell-main">
          <span className="ui-row">{limitCell(p.limits.diskBytes, formatBytes)}</span>
          {p.limits.diskInodes ? (
            <span>{t("limits.presets.files", { count: p.limits.diskInodes, n: formatCount(p.limits.diskInodes) })}</span>
          ) : (
            <span>{t("limits.presets.unlimitedFiles")}</span>
          )}
        </div>
      ),
    },
    { key: "memory", header: t("limits.presets.columns.memory"), sortValue: (p) => p.limits.memoryBytes || Infinity, cell: (p) => limitCell(p.limits.memoryBytes, formatBytes) },
    { key: "tasks", header: t("limits.presets.columns.processes"), sortValue: (p) => p.limits.tasks || Infinity, cell: (p) => limitCell(p.limits.tasks, formatCount) },
    { key: "workers", header: t("limits.presets.columns.workers"), sortValue: (p) => p.limits.phpWorkers, cell: (p) => p.limits.phpWorkers },
    {
      key: "cpu",
      header: t("limits.presets.columns.cores"),
      sortValue: (p) => p.limits.cpuQuotaPercent || Infinity,
      cell: (p) => (p.limits.cpuQuotaPercent ? displayCores(p.limits.cpuQuotaPercent) : <span className="ui-hint">{t("limits.presets.noLimit")}</span>),
    },
    {
      key: "objectCache",
      header: t("limits.presets.columns.objectCache"),
      sortValue: (p) => p.caches?.objectCacheMb ?? 0,
      cell: (p) =>
        p.caches?.objectCacheMb ? t("limits.presets.mib", { n: formatCount(p.caches.objectCacheMb) }) : <span className="ui-hint">{t("limits.presets.off")}</span>,
    },
    {
      key: "pageCache",
      header: t("limits.presets.columns.pageCache"),
      sortValue: (p) => p.caches?.pageCacheMb ?? 0,
      cell: (p) => (p.caches?.pageCacheMb ? formatSize(p.caches.pageCacheMb) : <span className="ui-hint">{t("limits.presets.off")}</span>),
    },
    {
      key: "priority",
      header: t("limits.presets.columns.priority"),
      sortValue: (p) => ["low", "normal", "high"].indexOf(p.limits.cpuPriority),
      cell: (p) => priorityLabel(p.limits.cpuPriority),
    },
    {
      key: "actions",
      header: t("limits.presets.columns.actions"),
      srHeader: true,
      cell: (p) => (
        <RowActions>
          {!p.default && (
            <IconButton
              label={t("limits.presets.actions.makeDefaultLabel", { name: p.name })}
              text={t("limits.presets.actions.makeDefault")} icon={<Star size={16} />}
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  const l = limitsOf(fieldsOf(p.limits), true);
                  if (typeof l === "string") throw new Error(l);
                  await updatePreset(p.name, p.name, true, p.limits, p.caches ?? noCaches);
                  return t("limits.presets.madeDefault", { name: p.name });
                })
              }
            />
          )}
          <IconLink label={t("limits.presets.actions.changeLabel", { name: p.name })} text={t("limits.presets.actions.change")} icon={<PencilSimple size={16} />} href={formatHash({ page: "limits", query: new URLSearchParams({ edit: p.name }) })} />
          {!p.default && (
            <IconButton
              label={t("limits.presets.actions.deleteLabel", { name: p.name })}
              text={t("limits.presets.actions.delete")} icon={<Trash size={16} />}
              danger
              disabled={busy}
              onClick={() => {
                setError("");
                setDeleting(p.name);
              }}
            />
          )}
        </RowActions>
      ),
    },
  ];

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const shown = presets.filter((p) => !q || p.name.includes(q));
  const sortKey = query.get("sort");
  const sort: Sort | undefined = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : undefined;

  return (
    <>
      <PageHead
        title={t("limits.presets.title")}
        description={t("limits.presets.description")}
        actions={
          <a className="ui-btn ui-btn-primary" href={formatHash({ page: "limits", query: new URLSearchParams({ new: "1" }) })}>
            <Plus size={16} aria-hidden="true" />
            {t("limits.presets.new")}
          </a>
        }
      />
      {loadError && <Notice tone="err" title={loadError} />}
      {done && <Notice tone="ok" title={done} />}
      {deleting && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="preset-delete-title">
          <h2 id="preset-delete-title" className="ui-flush">
            {t("limits.presets.delete.title", { name: deleting })}
          </h2>
          <p className="ui-flush">{t("limits.presets.delete.text")}</p>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button
              variant="danger"
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await deletePreset(deleting);
                  return t("limits.presets.delete.done", { name: deleting });
                })
              }
            >
              {t("limits.presets.delete.confirm")}
            </Button>
            <Button disabled={busy} onClick={() => setDeleting(null)}>
              {t("common.actions.cancel")}
            </Button>
          </div>
        </section>
      )}
      {error && !deleting && <Notice tone="err" title={error} />}
      <Card padded={false}>
        <div className="ui-filters">
          <input
            className="ui-input"
            type="search"
            aria-label={t("limits.presets.filter.label")}
            placeholder={t("limits.presets.filter.placeholder")}
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          {cpus !== undefined && (
            <p className="ui-hint ui-filters-note">{t("limits.presets.cpus", { count: cpus })}</p>
          )}
        </div>
        <Table
          label={t("limits.presets.table.label")}
          actionsBelow
          columns={columns}
          rows={shown}
          rowKey={(p) => p.name}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty={presets.length === 0 ? t("limits.presets.table.none") : t("limits.presets.table.noMatch")}
        />
      </Card>
    </>
  );
}

function PresetForm({
  title,
  submitLabel,
  initial,
  cpus,
  onSubmit,
  onCancel,
}: {
  title: string;
  submitLabel: string;
  initial?: Preset;
  cpus?: number | undefined;
  onSubmit: (name: string, isDefault: boolean, limits: Limits, caches: PresetCaches) => Promise<void>;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(initial?.name ?? "");
  const [objectCacheMb, setObjectCacheMb] = useState(initial?.caches?.objectCacheMb ?? 0);
  const [pageCacheMb, setPageCacheMb] = useState(initial?.caches?.pageCacheMb ?? 0);
  const [isDefault, setDefault] = useState(initial?.default ?? false);
  const [fields, setFields] = useState<LimitFields>(
    fieldsOf(
      initial?.limits ?? {
        diskBytes: 10 * 1024 ** 3,
        diskInodes: 500000,
        memoryBytes: 512 * 1024 ** 2,
        phpWorkers: 5,
        tasks: 128,
        cpuPriority: "normal",
        cpuQuotaPercent: 0,
        databaseBytes: 2 * 1024 ** 3,
        databaseConnections: 20,
      },
    ),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = presetNameProblem(name);
    if (problem) {
      setError(problem);
      return;
    }
    const l = limitsOf(fields, isDefault);
    if (typeof l === "string") {
      setError(l);
      return;
    }
    const caches = { objectCacheMb, pageCacheMb };
    const cacheProblem = presetCacheProblem(caches, l.memoryBytes);
    if (cacheProblem) {
      setError(cacheProblem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      await onSubmit(name, isDefault, l, caches);
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={submit} noValidate aria-label={title}>
      <Field label={t("limits.form.name")} hint={t("limits.form.nameHint")}>
        <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" spellCheck={false} />
      </Field>
      {!initial?.default && (
        <label className="ui-check">
          <input type="checkbox" checked={isDefault} onChange={(e) => setDefault(e.target.checked)} />
          {t("limits.form.default")}
        </label>
      )}
      <LimitInputs f={fields} set={setFields} allowUnlimited={!isDefault && !initial?.default} cpus={cpus} />
      {unlimitedWarning(fields)}
      <fieldset className="ui-fieldset ui-group ui-stack">
        <legend>{t("limits.form.caches")}</legend>
        <p className="ui-hint">{t("limits.form.cachesHint")}</p>
        <div className="ui-form-grid">
            <Field label={t("limits.form.objectCache")} hint={t("limits.form.objectCacheHint")}>
              <select className="ui-select" value={objectCacheMb} onChange={(e) => setObjectCacheMb(Number(e.target.value))}>
                <option value={0}>{t("limits.form.off")}</option>
                {cacheSizes.map((n) => (
                  <option key={n} value={n}>
                    {t("limits.presets.mib", { n: formatCount(n) })}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("limits.form.pageCache")} hint={t("limits.form.pageCacheHint")}>
              <select className="ui-select" value={pageCacheMb} onChange={(e) => setPageCacheMb(Number(e.target.value))}>
                <option value={0}>{t("limits.form.off")}</option>
                {pageCacheSizes.map((n) => (
                  <option key={n} value={n}>
                    {formatSize(n)}
                  </option>
                ))}
              </select>
            </Field>
        </div>
      </fieldset>
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={busy}>
          {submitLabel}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          {t("common.actions.cancel")}
        </Button>
      </div>
      {error && <Notice tone="err" title={error} />}
    </form>
  );
}
