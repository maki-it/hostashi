// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { listSites } from "./sites";
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

function status(a: Account): string {
  const parts: string[] = [];
  if (a.disabled) parts.push("disabled");
  if (a.invited) {
    parts.push(a.invitationExpiresAt ? "invited until " + new Date(a.invitationExpiresAt).toLocaleString() : "invitation expired");
  }
  if (!a.invited) parts.push(a.totpEnabled || a.passkeys > 0 ? "second factor on" : "no second factor");
  return parts.join(", ");
}

// Users lists the panel accounts for admins (ADR-0035): new accounts get a
// one-time invitation link, users get the sites they may work on.
export function Users({ me }: { me: string }) {
  const [users, setUsers] = useState<Account[] | null>(null);
  const [sites, setSites] = useState<string[]>([]);
  const [link, setLink] = useState<{ username: string; invitation: Invitation } | null>(null);
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

  if (!users) {
    return error ? (
      <p role="alert" className="card">
        {error}
      </p>
    ) : (
      <p aria-busy="true">Loading…</p>
    );
  }

  const showLink = (username: string, invitation: Invitation) => setLink({ username, invitation });

  return (
    <div className="stack">
      <CreateCard
        onCreated={(u, inv) => {
          showLink(u, inv);
          void load();
        }}
      />
      {link && <LinkCard username={link.username} invitation={link.invitation} onClose={() => setLink(null)} />}
      <section className="card" aria-labelledby="users-list">
        <h2 id="users-list">Accounts</h2>
        {error && <p role="alert">{error}</p>}
        <ul className="rows">
          {users.map((u) => (
            <UserRow key={u.id} account={u} self={u.username === me} sites={sites} onChange={load} onLink={showLink} />
          ))}
        </ul>
      </section>
    </div>
  );
}

function RoleSelect({ value, onChange }: { value: Role; onChange: (r: Role) => void }) {
  return (
    <label>
      Role
      <small>{roles.find((r) => r.value === value)?.hint}</small>
      <select value={value} onChange={(e) => onChange(e.target.value as Role)}>
        {roles.map((r) => (
          <option key={r.value} value={r.value}>
            {r.label}
          </option>
        ))}
      </select>
    </label>
  );
}

function CreateCard({ onCreated }: { onCreated: (username: string, inv: Invitation) => void }) {
  const [username, setUsername] = useState("");
  const [role, setRole] = useState<Role>("user");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const r = await createUser(username.trim(), role);
      setUsername("");
      onCreated(r.user.username, r.invitation);
    } catch (err) {
      setError(usersMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card" aria-labelledby="user-new">
      <h2 id="user-new">Invite someone</h2>
      <form onSubmit={(e) => void submit(e)} noValidate>
        <label>
          Username
          <small>3 to 32 characters: lowercase letters, digits, dot, dash and underscore.</small>
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" autoCapitalize="none" spellCheck={false} />
        </label>
        <RoleSelect value={role} onChange={setRole} />
        {error && <p role="alert">{error}</p>}
        <button type="submit" disabled={busy || username.trim() === ""}>
          {busy ? "Creating…" : "Create invitation"}
        </button>
      </form>
    </section>
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
    <section className="card notice" aria-labelledby="invite-link">
      <h2 id="invite-link">Invitation for {username}</h2>
      <p>
        Send this link to {username} on a channel you trust. It works once, until {new Date(invitation.expiresAt).toLocaleString()}, and is
        shown only now.
      </p>
      <input aria-label="Invitation link" value={link} readOnly onFocus={(e) => e.target.select()} />
      {copied && <p role="status">{copied}</p>}
      <div className="actions">
        <button type="button" onClick={() => void copy()}>
          Copy link
        </button>
        <button type="button" className="secondary" onClick={onClose}>
          Done
        </button>
      </div>
    </section>
  );
}

type RowProps = {
  account: Account;
  self: boolean;
  sites: string[];
  onChange: () => Promise<void>;
  onLink: (username: string, inv: Invitation) => void;
};

