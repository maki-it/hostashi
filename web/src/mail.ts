// SPDX-License-Identifier: AGPL-3.0-or-later

// The SMTP relay the panel sends email through (ADR-0044). Hostashi is not
// a mail server: it hands messages to a relay over STARTTLS or TLS.

import { ApiError, request } from "./api";
import { areaErrorText } from "./i18n";

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

/** accountEmailMessage explains an error of the account's email address in the current language. */
export const accountEmailMessage = (err: unknown): string => areaErrorText("account.email.errors", err);

export type NotifyCategory = "backups" | "certificates" | "doctor" | "services" | "deploys" | "php" | "disk";

export type Notifications = {
  /** The confirmed address; empty while there is none. */
  email: string;
  mail_enabled: boolean;
  /** Only the categories the account's role may get. */
  categories: { id: NotifyCategory; enabled: boolean }[];
};

export const getNotifications = () => request<Notifications>("GET", "/account/notifications");

export const setNotifications = (changes: Partial<Record<NotifyCategory, boolean>>) =>
  request<Notifications>("PUT", "/account/notifications", changes);
