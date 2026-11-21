// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError, request } from "./api";
import type { ColorScheme } from "./scheme";
import { getAssertion } from "./webauthn";

// secondFactorRequired without secondFactor: the account must set up TOTP
// or a passkey before the panel answers anything else (ADR-0035).
export type User = {
  username: string;
  role: string;
  clientIp?: string;
  secondFactor?: boolean;
  secondFactorRequired?: boolean;
  /** How the panel looks for the account (ADR-0037); missing means the system's scheme. */
  colorScheme?: ColorScheme;
  theme?: string;
};

export const needsSecondFactor = (u: User): boolean => !!u.secondFactorRequired && !u.secondFactor;

export async function setupRequired(): Promise<boolean> {
  const r = await request<{ required: boolean }>("GET", "/setup");
  return r.required;
}

export function setup(token: string, username: string, password: string): Promise<User> {
  return request<User>("POST", "/setup", { token, username, password });
}

// currentUser returns null when the browser has no valid session.
export async function currentUser(): Promise<User | null> {
  try {
    return await request<User>("GET", "/auth/me");
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return null;
    throw err;
  }
}

// login sends an empty totp on the first step. The server answers
// totp_required when the account has a second factor.
export function login(username: string, password: string, totp = ""): Promise<User> {
  return request<User>("POST", "/auth/login", { username, password, totp });
}

export function totpEnroll(): Promise<{ secret: string; uri: string }> {
  return request<{ secret: string; uri: string }>("POST", "/auth/totp/enroll");
}

export async function totpConfirm(code: string): Promise<string[]> {
  const r = await request<{ recoveryCodes: string[] }>("POST", "/auth/totp/confirm", { code });
  return r.recoveryCodes;
}

export function logout(): Promise<void> {
  return request<void>("POST", "/auth/logout");
}

type Ceremony = { ceremonyId: string; options: unknown };

export async function loginWithPasskey(): Promise<User> {
  const begin = await request<Ceremony>("POST", "/auth/passkey/login/begin");
  const credential = await getAssertion(begin.options);
  return request<User>("POST", "/auth/passkey/login/finish", { ceremonyId: begin.ceremonyId, credential });
}

// message turns an API error into a sentence for the login and setup forms.
export function message(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "invalid_credentials":
        return "Wrong username or password.";
      case "rate_limited":
        return "Too many attempts. Wait a few minutes and try again.";
      case "passkey_required":
        return "This account logs in with a passkey.";
      case "passkeys_unavailable":
        return "Passkeys are not set up on this server. Log in with your password.";
      case "passkey_failed":
        return "The passkey was not accepted.";
      case "setup_not_allowed":
        return "The setup token is wrong or has expired.";
      case "invalid_argument":
        return err.message.charAt(0).toUpperCase() + err.message.slice(1) + ".";
      case "busy":
        return "The server is busy. Try again in a minute.";
      default:
        return "Something went wrong (" + err.code + "). Try again.";
    }
  }
  if (err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "AbortError")) {
    return "The passkey prompt was closed or timed out.";
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
