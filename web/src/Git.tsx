// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
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
    const t = setInterval(() => void load(), 3000);
    return () => clearInterval(t);
  }, [active, load]);

  if (!git) return error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading the Git setting…</p>;

  if (!git.configured || editing) {
    if (!canEdit) {
      return (
        <Card title="Git deploy">
          <p className="ui-flush">This site does not deploy from Git. An administrator or a manager of the site can set up a repository.</p>
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
      title="Deploys"
      actions={
        <Button variant="primary" disabled={busy || active || needsHostKey || site.disabled} onClick={() => void act(() => startDeploy(site.name))}>
          {active ? "Deploying…" : "Deploy now"}
        </Button>
      }
    >
      <div className="ui-stack">
        <p className="ui-flush">
          A deploy fetches the newest commit of <code>{git.branch}</code> into a new release, runs the build commands as the user {site.name} and only then
          switches <code>{git.deployDir}</code> to it. A failed build never goes live.
        </p>
        {needsHostKey && <Notice tone="warn" title="Confirm the server's host key below before the first deploy." />}
        {site.disabled && <Notice tone="warn" title="The site is disabled: enable it to deploy." />}
        {error && <Notice tone="err" title={error} />}
        {deploys.length === 0 ? (
          <p className="ui-hint ui-flush">No deploys yet.</p>
        ) : (
          <ul className="ui-plain-list ui-stack" aria-label={"Deploys of " + site.name}>
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
  const href = commitURL(repo, sha);
  const code = <code title={sha}>{shortCommit(sha)}</code>;
  if (!href) return code;
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" aria-label={"Commit " + shortCommit(sha) + " at the Git host"}>
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
    const t = d.state === "running" || d.state === "queued" ? setInterval(() => void read(), 3000) : undefined;
    return () => {
      stop = true;
      if (t) clearInterval(t);
    };
  }, [p.open, p.site.name, d.id, d.kind, d.state]);

  return (
    <li className="ui-subsection ui-stack-tight">
      <div className="ui-row">
        <strong>#{d.id}</strong>
        <Badge tone={p.live ? "ok" : w.tone === "ok" ? "off" : w.tone}>{p.live ? "Live" : w.tone === "ok" ? (d.kind === "rollback" ? "Rollback" : "Kept") : w.word}</Badge>
        {d.commit && <CommitLink repo={p.repo} sha={d.commit} />}
        {d.message && <span>{d.message}</span>}
      </div>
      <p className="ui-hint ui-flush">
        {d.kind === "rollback" ? "Rollback to release " + d.release : "Release " + d.release}
        {d.author && <> · {d.author}</>} · {sourceLabel(d.source)} · <RelTime iso={d.createdAt} />
        {d.startedAt && d.finishedAt && <> · {duration(d.startedAt, d.finishedAt)}</>}
      </p>
      {d.error && <Notice tone={d.state === "failed" ? "err" : "warn"} title={d.error} />}
      <div className="ui-row">
        {d.kind === "deploy" && (
          <Button disabled={p.busy} onClick={p.onToggle} aria-expanded={p.open}>
            {p.open ? "Hide log" : "Show log"}
          </Button>
        )}
        {p.canRollBack && (
          <Button disabled={p.busy} onClick={p.onRollback}>
            Roll back to this release
          </Button>
        )}
      </div>
      {p.open &&
        (logError ? (
          <Notice tone="err" title={logError} />
        ) : !log ? (
          <p aria-busy="true">Loading the log…</p>
        ) : (
          <>
            {log.truncated && <p className="ui-hint ui-flush">The log is long; this is its end.</p>}
            <pre className="ui-code" aria-label={"Log of deploy " + String(d.id)}>
              {log.log || "The log is empty."}
            </pre>
          </>
        ))}
    </li>
  );
}

function duration(from: string, to: string): string {
  const s = Math.max(0, Math.round((Date.parse(to) - Date.parse(from)) / 1000));
  if (s < 60) return `${s} s`;
  return s % 60 === 0 ? `${s / 60} min` : `${Math.floor(s / 60)} min ${s % 60} s`;
}

function RepositoryCard({ site, git, canEdit, onEdit, onChange }: { site: Site; git: GitSetting; canEdit: boolean; onEdit: () => void; onChange: () => Promise<void> }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const auth = { none: "Public (https)", token: "Access token (https)", ssh: "Deploy key (SSH)" }[git.auth];

  return (
    <Card title="Repository" actions={canEdit ? <Button onClick={onEdit}>Change</Button> : undefined}>
      <div className="ui-stack">
        <dl className="ui-facts" aria-label="Repository">
          <dt>Address</dt>
          <dd className="ui-mono">{git.url}</dd>
          <dt>Branch</dt>
          <dd className="ui-mono">{git.branch}</dd>
          <dt>Access</dt>
          <dd>{auth}</dd>
          <dt>Deploy directory</dt>
          <dd className="ui-mono">{git.deployDir}</dd>
          <dt>Shared paths</dt>
          <dd className="ui-mono">{git.shared.length ? git.shared.join(", ") : "None"}</dd>
          <dt>Releases kept</dt>
          <dd>{git.keep}</dd>
          <dt>Build time limit</dt>
          <dd>{git.buildTimeoutMinutes} min</dd>
        </dl>
        <div className="ui-stack-tight">
          <strong>Build commands</strong>
          {git.commands.length ? <pre className="ui-code">{git.commands.join("\n")}</pre> : <p className="ui-hint ui-flush">None: the files are deployed as they are in the repository.</p>}
        </div>
        {canEdit &&
          (confirming ? (
            <div className="ui-row" role="alertdialog" aria-label="Remove the Git setting">
              <span>Remove the repository, its key and webhook? The live files stay.</span>
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
                Remove Git setting
              </Button>
              <Button disabled={busy} onClick={() => setConfirming(false)}>
                Cancel
              </Button>
            </div>
          ) : (
            <div className="ui-row">
              <Button variant="danger" onClick={() => setConfirming(true)}>
                Remove Git setting
              </Button>
            </div>
          ))}
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}

function RepositoryForm({ site, initial, onCancel, onSaved }: { site: Site; initial: GitSetting; onCancel?: (() => void) | undefined; onSaved: () => Promise<void> }) {
  const start = inputOf(initial);
  const [url, setUrl] = useState(start.url);
  const [auth, setAuth] = useState<GitAuth>(start.auth);
  const [tokenUser, setTokenUser] = useState(start.tokenUser);
  const [token, setToken] = useState("");
  const [branch, setBranch] = useState(start.branch);
  const [deployDir, setDeployDir] = useState(start.deployDir);
  const [shared, setShared] = useState(start.shared.join("\n"));
  const [commands, setCommands] = useState(start.commands.join("\n"));
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
    <Card title={initial.configured ? "Change the repository" : "Deploy from Git"}>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate aria-label="Git repository">
        {!initial.configured && (
          <p className="ui-flush">
            Hostashi fetches a branch of a repository into a new release below <code>.deploy/releases</code>, runs your build commands as the user{" "}
            {site.name} and then switches the deploy directory to it in one step. The files now in the deploy directory are kept as the first release.
          </p>
        )}
        <Field label="Repository address" hint="https://github.com/acme/site.git, or git@github.com:acme/site.git for a private repository with a deploy key.">
          <input className="ui-input ui-mono" value={url} onChange={(e) => setUrl(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>Access</legend>
          <label className="ui-check">
            <input type="radio" name="git-auth" checked={auth === "none"} disabled={isSSHURL(url)} onChange={() => setAuth("none")} />
            Public repository (https)
          </label>
          <label className="ui-check">
            <input type="radio" name="git-auth" checked={auth === "token"} disabled={isSSHURL(url)} onChange={() => setAuth("token")} />
            Private, with an access token (https)
          </label>
          <label className="ui-check">
            <input type="radio" name="git-auth" checked={auth === "ssh"} disabled={!isSSHURL(url) && url.trim() !== ""} onChange={() => setAuth("ssh")} />
            Private, with a deploy key (SSH): Hostashi makes the key, you add it to the repository as read only
          </label>
        </fieldset>
        {auth === "token" && (
          <div className="ui-narrow ui-stack">
            <Field
              label="Access token"
              hint={initial.hasToken && initial.auth === "token" ? "Leave empty to keep the stored token." : "A token that can only read this repository. It is stored encrypted and never shown again."}
            >
              <input className="ui-input ui-mono" type="password" value={token} onChange={(e) => setToken(e.target.value)} autoComplete="new-password" spellCheck={false} />
            </Field>
            <Field label="User name for the token" hint="Only if the Git host needs one, such as oauth2 for GitLab.">
              <input className="ui-input ui-mono" value={tokenUser} onChange={(e) => setTokenUser(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
        )}
        <div className="ui-narrow ui-stack">
          <Field label="Branch">
            <input className="ui-input ui-mono" value={branch} onChange={(e) => setBranch(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
          <Field label="Deploy directory" hint="The folder below the home that serves the live release, usually htdocs.">
            <input className="ui-input ui-mono" value={deployDir} onChange={(e) => setDeployDir(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
        </div>
        <Field label="Build commands" hint="One command per line, run in the new release as the site user, in this order. A command that fails stops the deploy.">
          <textarea className="ui-input ui-textarea ui-mono" rows={4} value={commands} onChange={(e) => setCommands(e.target.value)} spellCheck={false} />
        </Field>
        <div className="ui-row" role="group" aria-label="Presets">
          <span className="ui-hint">Presets:</span>
          {presets.map((p) => (
            <Button
              key={p.label}
              onClick={() => {
                setCommands(p.commands.join("\n"));
                if (lines(shared).length === 0) setShared(p.shared.join("\n"));
              }}
            >
              {p.label}
            </Button>
          ))}
        </div>
        <Field
          label="Shared paths"
          hint="One per line, relative to the release, such as .env or wp-content/uploads. They stay across deploys; files the site writes elsewhere are replaced by the next deploy."
        >
          <textarea className="ui-input ui-textarea ui-mono" rows={3} value={shared} onChange={(e) => setShared(e.target.value)} spellCheck={false} />
        </Field>
        <div className="ui-narrow ui-stack">
          <Field label="Releases to keep" hint={`For rollbacks; ${minKeep} to ${maxKeep}.`}>
            <input className="ui-input" type="number" min={minKeep} max={maxKeep} value={keep} onChange={(e) => setKeep(e.target.value)} />
          </Field>
          <Field label="Build time limit in minutes" hint={`1 to ${maxBuildMinutes}.`}>
            <input className="ui-input" type="number" min={1} max={maxBuildMinutes} value={timeout} onChange={(e) => setTimeoutMin(e.target.value)} />
          </Field>
        </div>
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {busy ? "Saving…" : "Save repository"}
          </Button>
          {onCancel && (
            <Button disabled={busy} onClick={onCancel}>
              Cancel
            </Button>
          )}
        </div>
      </form>
    </Card>
  );
}

function KeysCard({ site, git, onChange }: { site: Site; git: GitSetting; onChange: () => Promise<void> }) {
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
      setCopied("Copied.");
    } catch {
      setCopied("Copying is not possible here. Select the key and copy it by hand.");
    }
  }

  return (
    <Card title="Deploy key and host key">
      <div className="ui-stack">
        <Field label="Deploy key" hint="Add it to the repository as a read-only deploy key (GitHub, Gitea and Forgejo: Settings, Deploy keys; GitLab: Settings, Repository, Deploy keys).">
          <input readOnly value={git.publicKey} onFocus={(e) => e.currentTarget.select()} className="ui-input ui-mono" spellCheck={false} />
        </Field>
        <div className="ui-row">
          <Button onClick={() => void copy()}>Copy deploy key</Button>
          {!replacing && (
            <Button disabled={busy} onClick={() => setReplacing(true)}>
              Replace deploy key
            </Button>
          )}
          {copied && <span className="ui-hint">{copied}</span>}
        </div>
        {replacing && (
          <div className="ui-row" role="alertdialog" aria-label="Replace the deploy key">
            <span>The current key stops working at once. Add the new key to the repository before the next deploy.</span>
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
              Replace key
            </Button>
            <Button disabled={busy} onClick={() => setReplacing(false)}>
              Cancel
            </Button>
          </div>
        )}
        <div className="ui-stack-tight">
          <strong>Host key of {host || "the server"}</strong>
          {git.hostKey ? (
            <p className="ui-flush">
              Confirmed: <code>{git.hostKeyFingerprint}</code>. Deploys connect only to a server with this key.
            </p>
          ) : (
            <p className="ui-flush">Not confirmed yet. Read the key the server presents and compare its fingerprint with the one the Git host publishes.</p>
          )}
        </div>
        {scanned && (
          <section className="ui-card ui-card-body ui-stack ui-attention" aria-label="Scanned host key">
            <p className="ui-flush">The server presents this key:</p>
            <p className="ui-flush">
              <code className="ui-confirm-phrase">{scanned.fingerprint}</code>
            </p>
            <p className="ui-flush">
              {published ? (
                <>
                  Compare it with the fingerprints{" "}
                  <a href={published.href} target="_blank" rel="noreferrer">
                    {published.label} publishes
                  </a>
                  . Trust it only if it matches exactly.
                </>
              ) : (
                <>Compare it with the fingerprint your Git server's administrator gives you. Trust it only if it matches exactly.</>
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
                It matches: trust this key
              </Button>
              <Button disabled={busy} onClick={() => setScanned(null)}>
                Cancel
              </Button>
            </div>
          </section>
        )}
        {!scanned && (
          <div className="ui-row">
            <Button disabled={busy} onClick={() => void act(async () => setScanned(await scanHostKey(site.name)))}>
              {busy ? "Reading…" : git.hostKey ? "Read the host key again" : "Read the host key"}
            </Button>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}

function WebhookCard({ site, git, onChange }: { site: Site; git: GitSetting; onChange: () => Promise<void> }) {
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

  async function copy(what: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(what + " copied.");
    } catch {
      setCopied("Copying is not possible here. Select the text and copy it by hand.");
    }
  }

  return (
    <Card title="Webhook">
      <div className="ui-stack">
        <p className="ui-flush">
          With the webhook on, a push to <code>{git.branch}</code> deploys the site. GitHub, Gitea, Forgejo and GitLab sign their calls with the secret; a
          script can send it as <code>Authorization: Bearer</code>.
        </p>
        {shown ? (
          <section className="ui-card ui-card-body ui-stack ui-attention" aria-label="Webhook secret">
            <strong>Enter these in the Git host now</strong>
            <p className="ui-flush">The secret is shown only once. Content type: application/json; event: push.</p>
            <Field label="Payload URL">
              <input readOnly value={address(shown.path)} onFocus={(e) => e.currentTarget.select()} className="ui-input ui-mono" spellCheck={false} />
            </Field>
            <Field label="Secret">
              <input readOnly value={shown.secret} onFocus={(e) => e.currentTarget.select()} className="ui-input ui-mono" spellCheck={false} />
            </Field>
            <div className="ui-row">
              <Button onClick={() => void copy("Address", address(shown.path))}>Copy address</Button>
              <Button onClick={() => void copy("Secret", shown.secret)}>Copy secret</Button>
              <Button variant="primary" onClick={() => setShown(null)}>
                I have saved it
              </Button>
              {copied && <span className="ui-hint">{copied}</span>}
            </div>
          </section>
        ) : git.webhook ? (
          <>
            <Field label="Payload URL">
              <input readOnly value={address(git.webhookPath)} onFocus={(e) => e.currentTarget.select()} className="ui-input ui-mono" spellCheck={false} />
            </Field>
            <div className="ui-row">
              <Button disabled={busy} onClick={() => void act(async () => setShown(await setWebhook(site.name)))}>
                New secret
              </Button>
              <Button variant="danger" disabled={busy} onClick={() => void act(() => deleteWebhook(site.name))}>
                Turn webhook off
              </Button>
            </div>
          </>
        ) : (
          <div className="ui-row">
            <Button disabled={busy} onClick={() => void act(async () => setShown(await setWebhook(site.name)))}>
              Turn webhook on
            </Button>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}
