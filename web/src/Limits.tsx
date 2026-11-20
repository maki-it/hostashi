// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, PencilSimple, Plus, Star, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { ApiError } from "./api";
import {
  bounds,
  createPreset,
  deletePreset,
  fieldsOf,
  formatBytes,
  formatCores,
  formatCount,
  getSiteLimits,
  limitsMessage,
  limitsOf,
  listPresets,
  presetNameProblem,
  priorityLabel,
  setSiteCustom,
  setSitePreset,
  updatePreset,
  type CPUPriority,
  type LimitFields,
  type Limits,
  type Preset,
  type SiteLimits,
  type Usage,
} from "./limits";
import { formatHash } from "./routes";
import type { Site } from "./sites";
import { Badge, Button, Card, IconButton, IconLink, Notice, PageHead, RowActions, Table, type Column, type Sort } from "./ui";

const defaultLimits: Limits = {
  diskBytes: 0,
  diskInodes: 0,
  memoryBytes: 0,
  phpWorkers: 5,
  tasks: 0,
  cpuPriority: "normal",
  cpuQuotaPercent: 0,
};

// messageOf shows a form's own check as it is and turns API errors into
// sentences.
const messageOf = (err: unknown) =>
  err instanceof Error && !(err instanceof ApiError) && !(err instanceof TypeError) ? err.message : limitsMessage(err, "preset");

const cpuCoresText = (percent: number) => {
  const c = formatCores(percent);
  return c === "1" ? "1 CPU core" : c + " CPU cores";
};

const Unlimited = () => <span className="badge warn">Unlimited</span>;

// LimitFacts lists limits; an unlimited one is marked, so it is never
// mistaken for a small number.
export function LimitFacts({ limits, label }: { limits: Limits; label: string }) {
  return (
    <dl className="facts" aria-label={label}>
      <dt>Disk space</dt>
      <dd>{limits.diskBytes ? formatBytes(limits.diskBytes) : <Unlimited />}</dd>
      <dt>Files</dt>
      <dd>{limits.diskInodes ? formatCount(limits.diskInodes) : <Unlimited />}</dd>
      <dt>Memory</dt>
      <dd>{limits.memoryBytes ? formatBytes(limits.memoryBytes) : <Unlimited />}</dd>
      <dt>Processes</dt>
      <dd>{limits.tasks ? formatCount(limits.tasks) : <Unlimited />}</dd>
      <dt>PHP workers</dt>
      <dd>{limits.phpWorkers}</dd>
      <dt>CPU</dt>
      <dd>
        {priorityLabel[limits.cpuPriority]} priority
        {limits.cpuQuotaPercent > 0 ? `, at most ${cpuCoresText(limits.cpuQuotaPercent)}` : ", no hard limit"}
      </dd>
    </dl>
  );
}

