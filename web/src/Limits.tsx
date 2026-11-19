// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { ApiError } from "./api";
import {
  bounds,
  createPreset,
  deletePreset,
  fieldsOf,
  formatBytes,
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
import type { Site } from "./sites";

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
        {limits.cpuQuotaPercent > 0 ? `, at most ${String(limits.cpuQuotaPercent)} % of one core` : ", no hard limit"}
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
function LimitInputs({ f, set, allowUnlimited }: { f: LimitFields; set: (f: LimitFields) => void; allowUnlimited: boolean }) {
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
        Hard CPU limit
        <small>Percent of one core, 0 for none (100 = one full core)</small>
        <input inputMode="numeric" value={f.cpuQuota} onChange={(e) => set({ ...f, cpuQuota: e.target.value })} />
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
  const [loadError, setLoadError] = useState("");
  const [choice, setChoice] = useState(""); // a preset name or "" for custom values
  const [fields, setFields] = useState<LimitFields>(fieldsOf(defaultLimits));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      const [s, p] = await Promise.all([getSiteLimits(site.name), listPresets()]);
      setState(s);
      setPresets(p);
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
                <LimitInputs f={fields} set={setFields} allowUnlimited />
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

// Presets is the page of the limit presets.
export function Presets() {
  const [presets, setPresets] = useState<Preset[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const [adding, setAdding] = useState(false);
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      setPresets(await listPresets());
      setLoadError("");
    } catch (err) {
      setLoadError(limitsMessage(err, "preset"));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const changed = async (msg: string) => {
    setDone(msg);
    setAdding(false);
    await load();
  };

  return (
    <section className="card">
      <h2>Limit presets</h2>
      <p className="muted">
        A new site gets the default preset. Changing a preset changes every site that uses it; a site with custom values stays as it is.
      </p>
      {!presets ? (
        loadError ? (
          <p role="alert">{loadError}</p>
        ) : (
          <p aria-busy="true">Loading presets…</p>
        )
      ) : (
        <>
          {loadError && <p role="alert">{loadError}</p>}
          <ul className="jobs" aria-label="Limit presets">
            {presets.map((p) => (
              <PresetRow key={p.name} preset={p} onChange={changed} />
            ))}
          </ul>
          {done && <p role="status">{done}</p>}
          {adding ? (
            <PresetForm
              title="New preset"
              submitLabel="Add preset"
              onCancel={() => setAdding(false)}
              onSubmit={async (name, isDefault, limits) => {
                await createPreset(name, isDefault, limits);
                await changed(`Preset ${name} added.`);
              }}
            />
          ) : (
            <div className="actions">
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  setDone("");
                  setAdding(true);
                }}
              >
                Add preset
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}

function PresetRow({ preset, onChange }: { preset: Preset; onChange: (msg: string) => Promise<void> }) {
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    try {
      await onChange(await f());
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }

  if (editing) {
    return (
      <li>
        <PresetForm
          title={"Change preset " + preset.name}
          submitLabel="Save preset"
          initial={preset}
          onCancel={() => setEditing(false)}
          onSubmit={async (name, isDefault, limits) => {
            await updatePreset(preset.name, name, isDefault, limits);
            setEditing(false);
            await onChange(`Preset ${name} saved. Sites that use it get the new values.`);
          }}
        />
      </li>
    );
  }

  return (
    <li>
      <div>
        <strong>{preset.name}</strong>
        {preset.default && <span className="badge ok">Default</span>}
      </div>
      <LimitFacts limits={preset.limits} label={"Values of preset " + preset.name} />
      {confirming ? (
        <div className="actions">
          <span>Delete this preset? Its sites keep the values as custom values.</span>
          <button
            type="button"
            className="danger"
            disabled={busy}
            onClick={() =>
              void act(async () => {
                await deletePreset(preset.name);
                return `Preset ${preset.name} deleted.`;
              })
            }
          >
            Delete preset
          </button>
          <button type="button" className="secondary" disabled={busy} onClick={() => setConfirming(false)}>
            Cancel
          </button>
        </div>
      ) : (
        <div className="actions">
          {!preset.default && (
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  const l = limitsOf(fieldsOf(preset.limits), true);
                  if (typeof l === "string") throw new Error(l);
                  await updatePreset(preset.name, preset.name, true, preset.limits);
                  return `New sites now get the preset ${preset.name}.`;
                })
              }
            >
              Make default
            </button>
          )}
          <button type="button" className="secondary" disabled={busy} onClick={() => setEditing(true)}>
            Change
          </button>
          {!preset.default && (
            <button type="button" className="secondary danger" disabled={busy} onClick={() => setConfirming(true)}>
              Delete
            </button>
          )}
        </div>
      )}
      {error && <p role="alert">{error}</p>}
    </li>
  );
}

function PresetForm({
  title,
  submitLabel,
  initial,
  onSubmit,
  onCancel,
}: {
  title: string;
  submitLabel: string;
  initial?: Preset;
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
      <h3>{title}</h3>
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
      <LimitInputs f={fields} set={setFields} allowUnlimited={!isDefault && !initial?.default} />
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
