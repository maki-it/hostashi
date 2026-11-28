// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, Copy, DownloadSimple, FileText, Trash, UploadSimple } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type ChangeEvent, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  certInfo,
  panelCertBase,
  panelTLS,
  type PanelTLS,
  createCSR,
  daysLeft,
  deleteCert,
  fingerprintText,
  renewCert,
  setCertChallenge,
  setCertSource,
  uploadCert,
  type CertChallenge,
  uploadProblem,
  type CertInfo,
  type CertState,
  type CertUpload,
} from "./certs";
import { formatHash } from "./routes";
import { siteMessage } from "./sites";
import { Badge, Button, Card, CopyValue, Field, IconButton, Notice } from "./ui";

type Props = {
  /** The API path of the certificate routes, from certs.ts. */
  base: string;
  /** Names the radio group and the CSR download. */
  name: string;
  tls: CertState;
  /** false for the panel: it never goes without a certificate. */
  deletable?: boolean;
  /** Says how HTTPS is turned off before a delete. */
  offHint?: string;
  title?: string;
  /**
   * Shows the choice of HTTP-01 or DNS-01 (ADR-0076); editable for
   * admins. Left out for parked domains, which use HTTP only.
   */
  challenge?: { editable: boolean };
  onChange: () => Promise<void>;
};
type Mode = "" | "upload" | "csr" | "delete";

// Names that read the same in every language.
const letsEncrypt = "Let's Encrypt";
const sha256 = "SHA-256";

const emptyUpload: CertUpload = { certificate: "", chain: "", key: "", allowUntrusted: false };

// CertificateCard shows the live certificate of a site, a parked domain or
// the panel and its actions (ADR-0039): renew now, upload an own
// certificate, create a CSR, delete.
export function CertificateCard({ base, name, tls, deletable = true, offHint, title, challenge, onChange }: Props) {
  const { t } = useTranslation();
  const [info, setInfo] = useState<CertInfo | null>(null);
  const [loadError, setLoadError] = useState("");
  const [mode, setMode] = useState<Mode>("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      setInfo(await certInfo(base));
      setLoadError("");
    } catch (err) {
      setLoadError(siteMessage(err));
    }
  }, [base]);

  useEffect(() => {
    void load();
  }, [load]);

  async function act(f: () => Promise<string>, next: Mode = "") {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const msg = await f();
      await onChange();
      await load();
      setMode(next);
      setDone(msg);
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const open = (m: Mode) => {
    setError("");
    setDone("");
    setMode(mode === m ? "" : m);
  };

  const own = tls.source === "custom";
  const cert = info?.certificate ?? null;
  const canRenew = !own && tls.enabled;
  const canDelete = deletable && cert !== null && !tls.enabled && !tls.active;

  const chooseSource = (v: string) => {
    if (v === "custom") {
      // An own certificate starts with an upload; the source changes with it.
      if (!own) open("upload");
      return;
    }
    void act(async () => {
      await setCertSource(base, "letsencrypt");
      return t("certificate.done.letsencrypt");
    });
  };

  return (
    <Card
      title={title ?? t("certificate.title")}
      actions={
        <>
          {canRenew && (
            <IconButton
              label={t("certificate.renew")}
              text={t("certificate.renew")}
              icon={<ArrowClockwise />}
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await renewCert(base);
                  return t("certificate.done.renew");
                })
              }
            />
          )}
          <IconButton label={t("certificate.upload.title")} text={t("certificate.upload.short")} icon={<UploadSimple />} disabled={busy} onClick={() => open("upload")} />
          <IconButton label={t("certificate.csr.create")} text={t("certificate.csr.create")} icon={<FileText />} disabled={busy} onClick={() => open("csr")} />
          {deletable && (
            <IconButton
              label={canDelete ? t("certificate.delete.submit") : t("certificate.delete.blocked", { hint: offHint ?? t("certificate.delete.offFirst") })}
              text={t("certificate.delete.short")}
              icon={<Trash />}
              danger
              disabled={busy || !canDelete}
              onClick={() => open("delete")}
            />
          )}
        </>
      }
    >
      <div className="ui-stack">
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>{t("certificate.source.legend")}</legend>
          <label className="ui-check">
            <input type="radio" name={name + "-cert-source"} value="letsencrypt" checked={!own} disabled={busy} onChange={(e) => chooseSource(e.target.value)} />
            {t("certificate.source.letsencrypt")}
          </label>
          <label className="ui-check">
            <input type="radio" name={name + "-cert-source"} value="custom" checked={own} disabled={busy} onChange={(e) => chooseSource(e.target.value)} />
            {t("certificate.source.custom")}
          </label>
        </fieldset>

        {challenge && !own && (
          <ChallengeChoice
            name={name}
            value={tls.challenge ?? "http"}
            editable={challenge.editable}
            busy={busy}
            onChoose={(c) =>
              void act(async () => {
                await setCertChallenge(base, c);
                return t(`dnsAccounts.challenge.done.${c}`);
              })
            }
          />
        )}

        {loadError && <Notice tone="err" title={loadError} />}
        {info && !cert && <p className="ui-hint">{t("certificate.none")}</p>}
        {cert && <CertFacts own={tls.certSource === "custom"} cert={cert} />}
        {tls.renewPending && <p className="ui-hint">{t("certificate.renewPending")}</p>}

        {mode === "upload" && (
          <UploadForm busy={busy} withCSR={Boolean(info?.csr)} onCancel={() => setMode("")} onSubmit={(u) =>
            void act(async () => {
              await uploadCert(base, u);
              return t("certificate.done.installed");
            })
          } />
        )}
        {mode === "csr" && (
          <CSRPanel
            name={name}
            csr={info?.csr ?? ""}
            busy={busy}
            onCreate={() =>
              void act(async () => {
                await createCSR(base);
                return t("certificate.done.csr");
              }, "csr")
            }
            onUpload={() => open("upload")}
          />
        )}
        {mode === "delete" && (
          <div className="ui-stack" role="group" aria-label={t("certificate.delete.confirmLabel")}>
            <Notice tone="warn" title={t("certificate.delete.question")}>
              {t("certificate.delete.text")}
            </Notice>
            <div className="ui-row">
              <Button
                variant="danger"
                disabled={busy}
                onClick={() =>
                  void act(async () => {
                    await deleteCert(base);
                    return t("certificate.done.deleted");
                  })
                }
              >
                {t("certificate.delete.submit")}
              </Button>
              <Button onClick={() => setMode("")}>{t("common.actions.cancel")}</Button>
            </div>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </div>
    </Card>
  );
}