function UsageFacts({ usage, limits }: { usage: Usage; limits: Limits }) {
  if (!usage.enforced) {
    return <p className="muted">Disk limits are not enforced on this server: its file system has no project quota.</p>;
  }
  const badge =
    usage.level === "critical" ? (
      <span className="badge warn">Almost full</span>
    ) : usage.level === "warning" ? (
      <span className="badge warn">Filling up</span>
    ) : null;
  return (
    <>
      <dl className="facts" aria-label="Disk use">
        <dt>Disk use</dt>
        <dd>
          {formatBytes(usage.bytes)}
          {limits.diskBytes > 0 && ` of ${formatBytes(limits.diskBytes)} (${String(usage.bytesPercent)} %)`} {badge}
        </dd>
        <dt>Files</dt>
        <dd>
          {formatCount(usage.inodes)}
          {limits.diskInodes > 0 && ` of ${formatCount(limits.diskInodes)} (${String(usage.inodesPercent)} %)`}
        </dd>
      </dl>
      {usage.level === "critical" && (
        <p role="alert">The site has used 95 % or more of a disk limit. When it is full, writing new files fails.</p>
      )}
    </>
  );
}

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
  const row = (
    label: string,
    unit: string,
    value: string,
    onValue: (v: string) => void,
    unlimited: boolean,
    onUnlimited: (v: boolean) => void,
  ) => (
    <div className="limit-row">
      <label>
        {label}
        {unit && <small>{unit}</small>}
        <input inputMode="numeric" value={unlimited ? "" : value} disabled={unlimited} onChange={(e) => onValue(e.target.value)} />
      </label>
      {(allowUnlimited || unlimited) && (
        <label className="check">
          <input type="checkbox" checked={unlimited} onChange={(e) => onUnlimited(e.target.checked)} />
          Unlimited {label.toLowerCase()}
        </label>
      )}
    </div>
  );
  return (
    <>
      {row(
        "Disk space",
        "GiB",
        f.diskGiB,
        (v) => set({ ...f, diskGiB: v }),
        f.diskUnlimited,
        (v) => set({ ...f, diskUnlimited: v }),
      )}
      {row(
        "Files",
        "files, directories and links",
        f.inodes,
        (v) => set({ ...f, inodes: v }),
        f.inodesUnlimited,
        (v) => set({ ...f, inodesUnlimited: v }),
      )}
      {row(
        "Memory",
        "MiB for PHP, cron jobs and other processes of the site",
        f.memoryMiB,
        (v) => set({ ...f, memoryMiB: v }),
        f.memoryUnlimited,
        (v) => set({ ...f, memoryUnlimited: v }),
      )}
      {row(
        "Processes",
        "",
        f.tasks,
        (v) => set({ ...f, tasks: v }),
        f.tasksUnlimited,
        (v) => set({ ...f, tasksUnlimited: v }),
      )}
      <label>
        PHP workers
        <small>
          Requests the site's PHP handles at the same time, {bounds.workers.min} to {bounds.workers.max}
        </small>
        <input inputMode="numeric" value={f.workers} onChange={(e) => set({ ...f, workers: e.target.value })} />
      </label>
      <label>
        CPU priority
        <small>The share of the CPU the site gets when the server is busy</small>
        <select value={f.cpuPriority} onChange={(e) => set({ ...f, cpuPriority: e.target.value as CPUPriority })}>
          <option value="low">Low</option>
          <option value="normal">Normal</option>
          <option value="high">High</option>
        </select>
      </label>
      <label>
        CPU cores
        <small>
          Hard limit, such as 2 or 0.5; 0 for none.
          {cpus ? ` This server has ${String(cpus)} ${cpus === 1 ? "core" : "cores"}.` : ""}
        </small>
        <input inputMode="decimal" value={f.cpuCores} onChange={(e) => set({ ...f, cpuCores: e.target.value })} />
      </label>
    </>
  );
}

const unlimitedWarning = (f: LimitFields) =>
  f.diskUnlimited || f.inodesUnlimited || f.memoryUnlimited || f.tasksUnlimited ? (
    <p className="muted">
      An unlimited value lets this site fill the disk or take the memory of the whole server. Use it only for a site you trust.
    </p>
  ) : null;

