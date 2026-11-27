// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import i18n from "./i18n";
import {
  activePreset,
  applyPreset,
  commitURL,
  deleteGit,
  deleteWebhook,
  deployLog,
  deployWord,
  getGit,
  gitMessage,
  hostOf,
  inputOf,
  inputProblem,
  isSSHURL,
  lines,
  listDeploys,
  liveRelease,
  maxBuildMinutes,
  maxKeep,
  minKeep,
  newGitKey,
  presets,
  publishedFingerprints,
  rollback,
  scanHostKey,
  setGit,
  setHostKey,
  setWebhook,
  shortCommit,
  sourceLabel,
  startDeploy,
  type Deploy,
  type DeployLog,
  type GitAuth,
  type GitSetting,
  type HostKey,
  type Webhook,
} from "./git";
import type { Site } from "./sites";
import { Badge, Button, Card, Field, Notice, RelTime } from "./ui";

type Props = {
  site: Site;
  /** Admins and managers of the site change the setting; everyone may deploy. */
  canEdit: boolean;
};

// GitSettings is the Git tab of a site (ADR-0062): the repository, its
// keys, the webhook, and the deploys with their logs and rollbacks.
export function GitSettings({ site, canEdit }: Props) {
  const { t } = useTranslation();
  const [git, setGitState] = useState<GitSetting | null>(null);
  const [deploys, setDeploys] = useState<Deploy[]>([]);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);

  const load = useCallback(async () => {
    try {
      const g = await getGit(site.name);
      setGitState(g);
      setDeploys(g.configured ? await listDeploys(site.name) : []);
      setError("");
    } catch (err) {
      setError(gitMessage(err));
    }
  }, [site.name]);

  useEffect(() => {
    void load();
  }, [load]);

  // A running deploy changes its state on the server: look again.
  const active = deploys.some((d) => d.state === "queued" || d.state === "running");
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => void load(), 3000);
    return () => clearInterval(timer);
  }, [active, load]);

  if (!git) return error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{t("git.loading")}</p>;

  if (!git.configured || editing) {
    if (!canEdit) {
      return (
        <Card title={t("git.title")}>
          <p className="ui-flush">{t("git.notSetUp")}</p>
        </Card>
      );
    }
    return (
      <RepositoryForm
        site={site}
        initial={git}
        onCancel={git.configured ? () => setEditing(false) : undefined}
        onSaved={async () => {
          setEditing(false);
          await load();
        }}
      />
    );
  }

  return (
    <>
      {error && <Notice tone="err" title={error} />}
      <DeploysCard site={site} git={git} deploys={deploys} onChange={load} />
      <RepositoryCard site={site} git={git} canEdit={canEdit} onEdit={() => setEditing(true)} onChange={load} />
      {canEdit && git.auth === "ssh" && <KeysCard site={site} git={git} onChange={load} />}
      {canEdit && <WebhookCard site={site} git={git} onChange={load} />}
    </>
  );
}