// ChallengeChoice is how Let's Encrypt checks the domains: a file on port
// 80, or a TXT record through a DNS account (ADR-0076).
function ChallengeChoice({ name, value, editable, busy, onChoose }: { name: string; value: CertChallenge; editable: boolean; busy: boolean; onChoose: (c: CertChallenge) => void }) {
  const { t } = useTranslation();
  return (
    <fieldset className="ui-fieldset ui-stack-tight">
      <legend>{t("dnsAccounts.challenge.legend")}</legend>
      {(["http", "dns"] as const).map((c) => (
        <label key={c} className="ui-check">
          <input type="radio" name={name + "-cert-challenge"} value={c} checked={value === c} disabled={busy || !editable} onChange={() => onChoose(c)} />
          {t(`dnsAccounts.challenge.${c}`)}
        </label>
      ))}
      <p className="ui-hint">
        {editable ? (
          <Trans i18nKey="dnsAccounts.challenge.noAccounts" components={{ a: <a href={formatHash({ page: "dns", query: new URLSearchParams() })} /> }} />
        ) : (
          t("dnsAccounts.challenge.adminOnly")
        )}
      </p>
    </fieldset>
  );
}

function CertFacts({ own, cert }: { own: boolean; cert: NonNullable<CertInfo["certificate"]> }) {
  const { t } = useTranslation();
  const left = daysLeft(cert.notAfter);
  return (
    <dl className="ui-facts" aria-label={t("certificate.facts.label")}>
      <dt>{t("certificate.facts.from")}</dt>
      <dd>
        {own ? t("certificate.facts.own") : letsEncrypt} {!cert.trusted && <Badge tone="warn">{t("certificate.facts.untrusted")}</Badge>}
      </dd>
      <dt>{t("certificate.facts.issuer")}</dt>
      <dd>{cert.issuer || t("certificate.facts.unknown")}</dd>
      <dt>{t("certificate.facts.names")}</dt>
      <dd>{cert.domains.join(", ")}</dd>
      <dt>{t("certificate.facts.validUntil")}</dt>
      <dd>
        {cert.notAfter?.slice(0, 10)}
        {left !== null && left < 30 && (
          <>
            {" "}
            <Badge tone={left < 7 ? "err" : "warn"}>{left < 0 ? t("certificate.facts.expired") : t("certificate.facts.daysLeft", { count: left })}</Badge>
          </>
        )}
        {left !== null && !own && <span className="ui-hint"> {t("certificate.facts.renewedBefore")}</span>}
        {left !== null && own && <span className="ui-hint"> {t("certificate.facts.notRenewed")}</span>}
      </dd>
      <dt>{sha256}</dt>
      <dd className="ui-mono ui-break">
        <CopyValue value={fingerprintText(cert.fingerprint)} label={t("certificate.facts.copyFingerprint")} />
      </dd>
    </dl>
  );
}

type UploadFormProps = { busy: boolean; withCSR: boolean; onCancel: () => void; onSubmit: (u: CertUpload) => void };

