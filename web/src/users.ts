// SPDX-License-Identifier: AGPL-3.0-or-later

// Panel accounts and their roles (ADR-0035). Only admins manage accounts,
// in a browser session; a new account gets a one-time invitation link.

import { ApiError, request } from "./api";
import type { User } from "./auth";

export type Role = "admin" | "manager" | "user";

export const roles: { value: Role; label: string; hint: string }[] = [
  { value: "admin", label: "Admin", hint: "Everything, including accounts and server settings." },
  { value: "manager", label: "Manager", hint: "Every site, but not the server or the accounts." },
  { value: "user", label: "User", hint: "Only the sites you grant." },
];

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

export type Invitation = { token: string; expiresAt: string };

const userPath = (id: number) => "/users/" + String(id);

export const listUsers = async () => (await request<{ users: Account[] }>("GET", "/users")).users;

export const createUser = (username: string, role: Role) =>
  request<{ user: Account; invitation: Invitation }>("POST", "/users", { username, role });

export const updateUser = (id: number, change: { role?: Role; disabled?: boolean; requireSecondFactor?: boolean }) =>
  request<Account>("PUT", userPath(id), change);

export const deleteUser = (id: number) => request<void>("DELETE", userPath(id));

export const resetUser = async (id: number) => (await request<{ invitation: Invitation }>("POST", userPath(id) + "/reset")).invitation;

export const getGrants = async (id: number) => (await request<{ sites: string[] }>("GET", userPath(id) + "/grants")).sites;

export const setGrants = async (id: number, sites: string[]) =>
  (await request<{ sites: string[] }>("PUT", userPath(id) + "/grants", { sites })).sites;

export const checkInvitation = async (token: string) =>
  (await request<{ username: string }>("POST", "/invitations/check", { token })).username;

export const acceptInvitation = (token: string, password: string) => request<User>("POST", "/invitations/accept", { token, password });

// inviteLink builds the link the new user opens. The token stays in the URL
// fragment, which the browser never sends to a server.
export const inviteLink = (token: string) => window.location.origin + "/#/invite/" + token;

export function usersMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "user_exists":
        return "This username is taken.";
      case "own_account":
        return "You cannot change, reset or delete your own account here.";
      case "last_admin":
        return "This is the last enabled admin. Make another account admin first.";
      case "not_grantee":
        return "Admins and managers reach every site; only users get site grants.";
      case "invitation_invalid":
        return "This invitation link is invalid, used or expired. Ask an admin for a new one.";
      case "rate_limited":
        return "Too many attempts. Wait a few minutes and try again.";
      case "not_found":
        return "This account does not exist any more. Reload the page.";
      case "forbidden":
        return "Only admins can manage accounts.";
      case "invalid_argument":
        return err.message.charAt(0).toUpperCase() + err.message.slice(1) + ".";
      default:
        return "Something went wrong (" + err.code + "). Try again.";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