// LimitSettings shows a site's limits and disk use and lets an admin give
// it a preset or custom values.
export function LimitSettings({ site }: { site: Site }) {
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
      setState({ ...s, ...(state?.usage ? { usage: state.usage } : {}) });
      setDone(custom ? "Custom limits saved." : `${site.name} now uses the preset ${choice}.`);
      await load();
    } catch (err) {
      setError(limitsMessage(err));
      await load();
    } finally {
      setBusy(false);
    }
  }

  return (
    <fieldset>
      <legend>Resource limits of {site.name}</legend>
      {!state ? (
        loadError ? (
          <p role="alert">{loadError}</p>
        ) : (
          <p aria-busy="true">Loading limits…</p>
        )
      ) : (
        <>
          {state.usage && <UsageFacts usage={state.usage} limits={state.set ? state.limits : defaultLimits} />}
          {state.set ? (
            <>
              <p>{state.preset ? `Preset: ${state.preset}` : "Custom values"}</p>
              <LimitFacts limits={state.limits} label={"Limits of " + site.name} />
              {state.applyError && (
                <p role="alert">The limits are saved but could not be applied on the server: {state.applyError}. Save again to retry.</p>
              )}
            </>
          ) : (
            <p className="muted">This site has no limits yet: it was created before limits existed. Choose a preset to limit it.</p>
          )}
          <form onSubmit={save} noValidate>
            <label>
              Limits
              <select value={choice} onChange={(e) => setChoice(e.target.value)}>
                {presets.map((p) => (
                  <option key={p.name} value={p.name}>
                    Preset {p.name}
                    {p.default ? " (default)" : ""}
                  </option>
                ))}
                <option value="">Custom values</option>
              </select>
            </label>
            {choice === "" ? (
              <>
                <LimitInputs f={fields} set={setFields} allowUnlimited cpus={cpus} />
                {unlimitedWarning(fields)}
              </>
            ) : (
              // The values of the current preset are shown above already.
              presets
                .filter((p) => p.name === choice && !(state.set && state.preset === choice))
                .map((p) => <LimitFacts key={p.name} limits={p.limits} label={"Values of preset " + p.name} />)
            )}
            <div className="actions">
              <button type="submit" disabled={busy}>
                Save limits
              </button>
            </div>
          </form>
          {done && <p role="status">{done}</p>}
          {error && <p role="alert">{error}</p>}
        </>
      )}
    </fieldset>
  );
}