function UploadForm({ busy, withCSR, onCancel, onSubmit }: UploadFormProps) {
  const { t } = useTranslation();
  const [u, setU] = useState<CertUpload>(emptyUpload);
  const [problem, setProblem] = useState("");

  const set = (k: keyof CertUpload) => (e: ChangeEvent<HTMLTextAreaElement>) => setU({ ...u, [k]: e.target.value });

  // readFile fills a field from a chosen file; nothing is sent until submit.
  const readFile = (k: "certificate" | "chain" | "key") => async (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;
    setU({ ...u, [k]: await f.text() });
  };

  function submit(e: FormEvent) {
    e.preventDefault();
    const p = uploadProblem(u, withCSR);
    setProblem(p);
    if (!p) onSubmit(u);
  }

  return (
    <form className="ui-stack" onSubmit={submit} noValidate aria-label={t("certificate.upload.title")}>
      <Field label={t("certificate.upload.certificate")} hint={t("certificate.upload.certificateHint")}>
        <textarea className="ui-input ui-textarea ui-mono" rows={5} value={u.certificate} onChange={set("certificate")} spellCheck={false} />
      </Field>
      <input type="file" className="ui-file" aria-label={t("certificate.upload.certificateFile")} accept=".pem,.crt,.cer" onChange={(e) => void readFile("certificate")(e)} />
      <Field label={t("certificate.upload.chain")} hint={t("certificate.upload.chainHint")}>
        <textarea className="ui-input ui-textarea ui-mono" rows={4} value={u.chain} onChange={set("chain")} spellCheck={false} />
      </Field>
      <input type="file" className="ui-file" aria-label={t("certificate.upload.chainFile")} accept=".pem,.crt,.cer" onChange={(e) => void readFile("chain")(e)} />
      <Field
        label={withCSR ? t("certificate.upload.keyWithCSR") : t("certificate.upload.key")}
        hint={t("certificate.upload.keyHint")}
      >
        <textarea className="ui-input ui-textarea ui-mono" rows={4} value={u.key} onChange={set("key")} spellCheck={false} autoComplete="off" />
      </Field>
      <input type="file" className="ui-file" aria-label={t("certificate.upload.keyFile")} accept=".pem,.key" onChange={(e) => void readFile("key")(e)} />
      {u.key.trim() !== "" && (
        <Notice tone="info" title={t("certificate.upload.keyNotice")}>
          {t("certificate.upload.keyNoticeText")}
        </Notice>
      )}
      <label className="ui-check">
        <input type="checkbox" checked={u.allowUntrusted} onChange={(e) => setU({ ...u, allowUntrusted: e.target.checked })} />
        {t("certificate.upload.allowUntrusted")}
      </label>
      {problem && <Notice tone="err" title={problem} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          {t("certificate.upload.submit")}
        </Button>
        <Button onClick={onCancel}>{t("common.actions.cancel")}</Button>
      </div>
    </form>
  );
}

type CSRPanelProps = { name: string; csr: string; busy: boolean; onCreate: () => void; onUpload: () => void };

function CSRPanel({ name, csr, busy, onCreate, onUpload }: CSRPanelProps) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  async function copy() {
    await navigator.clipboard.writeText(csr);
    setCopied(true);
  }

  function download() {
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([csr], { type: "application/pkcs10" }));
    a.download = name + ".csr";
    a.click();
    URL.revokeObjectURL(a.href);
  }

  if (!csr) {
    return (
      <div className="ui-stack">
        <p className="ui-hint">{t("certificate.csr.intro")}</p>
        <div className="ui-row">
          <Button variant="primary" disabled={busy} onClick={onCreate}>
            {t("certificate.csr.createKey")}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="ui-stack">
      <Field label={t("certificate.csr.label")} hint={t("certificate.csr.hint")}>
        <textarea className="ui-input ui-textarea ui-mono" rows={6} value={csr} readOnly />
      </Field>
      <div className="ui-row">
        <Button icon={<Copy />} onClick={() => void copy()}>
          {copied ? t("certificate.csr.copied") : t("certificate.csr.copy")}
        </Button>
        <Button icon={<DownloadSimple />} onClick={download}>
          {t("certificate.csr.download")}
        </Button>
        <Button variant="primary" onClick={onUpload}>
          {t("certificate.csr.uploadCert")}
        </Button>
        <Button disabled={busy} onClick={onCreate}>
          {t("certificate.csr.createNew")}
        </Button>
      </div>
    </div>
  );
}

// PanelCertificate is the certificate of the panel's own address, on the
// System page (ADR-0039). Without an address of its own there is none.
export function PanelCertificate() {
  const { t } = useTranslation();
  const [tls, setTLS] = useState<PanelTLS | null>(null);

  const load = useCallback(async () => {
    try {
      setTLS(await panelTLS());
    } catch {
      setTLS(null);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!tls?.enabled) return null;
  return (
    <CertificateCard
      base={panelCertBase}
      name="panel"
      title={t("certificate.panelTitle", { domain: tls.domain })}
      tls={{ ...tls, enabled: true }}
      deletable={false}
      challenge={{ editable: true }}
      onChange={load}
    />
  );
}
