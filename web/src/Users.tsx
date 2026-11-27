// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, GearSix, Plus } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatHash } from "./routes";
import { listSites } from "./sites";
import { Badge, Button, Card, Field, IconLink, Notice, PageHead, RelTime, RowActions, Table, type Column, type Sort } from "./ui";
import {
  createUser,
  deleteUser,
  getGrants,
  getUserPolicy,
  hasSecondFactor,
  inviteLink,
  listUsers,
  resetUser,
  roleHint,
  roleName,
  roles,
  setGrants,
  setUserPolicy,
  updateUser,
  usersMessage,
  type Account,
  type Invitation,
  type Role,
  type UserPolicy,
} from "./users";

type QueryProps = {
  /** The address query: q, role, sort, dir; new=1 or user=<name> for one view. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

const usersHash = (q?: Record<string, string>) => formatHash({ page: "users", ...(q ? { query: new URLSearchParams(q) } : {}) });

// StatusBadges says in a few words whether an account can log in and how
// well it is protected.
function StatusBadges({ a }: { a: Account }) {
  const { t } = useTranslation();
  return (
    <span className="ui-row">
      {a.disabled && <Badge tone="err">{t("users.status.disabled")}</Badge>}
      {a.invited ? (
        a.invitationExpiresAt ? (
          <Badge tone="off">{t("users.status.invited")}</Badge>
        ) : (
          <Badge tone="warn">{t("users.status.invitationExpired")}</Badge>
        )
      ) : hasSecondFactor(a) ? (
        <Badge tone="ok">{t("users.status.secondFactorOn")}</Badge>
      ) : (
        <Badge tone="warn">{t("users.status.noSecondFactor")}</Badge>
      )}
    </span>
  );
}

// Users lists the panel accounts for admins (ADR-0035): new accounts get a
// one-time invitation link, users get the sites they may work on.
export function Users({ me, query = new URLSearchParams(), onQuery = () => undefined }: { me: string } & QueryProps) {
  const { t } = useTranslation();
  const [users, setUsers] = useState<Account[] | null>(null);
  const [policy, setPolicy] = useState<UserPolicy>({ requireSecondFactor: false });
  const [sites, setSites] = useState<string[]>([]);
  const [link, setLink] = useState<{ username: string; invitation: Invitation } | null>(null);
  const [done, setDone] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const [list, all, p] = await Promise.all([listUsers(), listSites(), getUserPolicy()]);
      setUsers(list);
      setPolicy(p);
      setSites(all.map((s) => s.name));
      setError("");
    } catch (err) {
      setError(usersMessage(err));
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

  const listLink = (
    <a className="ui-btn" href={usersHash()}>
      <ArrowLeft size={16} aria-hidden="true" />
      {t("users.allUsers")}
    </a>
  );

  if (!users) {
    return (
      <>
        <PageHead title={t("users.title")} />
        {error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>{t("common.actions.tryAgain")}</Button>
          </Notice>
        ) : (
          <p aria-busy="true">{t("users.loading")}</p>
        )}
      </>
    );
  }

  // A new link replaces the view, so that it is the one thing on the page.
  const showLink = (username: string, invitation: Invitation) => {
    setDone("");
    setLink({ username, invitation });
    set({ new: "", user: "" });
  };

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead title={t("users.invite.title")} description={t("users.invite.description")} actions={listLink} />
        <Card>
          <CreateForm
            onCancel={() => set({ new: "" })}
            onCreated={(u, inv) => {
              showLink(u, inv);
              void load();
            }}
          />
        </Card>
      </>
    );
  }

  const name = query.get("user");
  if (name !== null) {
    const account = users.find((u) => u.username === name);
    if (!account) {
      return (
        <>
          <PageHead title={t("users.title")} actions={listLink} />
          <Notice tone="warn" title={t("users.noSuchAccount", { name })} />
        </>
      );
    }
    return (
      <AccountView
        key={account.id}
        account={account}
        policy={policy}
        self={account.username === me}
        sites={sites}
        back={listLink}
        onChange={load}
        onLink={showLink}
        onDeleted={async (msg) => {
          setLink(null);
          setDone(msg);
          set({ user: "" });
          await load();
        }}
      />
    );
  }

  const columns: Column<Account>[] = [
    {
      key: "username",
      header: t("users.list.user"),
      sortValue: (a) => a.username,
      cell: (a) => (
        <span className="ui-row ui-nowrap">
          {a.username === me ? <strong>{a.username}</strong> : <a href={usersHash({ user: a.username })}>{a.username}</a>}
          {a.username === me && <Badge tone="off">{t("users.list.you")}</Badge>}
        </span>
      ),
    },
    {
      key: "role",
      header: t("users.list.role"),
      sortValue: (a) => roles.indexOf(a.role),
      cell: (a) => roleName(a.role),
    },
    {
      key: "status",
      header: t("users.list.status"),
      sortValue: (a) => (a.disabled ? 0 : a.invited ? 1 : a.totpEnabled || a.passkeys > 0 ? 3 : 2),
      cell: (a) => (
        <div className="ui-cell-main">
          <StatusBadges a={a} />
          {a.invited && a.invitationExpiresAt && (
            <p className="ui-hint ui-flush">
              <Trans i18nKey="users.status.linkExpires" components={{ time: <RelTime iso={a.invitationExpiresAt} /> }} />
            </p>
          )}
        </div>
      ),
    },
    {
      key: "actions",
      header: t("users.list.actions"),
      srHeader: true,
      cell: (a) =>
        a.username === me ? null : (
          <RowActions>
            <IconLink label={t("users.list.manageUser", { name: a.username })} text={t("users.list.manage")} icon={<GearSix size={16} />} href={usersHash({ user: a.username })} />
          </RowActions>
        ),
    },
  ];

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const role = query.get("role") ?? "";
  const shown = users.filter((u) => (!q || u.username.includes(q)) && (!role || u.role === role));
  const sortKey = query.get("sort");
  const sort: Sort | undefined = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : undefined;

  return (
    <>
      <PageHead
        title={t("users.title")}
        description={t("users.description")}
        actions={
          <a className="ui-btn ui-btn-primary" href={usersHash({ new: "1" })}>
            <Plus size={16} aria-hidden="true" />
            {t("users.invite.button")}
          </a>
        }
      />
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
      {link && <LinkCard username={link.username} invitation={link.invitation} onClose={() => setLink(null)} />}
      <Card padded={false}>
        <div className="ui-filters">
          <input
            className="ui-input"
            type="search"
            aria-label={t("users.list.filter")}
            placeholder={t("users.list.filterPlaceholder")}
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          <select className="ui-select" aria-label={t("users.list.role")} value={role} onChange={(e) => set({ role: e.target.value })}>
            <option value="">{t("users.list.allRoles")}</option>
            {roles.map((r) => (
              <option key={r} value={r}>
                {roleName(r)}
              </option>
            ))}
          </select>
        </div>
        <Table
          label={t("users.title")}
          columns={columns}
          rows={shown}
          rowKey={(a) => String(a.id)}
          {...(sort ? { sort } : {})}
          onSort={(s) => set({ sort: s.key, dir: s.dir })}
          empty={t("users.list.empty")}
        />
      </Card>
      <PolicyCard users={users} policy={policy} onChange={setPolicy} />
    </>
  );
}

// PolicyCard requires a second factor of every account (ADR-0035,
// addendum 1). Admins and managers need one anyway; this adds users.
// Turning it on names the accounts that have none yet, because they can
// do nothing but set one up from their next request on.
function PolicyCard({ users, policy, onChange }: { users: Account[]; policy: UserPolicy; onChange: (p: UserPolicy) => void }) {
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const { t } = useTranslation();

  const without = users.filter((u) => !u.disabled && !u.invited && !hasSecondFactor(u)).map((u) => u.username);

  async function save(on: boolean) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      onChange(await setUserPolicy(on));
      setConfirm(false);
      setDone(on ? t("users.policy.on") : t("users.policy.off"));
    } catch (err) {
      setError(usersMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("users.policy.title")}>
      <div className="ui-stack">
        <label className="ui-check">
          <input
            type="checkbox"
            checked={policy.requireSecondFactor || confirm}
            disabled={busy || confirm}
            onChange={(e) => {
              if (!e.target.checked) void save(false);
              else if (without.length > 0) setConfirm(true);
              else void save(true);
            }}
          />
          {t("users.policy.require")}
        </label>
        <p className="ui-hint ui-flush">{t("users.policy.hint")}</p>
        {confirm && (
          <div className="ui-stack" role="alertdialog" aria-labelledby="policy-confirm-title">
            <h3 id="policy-confirm-title" className="ui-flush">
              {t("users.policy.without", { count: without.length })}
            </h3>
            <p className="ui-flush">
              {without.map((n, i) => (
                <span key={n}>
                  {i > 0 && ", "}
                  <strong>{n}</strong>
                </span>
              ))}
              . {t("users.policy.withoutText")}
            </p>
            <div className="ui-row">
              <Button variant="primary" disabled={busy} onClick={() => void save(true)}>
                {t("users.policy.confirm")}
              </Button>
              <Button disabled={busy} onClick={() => setConfirm(false)}>
                {t("common.actions.cancel")}
              </Button>
            </div>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </div>
    </Card>
  );
}

function RoleSelect({ value, onChange }: { value: Role; onChange: (r: Role) => void }) {
  const { t } = useTranslation();
  return (
    <div className="ui-narrow">
      <Field label={t("users.list.role")} hint={roleHint(value)}>
        <select className="ui-select" value={value} onChange={(e) => onChange(e.target.value as Role)}>
          {roles.map((r) => (
            <option key={r} value={r}>
              {roleName(r)}
            </option>
          ))}
        </select>
      </Field>
    </div>
  );
}

function CreateForm({ onCreated, onCancel }: { onCreated: (username: string, inv: Invitation) => void; onCancel: () => void }) {
  const [username, setUsername] = useState("");
  const [role, setRole] = useState<Role>("user");
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { t } = useTranslation();

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const r = await createUser(username.trim(), role, email.trim());
      onCreated(r.user.username, r.invitation);
    } catch (err) {
      setError(usersMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate aria-label={t("users.invite.title")}>
      <div className="ui-narrow">
        <Field label={t("users.invite.username")} hint={t("users.invite.usernameHint")}>
          <input
            className="ui-input"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
          />
        </Field>
      </div>
      <RoleSelect value={role} onChange={setRole} />
      <div className="ui-narrow">
        <Field label={t("users.invite.email")} hint={t("users.invite.emailHint")}>
          <input
            className="ui-input"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="off"
            maxLength={254}
            spellCheck={false}
          />
        </Field>
      </div>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy || username.trim() === ""}>
          {busy ? t("users.invite.creating") : t("users.invite.submit")}
        </Button>
        <Button onClick={onCancel}>{t("common.actions.cancel")}</Button>
      </div>
    </form>
  );
}

function LinkCard({ username, invitation, onClose }: { username: string; invitation: Invitation; onClose: () => void }) {
  const [copied, setCopied] = useState("");
  const { t } = useTranslation();
  const link = inviteLink(invitation.token);

  async function copy() {
    try {
      await navigator.clipboard.writeText(link);
      setCopied(t("common.status.copied"));
    } catch {
      setCopied(t("users.link.copyFailed"));
    }
  }

  return (
    <Card title={t("users.link.title", { name: username })}>
      <div className="ui-stack">
        {invitation.emailedTo && (
          <Notice tone="ok" title={t("users.link.emailed", { email: invitation.emailedTo })}>
            {t("users.link.emailedHint")}
          </Notice>
        )}
        <p className="ui-flush">
          <Trans i18nKey="users.link.text" values={{ name: username }} components={{ time: <RelTime iso={invitation.expiresAt} /> }} />
        </p>
        <input className="ui-input ui-mono" aria-label={t("users.link.label")} value={link} readOnly onFocus={(e) => e.target.select()} />
        {copied && <p role="status" className="ui-hint ui-flush">{copied}</p>}
        <div className="ui-row">
          <Button variant="primary" onClick={() => void copy()}>
            {t("users.link.copy")}
          </Button>
          <Button onClick={onClose}>{t("users.link.done")}</Button>
        </div>
      </div>
    </Card>
  );
}

type ViewProps = {
  account: Account;
  policy: UserPolicy;
  self: boolean;
  sites: string[];
  back: ReactNode;
  onChange: () => Promise<void>;
  onLink: (username: string, inv: Invitation) => void;
  onDeleted: (msg: string) => Promise<void>;
};

// AccountView manages one account: its role, its sites, how it signs in,
// and whether it exists at all.
function AccountView({ account: a, policy, self, sites, back, onChange, onLink, onDeleted }: ViewProps) {
  const [role, setRole] = useState<Role>(a.role);
  const [grants, setGrantsState] = useState<string[] | null>(null);
  const [confirm, setConfirm] = useState<"" | "reset" | "delete">("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const { t } = useTranslation();

  useEffect(() => {
    if (self || a.role !== "user") return;
    getGrants(a.id)
      .then(setGrantsState)
      .catch((err: unknown) => setError(usersMessage(err)));
  }, [a.id, a.role, self]);

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const msg = await f();
      setConfirm("");
      setDone(msg);
      await onChange();
    } catch (err) {
      setError(usersMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const head = (
    <PageHead
      title={t("users.account.title", { name: a.username })}
      description={
        <span className="ui-row">
          {roleName(a.role)}
          <StatusBadges a={a} />
        </span>
      }
      actions={back}
    />
  );

  if (self) {
    return (
      <>
        {head}
        <Notice tone="info" title={t("users.account.self")}>
          {t("users.account.selfText")}
        </Notice>
      </>
    );
  }

  const toggle = (site: string, on: boolean) => setGrantsState((g) => (on ? [...(g ?? []), site] : (g ?? []).filter((s) => s !== site)));

  return (
    <>
      {head}
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}

      <Card title={t("users.account.role")}>
        <form
          className="ui-stack"
          onSubmit={(e) => {
            e.preventDefault();
            void act(async () => {
              await updateUser(a.id, { role });
              return t("users.account.roleChanged");
            });
          }}
        >
          <RoleSelect value={role} onChange={setRole} />
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={busy || role === a.role}>
              {t("users.account.saveRole")}
            </Button>
          </div>
        </form>
      </Card>

      {a.role === "user" && (
        <Card title={t("users.account.sites")}>
          {grants === null ? (
            <p aria-busy="true">{t("users.account.loadingSites")}</p>
          ) : (
            <form
              className="ui-stack"
              onSubmit={(e) => {
                e.preventDefault();
                void act(async () => {
                  const g = await setGrants(a.id, grants);
                  setGrantsState(g);
                  return g.length === 0 ? t("users.account.noneGranted") : t("users.account.granted", { sites: g.join(", ") });
                });
              }}
            >
              <fieldset className="ui-fieldset ui-stack-tight">
                <legend>{t("users.account.sitesLegend", { name: a.username })}</legend>
                {sites.length === 0 && <p className="ui-hint ui-flush">{t("users.account.noSites")}</p>}
                {sites.map((s) => (
                  <label key={s} className="ui-check">
                    <input type="checkbox" checked={grants.includes(s)} onChange={(e) => toggle(s, e.target.checked)} />
                    {s}
                  </label>
                ))}
              </fieldset>
              <div className="ui-row">
                <Button type="submit" variant="primary" disabled={busy}>
                  {t("users.account.saveSites")}
                </Button>
              </div>
            </form>
          )}
        </Card>
      )}

      <Card title={t("users.account.signIn")}>
        <div className="ui-stack">
          {a.role === "user" && policy.requireSecondFactor && (
            <>
              <label className="ui-check">
                <input type="checkbox" checked disabled />
                {t("users.account.requireSecondFactor")}
              </label>
              <p className="ui-hint ui-flush">
                <Trans i18nKey="users.account.requiredForAll" components={{ a: <a href={usersHash()} /> }} />
              </p>
            </>
          )}
          {a.role === "user" && !policy.requireSecondFactor && (
            <label className="ui-check">
              <input
                type="checkbox"
                checked={a.requireSecondFactor}
                disabled={busy}
                onChange={(e) => {
                  const on = e.target.checked;
                  void act(async () => {
                    await updateUser(a.id, { requireSecondFactor: on });
                    return on ? t("users.account.secondFactorRequired") : t("users.account.secondFactorOptional");
                  });
                }}
              />
              {t("users.account.requireSecondFactor")}
            </label>
          )}
          {confirm === "reset" ? (
            <div className="ui-stack" role="alertdialog" aria-labelledby="user-reset-title">
              <h3 id="user-reset-title" className="ui-flush">
                {t("users.account.resetTitle", { name: a.username })}
              </h3>
              <p className="ui-flush">{t("users.account.resetText")}</p>
              <div className="ui-row">
                <Button
                  variant="danger"
                  disabled={busy}
                  onClick={() =>
                    void act(async () => {
                      onLink(a.username, await resetUser(a.id));
                      return "";
                    })
                  }
                >
                  {t("users.account.resetConfirm", { name: a.username })}
                </Button>
                <Button disabled={busy} onClick={() => setConfirm("")}>
                  {t("common.actions.cancel")}
                </Button>
              </div>
            </div>
          ) : (
            <div className="ui-row">
              <Button onClick={() => setConfirm("reset")}>{t("users.account.reset")}</Button>
            </div>
          )}
        </div>
      </Card>

      <Card title={t("users.account.disableTitle")}>
        <div className="ui-stack">
          <p className="ui-flush">
            {a.disabled
              ? t("users.account.disabledText")
              : t("users.account.disableText")}
          </p>
          {confirm === "delete" ? (
            <div className="ui-stack" role="alertdialog" aria-labelledby="user-delete-title">
              <h3 id="user-delete-title" className="ui-flush">
                {t("users.account.deleteTitle", { name: a.username })}
              </h3>
              <p className="ui-flush">{t("users.account.deleteText")}</p>
              <div className="ui-row">
                <Button
                  variant="danger"
                  disabled={busy}
                  onClick={() => {
                    setBusy(true);
                    setError("");
                    deleteUser(a.id)
                      .then(() => onDeleted(t("users.account.deleted", { name: a.username })))
                      .catch((err: unknown) => {
                        setError(usersMessage(err));
                        setBusy(false);
                      });
                  }}
                >
                  {t("users.account.deleteConfirm", { name: a.username })}
                </Button>
                <Button disabled={busy} onClick={() => setConfirm("")}>
                  {t("common.actions.cancel")}
                </Button>
              </div>
            </div>
          ) : (
            <div className="ui-row">
              <Button
                disabled={busy}
                onClick={() =>
                  void act(async () => {
                    await updateUser(a.id, { disabled: !a.disabled });
                    return a.disabled ? t("users.account.enabled") : t("users.account.disabled");
                  })
                }
              >
                {a.disabled ? t("users.account.enable") : t("users.account.disable")}
              </Button>
              <Button variant="danger" onClick={() => setConfirm("delete")}>
                {t("users.account.delete")}
              </Button>
            </div>
          )}
        </div>
      </Card>
    </>
  );
}