type QueryProps = {
  /** The address query: q, sort, dir; new=1 or edit=<name> for the form. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

const limitCell = (v: number, text: (v: number) => string) => (v ? text(v) : <Badge tone="warn">Unlimited</Badge>);

// Presets is the page of the limit presets (ADR-0037): a table that can be
// sorted and filtered, with the CPU limit in cores, and a form for a new
// preset (?new=1) or a change (?edit=<name>).
export function Presets({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
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
      All presets
    </a>
  );

  if (!presets) {
    return (
      <>
        <PageHead title="Limit presets" />
        {loadError ? <Notice tone="err" title={loadError} /> : <p aria-busy="true">Loading presets…</p>}
      </>
    );
  }

  const editName = query.get("edit");
  if (query.get("new") === "1" || editName !== null) {
    const initial = editName !== null ? presets.find((p) => p.name === editName) : undefined;
    if (editName !== null && !initial) {
      return (
        <>
          <PageHead title="Limit presets" actions={listLink} />
          <Notice tone="warn" title={`There is no preset named ${editName}.`} />
        </>
      );
    }
    const title = initial ? "Change preset " + initial.name : "New preset";
    return (
      <>
        <PageHead
          title={title}
          description={initial ? "Sites that use this preset get the new values." : "A preset is a named set of limits for sites."}
          actions={listLink}
        />
        <Card>
          <PresetForm
            key={editName ?? "new"}
            title={title}
            submitLabel={initial ? "Save preset" : "Add preset"}
            cpus={cpus}
            onCancel={() => set({ new: "", edit: "" })}
            {...(initial ? { initial } : {})}
            onSubmit={async (name, isDefault, limits) => {
              if (initial) {
                await updatePreset(initial.name, name, isDefault, limits);
                await changed(`Preset ${name} saved. Sites that use it get the new values.`);
              } else {
                await createPreset(name, isDefault, limits);
                await changed(`Preset ${name} added.`);
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
      header: "Preset",
      sortValue: (p) => p.name,
      cell: (p) => (
        <span className="ui-row ui-nowrap">
          <strong>{p.name}</strong>
          {p.default && <Badge tone="ok">Default</Badge>}
        </span>
      ),
    },
    {
      key: "disk",
      header: "Disk space",
      sortValue: (p) => p.limits.diskBytes || Infinity,
      cell: (p) => (
        <div className="ui-cell-main">
          <span className="ui-row">{limitCell(p.limits.diskBytes, formatBytes)}</span>
          {p.limits.diskInodes ? <span>{formatCount(p.limits.diskInodes)} files</span> : <span>unlimited files</span>}
        </div>
      ),
    },
    { key: "memory", header: "Memory", sortValue: (p) => p.limits.memoryBytes || Infinity, cell: (p) => limitCell(p.limits.memoryBytes, formatBytes) },
    { key: "tasks", header: "Processes", sortValue: (p) => p.limits.tasks || Infinity, cell: (p) => limitCell(p.limits.tasks, formatCount) },
    { key: "workers", header: "PHP workers", sortValue: (p) => p.limits.phpWorkers, cell: (p) => p.limits.phpWorkers },
    {
      key: "cpu",
      header: "CPU cores",
      sortValue: (p) => p.limits.cpuQuotaPercent || Infinity,
      cell: (p) => (p.limits.cpuQuotaPercent ? formatCores(p.limits.cpuQuotaPercent) : <span className="muted">No limit</span>),
    },
    {
      key: "priority",
      header: "CPU priority",
      sortValue: (p) => ["low", "normal", "high"].indexOf(p.limits.cpuPriority),
      cell: (p) => priorityLabel[p.limits.cpuPriority],
    },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (p) => (
        <RowActions>
          {!p.default && (
            <IconButton
              label={`Make ${p.name} the default`}
              icon={<Star size={16} />}
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  const l = limitsOf(fieldsOf(p.limits), true);
                  if (typeof l === "string") throw new Error(l);
                  await updatePreset(p.name, p.name, true, p.limits);
                  return `New sites now get the preset ${p.name}.`;
                })
              }
            />
          )}
          <IconLink label={`Change ${p.name}`} icon={<PencilSimple size={16} />} href={formatHash({ page: "limits", query: new URLSearchParams({ edit: p.name }) })} />
          {!p.default && (
            <IconButton
              label={`Delete ${p.name}`}
              icon={<Trash size={16} />}
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
        title="Limit presets"
        description="A new site gets the default preset. Changing a preset changes every site that uses it; a site with custom values stays as it is."
        actions={
          <a className="ui-btn ui-btn-primary" href={formatHash({ page: "limits", query: new URLSearchParams({ new: "1" }) })}>
            <Plus size={16} aria-hidden="true" />
            New preset
          </a>
        }
      />
      {loadError && <Notice tone="err" title={loadError} />}
      {done && <Notice tone="ok" title={done} />}
      {deleting && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="preset-delete-title">
          <h2 id="preset-delete-title" className="ui-flush">
            Delete the preset {deleting}?
          </h2>
          <p className="ui-flush">Its sites keep the values as custom values.</p>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button
              variant="danger"
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await deletePreset(deleting);
                  return `Preset ${deleting} deleted.`;
                })
              }
            >
              Delete preset
            </Button>
            <Button disabled={busy} onClick={() => setDeleting(null)}>
              Cancel
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
            aria-label="Filter presets"
            placeholder="Filter by name"
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          {cpus !== undefined && (
            <p className="ui-hint ui-filters-note">
              This server has {cpus} CPU {cpus === 1 ? "core" : "cores"}.
            </p>
          )}
        </div>
        <Table
          label="Limit presets"
          columns={columns}
          rows={shown}
          rowKey={(p) => p.name}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty={presets.length === 0 ? "No presets yet." : "No preset matches the filter."}
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
  onSubmit: (name: string, isDefault: boolean, limits: Limits) => Promise<void>;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
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
    setBusy(true);
    setError("");
    try {
      await onSubmit(name, isDefault, l);
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} noValidate aria-label={title}>
      <label>
        Name
        <small>Lower-case letters, digits and dashes</small>
        <input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" spellCheck={false} />
      </label>
      {!initial?.default && (
        <label className="check">
          <input type="checkbox" checked={isDefault} onChange={(e) => setDefault(e.target.checked)} />
          Default for new sites
        </label>
      )}
      <LimitInputs f={fields} set={setFields} allowUnlimited={!isDefault && !initial?.default} cpus={cpus} />
      {unlimitedWarning(fields)}
      <div className="actions">
        <button type="submit" disabled={busy}>
          {submitLabel}
        </button>
        <button type="button" className="secondary" disabled={busy} onClick={onCancel}>
          Cancel
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
    </form>
  );
}
