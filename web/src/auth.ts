// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError, request } from "./api";
import i18n, { apiErrorText, type Language } from "./i18n";
import type { ColorScheme } from "./scheme";
import { createCredential, getAssertion } from "./webauthn";

// secondFactorRequired without secondFactor: the account must set up TOTP
// or a passkey before the panel answers anything else (ADR-0035).
export type User = {
  username: string;
  role: string;
  clientIp?: string;
  secondFactor?: boolean;
  secondFactorRequired?: boolean;
  /** Whether an authenticator app (TOTP) is set up. */
  totp?: boolean;
  /** How the panel looks for the account (ADR-0037); missing means the system's scheme. */
  colorScheme?: ColorScheme;
  theme?: string;
  /** The language of the panel (ADR-0071); missing means "auto", the browser's. */
  language?: Language;
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

type Ceremony = { ceremonyId: string; options: unknown };

/** totpReplace starts moving TOTP to a new device; the old app works until totpReplaceConfirm. */
export function totpReplace(password: string, code: string): Promise<{ secret: string; uri: string }> {
  return request<{ secret: string; uri: string }>("POST", "/auth/totp/replace", { password, code });
}

/** totpReplaceConfirm swaps in the new device and returns new recovery codes. */
export async function totpReplaceConfirm(code: string): Promise<string[]> {
  const r = await request<{ recoveryCodes: string[] }>("POST", "/auth/totp/replace/confirm", { code });
  return r.recoveryCodes;
}

/** totpDisable turns TOTP off; it needs the password and a current or recovery code. */
export function totpDisable(password: string, code: string): Promise<void> {
  return request<void>("POST", "/auth/totp/disable", { password, code });
}

/** changePassword sets a new password; the other sessions of the account end. */
export function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  return request<void>("POST", "/auth/password", { currentPassword, newPassword });
}

export type Passkey = { id: number; name: string; createdAt: string; lastUsedAt: string | null };

export async function listPasskeys(): Promise<Passkey[]> {
  const r = await request<{ passkeys: Passkey[] }>("GET", "/auth/passkeys");
  return Array.isArray(r.passkeys) ? r.passkeys : [];
}

/** addPasskey asks the browser for a new passkey and stores it under name. */
export async function addPasskey(name: string): Promise<Passkey> {
  const begin = await request<Ceremony>("POST", "/auth/passkeys/register/begin");
  const credential = await createCredential(begin.options);
  return request<Passkey>("POST", "/auth/passkeys/register/finish", { ceremonyId: begin.ceremonyId, name, credential });
}

export function deletePasskey(id: number): Promise<void> {
  return request<void>("DELETE", "/auth/passkeys/" + id);
}

export function logout(): Promise<void> {
  return request<void>("POST", "/auth/logout");
}


export async function loginWithPasskey(): Promise<User> {
  const begin = await request<Ceremony>("POST", "/auth/passkey/login/begin");
  const credential = await getAssertion(begin.options);
  return request<User>("POST", "/auth/passkey/login/finish", { ceremonyId: begin.ceremonyId, credential });
}

// message turns an error into a sentence for the login and setup forms,
// in the current language. Known API codes come from the catalog
// (errors.<code>); invalid_argument shows the server's English reason,
// any other code names itself.
export function message(err: unknown): string {
  if (err instanceof ApiError && err.code !== "invalid_argument" && !i18n.exists(`errors.${err.code}`)) {
    return i18n.t("common.errors.unknown", { code: err.code });
  }
  if (err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "AbortError")) {
    return i18n.t("auth.login.passkeyClosed");
  }
  return apiErrorText(err);
}
