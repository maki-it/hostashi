// SPDX-License-Identifier: AGPL-3.0-or-later

// The SMTP relay the panel sends email through (ADR-0044). Hostashi is not
// a mail server: it hands messages to a relay over STARTTLS or TLS.

import { ApiError, request } from "./api";

export type TLSMode = "starttls" | "tls";

export type MailSettings = {
  configured: boolean;
  host: string;
  port: number;
  tls: TLSMode | "";
  username: string;
  password_set: boolean;
  from: string;
  from_name: string;
  reply_to: string;
  ca_pem: string;
};

export type MailInput = {
  host: string;
  port: number;
  tls: TLSMode;
  username: string;
  /** Left out keeps the stored password, "" removes it. */
  password?: string;
  from: string;
  from_name: string;
  reply_to: string;
  ca_pem: string;
};

export const getMail = () => request<MailSettings>("GET", "/settings/mail");

export const setMail = (input: MailInput) => request<MailSettings>("PUT", "/settings/mail", input);

export const clearMail = () => request<void>("DELETE", "/settings/mail");

export const testMail = (to: string) => request<{ sent: boolean }>("POST", "/settings/mail/test", { to });

/** The usual port of each mode. */
export const defaultPort = (tls: TLSMode) => (tls === "tls" ? 465 : 587);

export function mailMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return "Only administrators can change the email settings.";
      case "invalid_argument":
        return err.message.replace(/^mail: invalid settings: /, "").replace(/^./, (c) => c.toUpperCase()) + ".";
      case "mail_not_configured":
        return "Save the relay first.";
      case "rate_limited":
        return "At most 10 test emails per hour. Try again later.";
      case "mail_failed":
        return "The relay did not take the email: " + err.message;
    }
  }
  return "The request failed. Try again.";
}

export type AccountEmail = {
  email: string;
  pending: string;
  pending_expires?: string;
  /** False while no relay is set up or the panel has no public address. */
  mail_enabled: boolean;
};

export const getAccountEmail = () => request<AccountEmail>("GET", "/account/email");

export const setAccountEmail = (email: string) => request<AccountEmail>("PUT", "/account/email", { email });

export const removeAccountEmail = () => request<void>("DELETE", "/account/email");

export const confirmAccountEmail = (token: string) => request<{ email: string }>("POST", "/account/email/confirm", { token });

export function accountEmailMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "invalid_argument":
        return err.message.charAt(0).toUpperCase() + err.message.slice(1) + ".";
      case "mail_not_configured":
        return "An administrator has to set up email first.";
      case "no_public_url":
        return "The panel has no public address for links. An administrator has to set one.";
      case "rate_limited":
        return "Too many attempts in the last hour. Try again later.";
      case "link_invalid":
        return "The link is invalid, used or expired. Enter the address again on your account page to get a new one.";
      case "mail_failed":
        return "The confirmation email could not be queued. Try again later.";
    }
  }
  return "The request failed. Try again.";
}
