// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ApiError } from "./api";
import { message, totpConfirm, totpEnroll } from "./auth";
import { Copy } from "@phosphor-icons/react";
import { Button, Card, Field, Notice, QRCode } from "./ui";

type Props = { onDone: () => void };

/** groupKey splits a base32 key into groups of four for reading it off the screen. */
export function groupKey(secret: string): string {
  return secret.replace(/(.{4})(?=.)/g, "$1 ");
}

// SecondFactor sets up TOTP for an account that must have a second factor
// and has none yet (ADR-0035). Until then the panel refuses everything else.
export function SecondFactor({ onDone }: Props) {
  const { t } = useTranslation();
  return (
    <Card title={t("auth.totp.title")}>
      <TotpEnrolment intro={t("auth.totp.intro")} onDone={onDone} />
    </Card>
  );
}

type Enrolment = { secret: string; uri: string };

type EnrolmentProps = {
  intro: string;
  onDone: () => void;
  autoStart?: boolean;
  /** initial skips the first step; moving to a new device gets its secret elsewhere. */
  initial?: Enrolment;
  /** confirm checks the first code; it defaults to turning TOTP on. */
  confirm?: (code: string) => Promise<string[]>;
  /** The submit button; defaults to "Turn on". */
  submitLabel?: string;
  /** The notice above the recovery codes; defaults to "is on". */
  doneTitle?: string;
};

// TotpEnrolment walks through setting up an authenticator app: QR code,
// first code, recovery codes. The account page uses it too, also for moving
// to a new device.
export function TotpEnrolment({
  intro,
  onDone,
  autoStart = false,
  initial,
  confirm: confirmCode = totpConfirm,
  submitLabel,
  doneTitle,
}: EnrolmentProps) {
  const { t } = useTranslation();
  const [enrolment, setEnrolment] = useState<Enrolment | null>(initial ?? null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");

  async function copyKey(secret: string) {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(t("common.status.copied"));
    } catch {
      setCopied(t("auth.totp.copyFailed"));
    }
  }

  async function start() {
    setBusy(true);
    setError("");
    try {
      setEnrolment(await totpEnroll());
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  // autoStart asks for the secret at once; the ref keeps a second effect
  // run (React's strict mode) from asking twice.
  const started = useRef(false);
  useEffect(() => {
    if (autoStart && !started.current) {
      started.current = true;
      void start();
    }
  });

  async function confirm(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      setCodes(await confirmCode(code.trim()));
    } catch (err) {
      setError(err instanceof ApiError && err.code === "invalid_credentials" ? t("auth.totp.wrongCode") : message(err));
      setCode("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="ui-stack">
      {codes ? (
        <>
          <Notice tone="ok" title={doneTitle ?? t("auth.totp.done")} />
          <ul aria-label={t("auth.totp.recoveryCodes")}>
            {codes.map((c) => (
              <li key={c}>
                <code>{c}</code>
              </li>
            ))}
          </ul>
          <Button variant="primary" onClick={onDone}>
            {t("common.actions.continue")}
          </Button>
        </>
      ) : enrolment ? (
        <form className="ui-stack" onSubmit={(e) => void confirm(e)} noValidate>
          <p className="ui-flush">{t("auth.totp.scan")}</p>
          <QRCode value={enrolment.uri} label={t("auth.totp.qr")} />
          <div className="ui-stack-tight">
            <p className="ui-flush">{t("auth.totp.cannotScan")}</p>
            <pre className="ui-code" aria-label={t("auth.totp.key")}>
              {groupKey(enrolment.secret)}
            </pre>
            <div className="ui-row">
              <Button icon={<Copy size={16} aria-hidden="true" />} onClick={() => void copyKey(enrolment.secret)}>
                {t("auth.totp.copyKey")}
              </Button>
              <a className="ui-btn" href={enrolment.uri}>
                {t("auth.totp.openApp")}
              </a>
              {copied && (
                <span role="status" className="ui-hint">
                  {copied}
                </span>
              )}
            </div>
          </div>
          <Field label={t("auth.totp.code")}>
            <input
              className="ui-input"
              name="code"
              inputMode="numeric"
              autoComplete="one-time-code"
              required
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </Field>
          <Button variant="primary" type="submit" disabled={busy || code.trim() === ""}>
            {submitLabel ?? t("auth.totp.turnOn")}
          </Button>
        </form>
      ) : (
        <>
          {intro && <p className="ui-flush">{intro}</p>}
          <Button variant="primary" disabled={busy} onClick={() => void start()}>
            {t("auth.totp.start")}
          </Button>
        </>
      )}
      {error && <Notice tone="err" title={error} />}
    </div>
  );
}