function DeploysCard({ site, git, deploys, onChange }: { site: Site; git: GitSetting; deploys: Deploy[]; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [open, setOpen] = useState<number | null>(null);

  const active = deploys.some((d) => d.state === "queued" || d.state === "running");
  const live = liveRelease(deploys);
  const needsHostKey = git.auth === "ssh" && git.hostKey === "";

  async function act(f: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await f();
      await onChange();
    } catch (err) {
      setError(gitMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card
      title={t("git.deploys.title")}
      actions={
        <Button variant="primary" disabled={busy || active || needsHostKey || site.disabled} onClick={() => void act(() => startDeploy(site.name))}>
          {active ? t("git.deploys.deploying") : t("git.deploys.deployNow")}
        </Button>
      }
    >
      <div className="ui-stack">
        <p className="ui-flush">
          <Trans i18nKey="git.deploys.intro" values={{ branch: git.branch, user: site.name, dir: git.deployDir }} components={{ code1: <code />, code2: <code /> }} />
        </p>
        {needsHostKey && <Notice tone="warn" title={t("git.deploys.needsHostKey")} />}
        {site.disabled && <Notice tone="warn" title={t("git.deploys.disabled")} />}
        {error && <Notice tone="err" title={error} />}
        {deploys.length === 0 ? (
          <p className="ui-hint ui-flush">{t("git.deploys.none")}</p>
        ) : (
          <ul className="ui-plain-list ui-stack" aria-label={t("git.deploys.list", { site: site.name })}>
            {deploys.map((d) => (
              <DeployRow
                key={d.id}
                site={site}
                repo={git.url}
                deploy={d}
                live={d.state === "succeeded" && d.release === live && deploys.find((x) => x.state === "succeeded") === d}
                canRollBack={!active && d.kind === "deploy" && d.state === "succeeded" && d.release !== live}
                busy={busy}
                open={open === d.id}
                onToggle={() => setOpen(open === d.id ? null : d.id)}
                onRollback={() => void act(() => rollback(site.name, d.id))}
              />
            ))}
          </ul>
        )}
      </div>
    </Card>
  );
}

// CommitLink opens the commit at the Git host, when its address gives a
// web page; otherwise it is the short id alone.
function CommitLink({ repo, sha }: { repo: string; sha: string }) {
  const { t } = useTranslation();
  const href = commitURL(repo, sha);
  const code = <code title={sha}>{shortCommit(sha)}</code>;
  if (!href) return code;
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" aria-label={t("git.deploys.commit", { commit: shortCommit(sha) })}>
      {code}
    </a>
  );
}

function DeployRow(p: {
  site: Site;
  repo: string;
  deploy: Deploy;
  live: boolean;
  canRollBack: boolean;
  busy: boolean;
  open: boolean;
  onToggle: () => void;
  onRollback: () => void;
}) {
  const { t } = useTranslation();
  const d = p.deploy;
  const w = deployWord(d);
  const [log, setLog] = useState<DeployLog | null>(null);
  const [logError, setLogError] = useState("");

  useEffect(() => {
    if (!p.open || d.kind !== "deploy") return;
    let stop = false;
    const read = async () => {
      try {
        const l = await deployLog(p.site.name, d.id);
        if (!stop) setLog(l);
      } catch (err) {
        if (!stop) setLogError(gitMessage(err));
      }
    };
    void read();
    // The log grows while the run is active.
    const timer = d.state === "running" || d.state === "queued" ? setInterval(() => void read(), 3000) : undefined;
    return () => {
      stop = true;
      if (timer) clearInterval(timer);
    };
  }, [p.open, p.site.name, d.id, d.kind, d.state]);

  return (
    <li className="ui-subsection ui-stack-tight">
      <div className="ui-row">
        <strong>#{d.id}</strong>
        <Badge tone={p.live ? "ok" : w.tone === "ok" ? "off" : w.tone}>{p.live ? t("git.words.live") : w.tone === "ok" ? (d.kind === "rollback" ? t("git.words.rollback") : t("git.words.kept")) : w.word}</Badge>
        {d.commit && <CommitLink repo={p.repo} sha={d.commit} />}
        {d.message && <span>{d.message}</span>}
      </div>
      <p className="ui-hint ui-flush">
        {d.kind === "rollback" ? t("git.deploys.rollbackTo", { release: d.release }) : t("git.deploys.release", { release: d.release })}
        {d.author && <> · {d.author}</>} · {sourceLabel(d.source)} · <RelTime iso={d.createdAt} />
        {d.startedAt && d.finishedAt && <> · {duration(d.startedAt, d.finishedAt)}</>}
      </p>
      {d.error && <Notice tone={d.state === "failed" ? "err" : "warn"} title={d.error} />}
      <div className="ui-row">
        {d.kind === "deploy" && (
          <Button disabled={p.busy} onClick={p.onToggle} aria-expanded={p.open}>
            {p.open ? t("git.deploys.hideLog") : t("git.deploys.showLog")}
          </Button>
        )}
        {p.canRollBack && (
          <Button disabled={p.busy} onClick={p.onRollback}>
            {t("git.deploys.rollBack")}
          </Button>
        )}
      </div>
      {p.open &&
        (logError ? (
          <Notice tone="err" title={logError} />
        ) : !log ? (
          <p aria-busy="true">{t("git.deploys.loadingLog")}</p>
        ) : (
          <>
            {log.truncated && <p className="ui-hint ui-flush">{t("git.deploys.truncated")}</p>}
            <pre className="ui-code" aria-label={t("git.deploys.logLabel", { id: d.id })}>
              {log.log || t("git.deploys.emptyLog")}
            </pre>
          </>
        ))}
    </li>
  );
}

function duration(from: string, to: string): string {
  const s = Math.max(0, Math.round((Date.parse(to) - Date.parse(from)) / 1000));
  if (s < 60) return i18n.t("git.deploys.seconds", { s });
  return s % 60 === 0 ? i18n.t("git.deploys.minutes", { m: s / 60 }) : i18n.t("git.deploys.minutesSeconds", { m: Math.floor(s / 60), s: s % 60 });
}

function RepositoryCard({ site, git, canEdit, onEdit, onChange }: { site: Site; git: GitSetting; canEdit: boolean; onEdit: () => void; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const auth = t(`git.repo.auth.${git.auth}`);

  return (
    <Card title={t("git.repo.title")} actions={canEdit ? <Button onClick={onEdit}>{t("git.repo.change")}</Button> : undefined}>
      <div className="ui-stack">
        <dl className="ui-facts" aria-label={t("git.repo.title")}>
          <dt>{t("git.repo.address")}</dt>
          <dd className="ui-mono">{git.url}</dd>
          <dt>{t("git.repo.branch")}</dt>
          <dd className="ui-mono">{git.branch}</dd>
          <dt>{t("git.repo.access")}</dt>
          <dd>{auth}</dd>
          <dt>{t("git.repo.deployDir")}</dt>
          <dd className="ui-mono">{git.deployDir}</dd>
          <dt>{t("git.repo.shared")}</dt>
          <dd className="ui-mono">{git.shared.length ? git.shared.join(", ") : t("git.repo.none")}</dd>
          <dt>{t("git.repo.keep")}</dt>
          <dd>{git.keep}</dd>
          <dt>{t("git.repo.timeout")}</dt>
          <dd>{t("git.repo.timeoutValue", { count: git.buildTimeoutMinutes })}</dd>
        </dl>
        <div className="ui-stack-tight">
          <strong>{t("git.repo.commands")}</strong>
          {git.commands.length ? <pre className="ui-code">{git.commands.join("\n")}</pre> : <p className="ui-hint ui-flush">{t("git.repo.noCommands")}</p>}
        </div>
        {canEdit &&
          (confirming ? (
            <div className="ui-row" role="alertdialog" aria-label={t("git.repo.removeLabel")}>
              <span>{t("git.repo.removeText")}</span>
              <Button
                variant="danger"
                disabled={busy}
                onClick={() => {
                  setBusy(true);
                  deleteGit(site.name)
                    .then(onChange)
                    .catch((err: unknown) => {
                      setError(gitMessage(err));
                      setBusy(false);
                    });
                }}
              >
                {t("git.repo.remove")}
              </Button>
              <Button disabled={busy} onClick={() => setConfirming(false)}>
                {t("git.repo.cancel")}
              </Button>
            </div>
          ) : (
            <div className="ui-row">
              <Button variant="danger" onClick={() => setConfirming(true)}>
                {t("git.repo.remove")}
              </Button>
            </div>
          ))}
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}

/** presetName is a preset's label in the panel's language. */
const presetName = (label: string) => (label === "Static site (npm build)" ? i18n.t("git.form.presetStatic") : label);

function RepositoryForm(
{ site, initial, onCancel, onSaved }: { site: Site; initial: GitSetting; onCancel?: (() => void) | undefined; onSaved: () => Promise<void> }) {
  const { t } = useTranslation();
  const start = inputOf(initial);
  const [url, setUrl] = useState(start.url);
  const [auth, setAuth] = useState<GitAuth>(start.auth);
  const [tokenUser, setTokenUser] = useState(start.tokenUser);
  const [token, setToken] = useState("");
  const [branch, setBranch] = useState(start.branch);
  const [deployDir, setDeployDir] = useState(start.deployDir);
  const [shared, setShared] = useState(start.shared.join("\n"));
  const [commands, setCommands] = useState(start.commands.join("\n"));
  // undo holds what a preset replaced when that was the user's own text.
  const [undo, setUndo] = useState<{ preset: string; commands: string; shared: string; edited: string[] } | null>(null);
  const [keep, setKeep] = useState(String(start.keep));
  const [timeout, setTimeoutMin] = useState(String(start.buildTimeoutMinutes));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  // An SSH address needs the deploy key; an https one cannot use it.
  useEffect(() => {
    if (isSSHURL(url) && auth !== "ssh") setAuth("ssh");
    if (!isSSHURL(url) && auth === "ssh" && url.trim().startsWith("https://")) setAuth("none");
  }, [url, auth]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const input = {
      url: url.trim(),
      branch: branch.trim(),
      deployDir: deployDir.trim(),
      shared: lines(shared),
      commands: lines(commands),
      keep: Number(keep),
      buildTimeoutMinutes: Number(timeout),
      submodules: false,
      auth,
      tokenUser: auth === "token" ? tokenUser.trim() : "",
      token: auth === "token" ? token : "",
    };
    const problem = inputProblem(input, initial.hasToken && initial.auth === "token");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      await setGit(site.name, input);
      await onSaved();
    } catch (err) {
      setError(gitMessage(err));
      setBusy(false);
    }
  }

  return (
    <Card title={initial.configured ? t("git.form.titleChange") : t("git.form.titleNew")}>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate aria-label={t("git.form.label")}>
        {!initial.configured && (
          <p className="ui-flush">
            <Trans i18nKey="git.form.intro" values={{ user: site.name }} components={{ code: <code /> }} />
          </p>
        )}
        <Field label={t("git.form.url")} hint={t("git.form.urlHint")}>
          <input className="ui-input ui-mono" value={url} onChange={(e) => setUrl(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>{t("git.form.access")}</legend>
          <label className="ui-check">
            <input type="radio" name="git-auth" checked={auth === "none"} disabled={isSSHURL(url)} onChange={() => setAuth("none")} />
            {t("git.form.authNone")}
          </label>
          <label className="ui-check">
            <input type="radio" name="git-auth" checked={auth === "token"} disabled={isSSHURL(url)} onChange={() => setAuth("token")} />
            {t("git.form.authToken")}
          </label>
          <label className="ui-check">
            <input type="radio" name="git-auth" checked={auth === "ssh"} disabled={!isSSHURL(url) && url.trim() !== ""} onChange={() => setAuth("ssh")} />
            {t("git.form.authSsh")}
          </label>
        </fieldset>
        {auth === "token" && (
          <div className="ui-narrow ui-stack">
            <Field
              label={t("git.form.token")}
              hint={initial.hasToken && initial.auth === "token" ? t("git.form.tokenKeep") : t("git.form.tokenHint")}
            >
              <input className="ui-input ui-mono" type="password" value={token} onChange={(e) => setToken(e.target.value)} autoComplete="new-password" spellCheck={false} />
            </Field>
            <Field label={t("git.form.tokenUser")} hint={t("git.form.tokenUserHint")}>
              <input className="ui-input ui-mono" value={tokenUser} onChange={(e) => setTokenUser(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
        )}
        <div className="ui-narrow ui-stack">
          <Field label={t("git.form.branch")}>
            <input className="ui-input ui-mono" value={branch} onChange={(e) => setBranch(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
          <Field label={t("git.form.deployDir")} hint={t("git.form.deployDirHint")}>
            <input className="ui-input ui-mono" value={deployDir} onChange={(e) => setDeployDir(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
        </div>
        <Field label={t("git.form.commands")} hint={t("git.form.commandsHint")}>
          <textarea className="ui-input ui-textarea ui-mono" rows={4} value={commands} onChange={(e) => {
              setCommands(e.target.value);
              setUndo(null);
            }} spellCheck={false} />
        </Field>
        <div className="ui-row" role="group" aria-label={t("git.form.presets")}>
          <span className="ui-hint">{t("git.form.presetsLabel")}</span>
          {presets.map((p) => (
            <Button
              key={p.label}
              aria-pressed={activePreset(commands, shared) === p.label}
              onClick={() => {
                const next = applyPreset(p, commands, shared);
                setUndo(next.edited.length > 0 ? { preset: p.label, commands, shared, edited: next.edited } : null);
                setCommands(next.commands);
                setShared(next.shared);
              }}
            >
              {presetName(p.label)}
            </Button>
          ))}
        </div>
        {undo && (
          <Notice
            tone="info"
            title={
              undo.edited.length > 1
                ? t("git.form.replacedBoth", { preset: presetName(undo.preset) })
                : t("git.form.replaced", { preset: presetName(undo.preset), what: undo.edited[0] === "build commands" ? t("git.form.editedCommands") : t("git.form.editedShared") })
            }
          >
            <Button
              small
              onClick={() => {
                setCommands(undo.commands);
                setShared(undo.shared);
                setUndo(null);
              }}
            >
              {t("git.form.undo")}
            </Button>
          </Notice>
        )}
        <Field
          label={t("git.form.shared")}
          hint={t("git.form.sharedHint")}
        >
          <textarea className="ui-input ui-textarea ui-mono" rows={3} value={shared} onChange={(e) => {
              setShared(e.target.value);
              setUndo(null);
            }} spellCheck={false} />
        </Field>
        <div className="ui-narrow ui-stack">
          <Field label={t("git.form.keep")} hint={t("git.form.keepHint", { min: minKeep, max: maxKeep })}>
            <input className="ui-input" type="number" min={minKeep} max={maxKeep} value={keep} onChange={(e) => setKeep(e.target.value)} />
          </Field>
          <Field label={t("git.form.timeout")} hint={t("git.form.timeoutHint", { max: maxBuildMinutes })}>
            <input className="ui-input" type="number" min={1} max={maxBuildMinutes} value={timeout} onChange={(e) => setTimeoutMin(e.target.value)} />
          </Field>
        </div>
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {busy ? t("git.form.saving") : t("git.form.save")}
          </Button>
          {onCancel && (
            <Button disabled={busy} onClick={onCancel}>
              {t("git.form.cancel")}
            </Button>
          )}
        </div>
      </form>
    </Card>
  );
}

function KeysCard({ site, git, onChange }: { site: Site; git: GitSetting; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
  const [scanned, setScanned] = useState<HostKey | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");
  const [replacing, setReplacing] = useState(false);
  const host = hostOf(git.url);
  const published = publishedFingerprints.find((p) => p.host === host);

  async function act(f: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await f();
    } catch (err) {
      setError(gitMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(git.publicKey);
      setCopied(t("git.keys.copied"));
    } catch {
      setCopied(t("git.keys.copyFailed"));
    }
  }

  return (
    <Card title={t("git.keys.title")}>
      <div className="ui-stack">
        <Field label={t("git.keys.deployKey")} hint={t("git.keys.deployKeyHint")}>
          <input readOnly value={git.publicKey} onFocus={(e) => e.currentTarget.select()} className="ui-input ui-mono" spellCheck={false} />
        </Field>
        <div className="ui-row">
          <Button onClick={() => void copy()}>{t("git.keys.copy")}</Button>
          {!replacing && (
            <Button disabled={busy} onClick={() => setReplacing(true)}>
              {t("git.keys.replace")}
            </Button>
          )}
          {copied && <span className="ui-hint">{copied}</span>}
        </div>
        {replacing && (
          <div className="ui-row" role="alertdialog" aria-label={t("git.keys.replaceLabel")}>
            <span>{t("git.keys.replaceText")}</span>
            <Button
              variant="danger"
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await newGitKey(site.name);
                  setReplacing(false);
                  await onChange();
                })
              }
            >
              {t("git.keys.replaceSubmit")}
            </Button>
            <Button disabled={busy} onClick={() => setReplacing(false)}>
              {t("git.keys.cancel")}
            </Button>
          </div>
        )}
        <div className="ui-stack-tight">
          <strong>{t("git.keys.hostKeyOf", { host: host || t("git.keys.theServer") })}</strong>
          {git.hostKey ? (
            <p className="ui-flush">
              <Trans i18nKey="git.keys.confirmed" values={{ fingerprint: git.hostKeyFingerprint }} components={{ code: <code /> }} />
            </p>
          ) : (
            <p className="ui-flush">{t("git.keys.notConfirmed")}</p>
          )}
        </div>
        {scanned && (
          <section className="ui-card ui-card-body ui-stack ui-attention" aria-label={t("git.keys.scanned")}>
            <p className="ui-flush">{t("git.keys.presents")}</p>
            <p className="ui-flush">
              <code className="ui-confirm-phrase">{scanned.fingerprint}</code>
            </p>
            <p className="ui-flush">
              {published ? (
                <Trans i18nKey="git.keys.comparePublished" values={{ host: published.label }} components={{ a: <a href={published.href} target="_blank" rel="noreferrer" /> }} />
              ) : (
                t("git.keys.compareOwn")
              )}
            </p>
            <div className="ui-row">
              <Button
                variant="primary"
                disabled={busy}
                onClick={() =>
                  void act(async () => {
                    await setHostKey(site.name, scanned.key);
                    setScanned(null);
                    await onChange();
                  })
                }
              >
                {t("git.keys.trust")}
              </Button>
              <Button disabled={busy} onClick={() => setScanned(null)}>
                {t("git.keys.cancel")}
              </Button>
            </div>
          </section>
        )}
        {!scanned && (
          <div className="ui-row">
            <Button disabled={busy} onClick={() => void act(async () => setScanned(await scanHostKey(site.name)))}>
              {busy ? t("git.keys.reading") : git.hostKey ? t("git.keys.readAgain") : t("git.keys.read")}
            </Button>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}

function WebhookCard({ site, git, onChange }: { site: Site; git: GitSetting; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
  const [shown, setShown] = useState<Webhook | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");
  const address = (path: string) => window.location.origin + path;

  async function act(f: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await f();
      await onChange();
    } catch (err) {
      setError(gitMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function copy(done: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(done);
    } catch {
      setCopied(t("git.webhook.copyFailed"));
    }
  }

  return (
    <Card title={t("git.webhook.title")}>
      <div className="ui-stack">
        <p className="ui-flush">
          <Trans i18nKey="git.webhook.intro" values={{ branch: git.branch }} components={{ code1: <code />, code2: <code /> }} />
        </p>
        {shown ? (
          <section className="ui-card ui-card-body ui-stack ui-attention" aria-label={t("git.webhook.secretLabel")}>
            <strong>{t("git.webhook.enterNow")}</strong>
            <p className="ui-flush">{t("git.webhook.once")}</p>
            <Field label={t("git.webhook.url")}>
              <input readOnly value={address(shown.path)} onFocus={(e) => e.currentTarget.select()} className="ui-input ui-mono" spellCheck={false} />
            </Field>
            <Field label={t("git.webhook.secret")}>
              <input readOnly value={shown.secret} onFocus={(e) => e.currentTarget.select()} className="ui-input ui-mono" spellCheck={false} />
            </Field>
            <div className="ui-row">
              <Button onClick={() => void copy(t("git.webhook.addressCopied"), address(shown.path))}>{t("git.webhook.copyAddress")}</Button>
              <Button onClick={() => void copy(t("git.webhook.secretCopied"), shown.secret)}>{t("git.webhook.copySecret")}</Button>
              <Button variant="primary" onClick={() => setShown(null)}>
                {t("git.webhook.saved")}
              </Button>
              {copied && <span className="ui-hint">{copied}</span>}
            </div>
          </section>
        ) : git.webhook ? (
          <>
            <Field label={t("git.webhook.url")}>
              <input readOnly value={address(git.webhookPath)} onFocus={(e) => e.currentTarget.select()} className="ui-input ui-mono" spellCheck={false} />
            </Field>
            <div className="ui-row">
              <Button disabled={busy} onClick={() => void act(async () => setShown(await setWebhook(site.name)))}>
                {t("git.webhook.newSecret")}
              </Button>
              <Button variant="danger" disabled={busy} onClick={() => void act(() => deleteWebhook(site.name))}>
                {t("git.webhook.off")}
              </Button>
            </div>
          </>
        ) : (
          <div className="ui-row">
            <Button disabled={busy} onClick={() => void act(async () => setShown(await setWebhook(site.name)))}>
              {t("git.webhook.on")}
            </Button>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}
