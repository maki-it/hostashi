// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, GearSix, Plus } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { formatHash } from "./routes";
import { listSites } from "./sites";
import { Badge, Button, Card, Field, IconLink, Notice, PageHead, RelTime, RowActions, Table, type Column, type Sort } from "./ui";
import {
  createUser,
  deleteUser,
  getGrants,
  inviteLink,
  listUsers,
  resetUser,
  roles,
  setGrants,
  updateUser,
  usersMessage,
  type Account,
  type Invitation,
  type Role,
} from "./users";

const roleLabel = (r: Role) => roles.find((x) => x.value === r)?.label ?? r;

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
  return (
    <span className="ui-row">
      {a.disabled && <Badge tone="err">Disabled</Badge>}
      {a.invited ? (
        a.invitationExpiresAt ? (
          <Badge tone="off">Invited</Badge>
        ) : (
          <Badge tone="warn">Invitation expired</Badge>
        )
      ) : a.totpEnabled || a.passkeys > 0 ? (
        <Badge tone="ok">Second factor on</Badge>
      ) : (
        <Badge tone="warn">No second factor</Badge>
      )}
    </span>
  );
}

// Users lists the panel accounts for admins (ADR-0035): new accounts get a
// one-time invitation link, users get the sites they may work on.
export function Users({ me, query = new URLSearchParams(), onQuery = () => undefined }: { me: string } & QueryProps) {
  const [users, setUsers] = useState<Account[] | null>(null);
  const [sites, setSites] = useState<string[]>([]);
  const [link, setLink] = useState<{ username: string; invitation: Invitation } | null>(null);
  const [done, setDone] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const [list, all] = await Promise.all([listUsers(), listSites()]);
      setUsers(list);
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
      All users
    </a>
  );

  if (!users) {
    return (
      <>
        <PageHead title="Users" />
        {error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>Try again</Button>
          </Notice>
        ) : (
          <p aria-busy="true">Loading users…</p>
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
        <PageHead title="Invite someone" description="The new account gets a one-time link to set its password." actions={listLink} />
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
          <PageHead title="Users" actions={listLink} />
          <Notice tone="warn" title={`There is no account named ${name}.`} />
        </>
      );
    }
    return (
      <AccountView
        key={account.id}
        account={account}
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
      header: "User",
      sortValue: (a) => a.username,
      cell: (a) => (
        <span className="ui-row ui-nowrap">
          {a.username === me ? <strong>{a.username}</strong> : <a href={usersHash({ user: a.username })}>{a.username}</a>}
          {a.username === me && <Badge tone="off">You</Badge>}
        </span>
      ),
    },
    {
      key: "role",
      header: "Role",
      sortValue: (a) => roles.findIndex((r) => r.value === a.role),
      cell: (a) => roleLabel(a.role),
    },
    {
      key: "status",
      header: "Status",
      sortValue: (a) => (a.disabled ? 0 : a.invited ? 1 : a.totpEnabled || a.passkeys > 0 ? 3 : 2),
      cell: (a) => (
        <div className="ui-cell-main">
          <StatusBadges a={a} />
          {a.invited && a.invitationExpiresAt && (
            <p className="ui-hint ui-flush">
              Link expires <RelTime iso={a.invitationExpiresAt} />
            </p>
          )}
        </div>
      ),
    },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (a) =>
        a.username === me ? null : (
          <RowActions>
            <IconLink label={`Manage ${a.username}`} text="Manage" icon={<GearSix size={16} />} href={usersHash({ user: a.username })} />
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
        title="Users"
        description="The accounts of this panel. Admins manage everything, managers every site, users only the sites you grant."
        actions={
          <a className="ui-btn ui-btn-primary" href={usersHash({ new: "1" })}>
            <Plus size={16} aria-hidden="true" />
            Invite someone
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
            aria-label="Filter users"
            placeholder="Filter by name"
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          <select className="ui-select" aria-label="Role" value={role} onChange={(e) => set({ role: e.target.value })}>
            <option value="">All roles</option>
            {roles.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </div>
        <Table
          label="Users"
          columns={columns}
          rows={shown}
          rowKey={(a) => String(a.id)}
          {...(sort ? { sort } : {})}
          onSort={(s) => set({ sort: s.key, dir: s.dir })}
          empty="No account matches the filter."
        />
      </Card>
    </>
  );
}

function RoleSelect({ value, onChange }: { value: Role; onChange: (r: Role) => void }) {
  return (
    <div className="ui-narrow">
      <Field label="Role" hint={roles.find((r) => r.value === value)?.hint ?? ""}>
        <select className="ui-select" value={value} onChange={(e) => onChange(e.target.value as Role)}>
          {roles.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
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
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate aria-label="Invite someone">
      <div className="ui-narrow">
        <Field label="Username" hint="3 to 32 characters: lowercase letters, digits, dot, dash and underscore.">
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
        <Field label="Email (optional)" hint="The link is sent there, and the address becomes the account's once the link is used. Needs email set up.">
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
          {busy ? "Creating…" : "Create invitation"}
        </Button>
        <Button onClick={onCancel}>Cancel</Button>
      </div>
    </form>
  );
}

function LinkCard({ username, invitation, onClose }: { username: string; invitation: Invitation; onClose: () => void }) {
  const [copied, setCopied] = useState("");
  const link = inviteLink(invitation.token);

  async function copy() {
    try {
      await navigator.clipboard.writeText(link);
      setCopied("Copied.");
    } catch {
      setCopied("Copying is not possible here. Select the link and copy it by hand.");
    }
  }

  return (
    <Card title={"Invitation for " + username}>
      <div className="ui-stack">
        {invitation.emailedTo && (
          <Notice tone="ok" title={"The link was emailed to " + invitation.emailedTo + "."}>
            If it does not arrive, send the link below yourself.
          </Notice>
        )}
        <p className="ui-flush">
          Send this link to {username} on a channel you trust. It works once, expires <RelTime iso={invitation.expiresAt} /> and is shown only
          now.
        </p>
        <input className="ui-input ui-mono" aria-label="Invitation link" value={link} readOnly onFocus={(e) => e.target.select()} />
        {copied && <p role="status" className="ui-hint ui-flush">{copied}</p>}
        <div className="ui-row">
          <Button variant="primary" onClick={() => void copy()}>
            Copy link
          </Button>
          <Button onClick={onClose}>Done</Button>
        </div>
      </div>
    </Card>
  );
}

type ViewProps = {
  account: Account;
  self: boolean;
  sites: string[];
  back: ReactNode;
  onChange: () => Promise<void>;
  onLink: (username: string, inv: Invitation) => void;
  onDeleted: (msg: string) => Promise<void>;
};

// AccountView manages one account: its role, its sites, how it signs in,
// and whether it exists at all.
function AccountView({ account: a, self, sites, back, onChange, onLink, onDeleted }: ViewProps) {
  const [role, setRole] = useState<Role>(a.role);
  const [grants, setGrantsState] = useState<string[] | null>(null);
  const [confirm, setConfirm] = useState<"" | "reset" | "delete">("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

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
      title={"User " + a.username}
      description={
        <span className="ui-row">
          {roleLabel(a.role)}
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
        <Notice tone="info" title="This is your own account.">
          Change your password and second factor under Settings. Another admin changes your role.
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

      <Card title="Role">
        <form
          className="ui-stack"
          onSubmit={(e) => {
            e.preventDefault();
            void act(async () => {
              await updateUser(a.id, { role });
              return "Role changed. Its sessions ended.";
            });
          }}
        >
          <RoleSelect value={role} onChange={setRole} />
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={busy || role === a.role}>
              Save role
            </Button>
          </div>
        </form>
      </Card>

      {a.role === "user" && (
        <Card title="Sites">
          {grants === null ? (
            <p aria-busy="true">Loading sites…</p>
          ) : (
            <form
              className="ui-stack"
              onSubmit={(e) => {
                e.preventDefault();
                void act(async () => {
                  const g = await setGrants(a.id, grants);
                  setGrantsState(g);
                  return g.length === 0 ? "No site granted." : "Sites: " + g.join(", ") + ".";
                });
              }}
            >
              <fieldset className="ui-fieldset ui-stack-tight">
                <legend>Sites {a.username} may work on</legend>
                {sites.length === 0 && <p className="ui-hint ui-flush">There are no sites yet.</p>}
                {sites.map((s) => (
                  <label key={s} className="ui-check">
                    <input type="checkbox" checked={grants.includes(s)} onChange={(e) => toggle(s, e.target.checked)} />
                    {s}
                  </label>
                ))}
              </fieldset>
              <div className="ui-row">
                <Button type="submit" variant="primary" disabled={busy}>
                  Save sites
                </Button>
              </div>
            </form>
          )}
        </Card>
      )}

      <Card title="Sign-in">
        <div className="ui-stack">
          {a.role === "user" && (
            <label className="ui-check">
              <input
                type="checkbox"
                checked={a.requireSecondFactor}
                disabled={busy}
                onChange={(e) => {
                  const on = e.target.checked;
                  void act(async () => {
                    await updateUser(a.id, { requireSecondFactor: on });
                    return on ? "A second factor is now required." : "A second factor is now optional.";
                  });
                }}
              />
              Require a second factor
            </label>
          )}
          {confirm === "reset" ? (
            <div className="ui-stack" role="alertdialog" aria-labelledby="user-reset-title">
              <h3 id="user-reset-title" className="ui-flush">
                Reset {a.username}?
              </h3>
              <p className="ui-flush">This removes the password and every second factor and makes a new invitation link. Its sessions end.</p>
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
                  Reset {a.username}
                </Button>
                <Button disabled={busy} onClick={() => setConfirm("")}>
                  Cancel
                </Button>
              </div>
            </div>
          ) : (
            <div className="ui-row">
              <Button onClick={() => setConfirm("reset")}>Reset password and second factor</Button>
            </div>
          )}
        </div>
      </Card>

      <Card title="Disable or delete">
        <div className="ui-stack">
          <p className="ui-flush">
            {a.disabled
              ? "This account is disabled and cannot log in. Its API tokens do not work."
              : "Disabling ends its sessions and stops its API tokens until you enable it again."}
          </p>
          {confirm === "delete" ? (
            <div className="ui-stack" role="alertdialog" aria-labelledby="user-delete-title">
              <h3 id="user-delete-title" className="ui-flush">
                Delete {a.username}?
              </h3>
              <p className="ui-flush">The account and its API tokens are gone for good.</p>
              <div className="ui-row">
                <Button
                  variant="danger"
                  disabled={busy}
                  onClick={() => {
                    setBusy(true);
                    setError("");
                    deleteUser(a.id)
                      .then(() => onDeleted(`Account ${a.username} deleted.`))
                      .catch((err: unknown) => {
                        setError(usersMessage(err));
                        setBusy(false);
                      });
                  }}
                >
                  Delete {a.username}
                </Button>
                <Button disabled={busy} onClick={() => setConfirm("")}>
                  Cancel
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
                    return a.disabled ? "Enabled." : "Disabled. Its sessions ended.";
                  })
                }
              >
                {a.disabled ? "Enable" : "Disable"}
              </Button>
              <Button variant="danger" onClick={() => setConfirm("delete")}>
                Delete account
              </Button>
            </div>
          )}
        </div>
      </Card>
    </>
  );
}
