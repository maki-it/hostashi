// SPDX-License-Identifier: AGPL-3.0-or-later

// Panel accounts and their roles (ADR-0035). Only admins manage accounts,
// in a browser session; a new account gets a one-time invitation link.

import { request } from "./api";
import i18n, { areaErrorText } from "./i18n";
import type { User } from "./auth";

export type Role = "admin" | "manager" | "user";

export const roles: readonly Role[] = ["admin", "manager", "user"];

/** roleName is the role's name for lists and selects, such as "Admin". */
export const roleName = (r: Role): string => (roles.includes(r) ? i18n.t(`users.roles.${r}`) : r);

/** roleHint says in one sentence what the role may do. */
export const roleHint = (r: Role): string => (roles.includes(r) ? i18n.t(`users.roles.${r}Hint`) : "");

export type Account = {
  id: number;
  username: string;
  role: Role;
  disabled: boolean;
  totpEnabled: boolean;
  passkeys: number;
  requireSecondFactor: boolean;
  invited: boolean;
  invitationExpiresAt: string | null;
};

export type Invitation = {
  token: string;
  expiresAt: string;
  /** The address the link was emailed to (ADR-0044); missing when it is only shown. */
  emailedTo?: string;
};

const userPath = (id: number) => "/users/" + String(id);

export const listUsers = async () => (await request<{ users: Account[] }>("GET", "/users")).users;

export const createUser = (username: string, role: Role, email = "") =>
  request<{ user: Account; invitation: Invitation }>("POST", "/users", email === "" ? { username, role } : { username, role, email });

export const updateUser = (id: number, change: { role?: Role; disabled?: boolean; requireSecondFactor?: boolean }) =>
  request<Account>("PUT", userPath(id), change);

export const deleteUser = (id: number) => request<void>("DELETE", userPath(id));

export const resetUser = async (id: number) => (await request<{ invitation: Invitation }>("POST", userPath(id) + "/reset")).invitation;

/** What applies to every account (ADR-0035, addendum 1). */
export type UserPolicy = { requireSecondFactor: boolean };

export const getUserPolicy = () => request<UserPolicy>("GET", "/users/policy");

export const setUserPolicy = (requireSecondFactor: boolean) => request<UserPolicy>("PUT", "/users/policy", { requireSecondFactor });

/** hasSecondFactor reports whether the account has TOTP or a passkey. */
export const hasSecondFactor = (a: Account) => a.totpEnabled || a.passkeys > 0;

export const getGrants = async (id: number) => (await request<{ sites: string[] }>("GET", userPath(id) + "/grants")).sites;

export const setGrants = async (id: number, sites: string[]) =>
  (await request<{ sites: string[] }>("PUT", userPath(id) + "/grants", { sites })).sites;

export const checkInvitation = async (token: string) =>
  (await request<{ username: string }>("POST", "/invitations/check", { token })).username;

export const acceptInvitation = (token: string, password: string) => request<User>("POST", "/invitations/accept", { token, password });

// inviteLink builds the link the new user opens. The token stays in the URL
// fragment, which the browser never sends to a server.
export const inviteLink = (token: string) => window.location.origin + "/#/invite/" + token;

/** usersMessage explains an error of the account routes in the current language. */
export const usersMessage = (err: unknown): string => areaErrorText("users.errors", err);