function UserRow({ account: a, self, sites, onChange, onLink }: RowProps) {
  const [mode, setMode] = useState<"" | "role" | "sites" | "reset" | "delete">("");
  const [role, setRole] = useState<Role>(a.role);
  const [grants, setGrantsState] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const msg = await f();
      setMode("");
      setDone(msg);
      await onChange();
    } catch (err) {
      setError(usersMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function openSites() {
    setError("");
    try {
      setGrantsState(await getGrants(a.id));
      setMode("sites");
    } catch (err) {
      setError(usersMessage(err));
    }
  }

  const toggle = (site: string, on: boolean) =>
    setGrantsState((g) => (on ? [...(g ?? []), site] : (g ?? []).filter((s) => s !== site)));

  return (
    <li>
      <span>
        <strong>{a.username}</strong> {roleLabel(a.role)}
        {self && " (you)"}
        <small className="muted"> {status(a)}</small>
      </span>
      {mode === "" && !self && (
        <span className="actions">
          <button
            type="button"
            className="secondary"
            onClick={() => {
              setRole(a.role);
              setMode("role");
            }}
          >
            Change role
          </button>
          {a.role === "user" && (
            <button type="button" className="secondary" onClick={() => void openSites()}>
              Sites
            </button>
          )}
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() =>
              void act(async () => {
                await updateUser(a.id, { disabled: !a.disabled });
                return a.disabled ? "Enabled." : "Disabled. Its sessions ended.";
              })
            }
          >
            {a.disabled ? "Enable" : "Disable"}
          </button>
          <button type="button" className="secondary" onClick={() => setMode("reset")}>
            Reset
          </button>
          <button type="button" className="secondary danger" onClick={() => setMode("delete")}>
            Delete
          </button>
        </span>
      )}
      {mode === "" && !self && a.role === "user" && (
        <label className="check">
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
      {mode === "role" && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act(async () => {
              await updateUser(a.id, { role });
              return "Role changed. Its sessions ended.";
            });
          }}
        >
          <RoleSelect value={role} onChange={setRole} />
          <span className="actions">
            <button type="submit" disabled={busy || role === a.role}>
              Save
            </button>
            <button type="button" className="secondary" onClick={() => setMode("")}>
              Cancel
            </button>
          </span>
        </form>
      )}
      {mode === "sites" && grants && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act(async () => {
              const g = await setGrants(a.id, grants);
              return g.length === 0 ? "No site granted." : "Sites: " + g.join(", ") + ".";
            });
          }}
        >
          <fieldset>
            <legend>Sites {a.username} may work on</legend>
            {sites.length === 0 && <p className="muted">There are no sites yet.</p>}
            {sites.map((s) => (
              <label key={s} className="check">
                <input type="checkbox" checked={grants.includes(s)} onChange={(e) => toggle(s, e.target.checked)} />
                {s}
              </label>
            ))}
          </fieldset>
          <span className="actions">
            <button type="submit" disabled={busy}>
              Save sites
            </button>
            <button type="button" className="secondary" onClick={() => setMode("")}>
              Cancel
            </button>
          </span>
        </form>
      )}
      {mode === "reset" && (
        <span className="actions">
          <span>Remove the password and second factors of {a.username} and make a new invitation link?</span>
          <button
            type="button"
            disabled={busy}
            onClick={() =>
              void act(async () => {
                onLink(a.username, await resetUser(a.id));
                return "Reset. Send the new link.";
              })
            }
          >
            Reset
          </button>
          <button type="button" className="secondary" onClick={() => setMode("")}>
            Cancel
          </button>
        </span>
      )}
      {mode === "delete" && (
        <span className="actions">
          <span>Delete {a.username}? Its API tokens stop working.</span>
          <button
            type="button"
            className="danger"
            disabled={busy}
            onClick={() =>
              void act(async () => {
                await deleteUser(a.id);
                return "Deleted.";
              })
            }
          >
            Delete
          </button>
          <button type="button" className="secondary" onClick={() => setMode("")}>
            Cancel
          </button>
        </span>
      )}
      {error && <p role="alert">{error}</p>}
      {done && <p role="status">{done}</p>}
    </li>
  );
}
