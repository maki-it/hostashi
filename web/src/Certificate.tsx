// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, Copy, DownloadSimple, FileText, Trash, UploadSimple } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type ChangeEvent, type FormEvent } from "react";
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
  setCertSource,
  uploadCert,
  uploadProblem,
  type CertInfo,
  type CertState,
  type CertUpload,
} from "./certs";
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
  onChange: () => Promise<void>;
};
type Mode = "" | "upload" | "csr" | "delete";

const emptyUpload: CertUpload = { certificate: "", chain: "", key: "", allowUntrusted: false };

// CertificateCard shows the live certificate of a site, a parked domain or
// the panel and its actions (ADR-0039): renew now, upload an own
// certificate, create a CSR, delete.
export function CertificateCard({ base, name, tls, deletable = true, offHint = "turn HTTPS off first", title = "Certificate", onChange }: Props) {
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
      return "Let's Encrypt is the source again. The own certificate serves until the new one is there.";
    });
  };

  return (
    <Card
      title={title}
      actions={
        <>
          {canRenew && (
            <IconButton
              label="Renew now"
              text="Renew now"
              icon={<ArrowClockwise />}
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await renewCert(base);
                  return "Renewal requested. The new certificate is there in a minute or two.";
                })
              }
            />
          )}
          <IconButton label="Upload certificate" text="Upload" icon={<UploadSimple />} disabled={busy} onClick={() => open("upload")} />
          <IconButton label="Create CSR" text="Create CSR" icon={<FileText />} disabled={busy} onClick={() => open("csr")} />
          {deletable && (
            <IconButton
              label={canDelete ? "Delete certificate" : `Delete certificate (${offHint})`}
              text="Delete"
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
          <legend>Source</legend>
          <label className="ui-check">
            <input type="radio" name={name + "-cert-source"} value="letsencrypt" checked={!own} disabled={busy} onChange={(e) => chooseSource(e.target.value)} />
            Let's Encrypt, renewed automatically
          </label>
          <label className="ui-check">
            <input type="radio" name={name + "-cert-source"} value="custom" checked={own} disabled={busy} onChange={(e) => chooseSource(e.target.value)} />
            Own certificate, you renew it yourself
          </label>
        </fieldset>

        {loadError && <Notice tone="err" title={loadError} />}
        {info && !cert && <p className="ui-hint">No certificate yet.</p>}
        {cert && <CertFacts own={tls.certSource === "custom"} cert={cert} />}
        {tls.renewPending && <p className="ui-hint">A renewal is on its way.</p>}

        {mode === "upload" && (
          <UploadForm busy={busy} withCSR={Boolean(info?.csr)} onCancel={() => setMode("")} onSubmit={(u) =>
            void act(async () => {
              await uploadCert(base, u);
              return "Certificate installed and served over HTTPS.";
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
                return "CSR created. Have it signed, then upload the certificate here without a key.";
              }, "csr")
            }
            onUpload={() => open("upload")}
          />
        )}
        {mode === "delete" && (
          <div className="ui-stack" role="group" aria-label="Confirm delete">
            <Notice tone="warn" title="Delete the certificate and its key?">
              HTTPS is off, so nothing serves this certificate any more. A pending CSR is deleted too.
            </Notice>
            <div className="ui-row">
              <Button
                variant="danger"
                disabled={busy}
                onClick={() =>
                  void act(async () => {
                    await deleteCert(base);
                    return "Certificate deleted.";
                  })
                }
              >
                Delete certificate
              </Button>
              <Button onClick={() => setMode("")}>Cancel</Button>
            </div>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </div>
    </Card>
  );
}

function CertFacts({ own, cert }: { own: boolean; cert: NonNullable<CertInfo["certificate"]> }) {
  const left = daysLeft(cert.notAfter);
  return (
    <dl className="ui-facts" aria-label="Live certificate">
      <dt>From</dt>
      <dd>
        {own ? "Own certificate" : "Let's Encrypt"} {!cert.trusted && <Badge tone="warn">Not publicly trusted</Badge>}
      </dd>
      <dt>Issuer</dt>
      <dd>{cert.issuer || "unknown"}</dd>
      <dt>Names</dt>
      <dd>{cert.domains.join(", ")}</dd>
      <dt>Valid until</dt>
      <dd>
        {cert.notAfter?.slice(0, 10)}
        {left !== null && left < 30 && (
          <>
            {" "}
            <Badge tone={left < 7 ? "err" : "warn"}>{left < 0 ? "expired" : `${left} days left`}</Badge>
          </>
        )}
        {left !== null && !own && <span className="ui-hint"> Renewed 30 days before.</span>}
        {left !== null && own && <span className="ui-hint"> Not renewed automatically.</span>}
      </dd>
      <dt>SHA-256</dt>
      <dd className="ui-mono ui-break">
        <CopyValue value={fingerprintText(cert.fingerprint)} label="Copy the SHA-256 fingerprint" />
      </dd>
    </dl>
  );
}

type UploadFormProps = { busy: boolean; withCSR: boolean; onCancel: () => void; onSubmit: (u: CertUpload) => void };

function UploadForm({ busy, withCSR, onCancel, onSubmit }: UploadFormProps) {
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
    <form className="ui-stack" onSubmit={submit} noValidate aria-label="Upload certificate">
      <Field label="Certificate" hint="PEM, starting with -----BEGIN CERTIFICATE-----.">
        <textarea className="ui-input ui-textarea ui-mono" rows={5} value={u.certificate} onChange={set("certificate")} spellCheck={false} />
      </Field>
      <input type="file" className="ui-file" aria-label="Certificate file" accept=".pem,.crt,.cer" onChange={(e) => void readFile("certificate")(e)} />
      <Field label="Chain (optional)" hint="The intermediate certificates of your CA, if they are not in the certificate field.">
        <textarea className="ui-input ui-textarea ui-mono" rows={4} value={u.chain} onChange={set("chain")} spellCheck={false} />
      </Field>
      <input type="file" className="ui-file" aria-label="Chain file" accept=".pem,.crt,.cer" onChange={(e) => void readFile("chain")(e)} />
      <Field
        label={withCSR ? "Private key (leave empty for the key of the CSR)" : "Private key"}
        hint="PEM without passphrase. RSA with at least 2048 bits, or ECDSA P-256 or P-384."
      >
        <textarea className="ui-input ui-textarea ui-mono" rows={4} value={u.key} onChange={set("key")} spellCheck={false} autoComplete="off" />
      </Field>
      <input type="file" className="ui-file" aria-label="Key file" accept=".pem,.key" onChange={(e) => void readFile("key")(e)} />
      {u.key.trim() !== "" && (
        <Notice tone="info" title="The key passes through your browser and the panel.">
          It is stored on the server only, readable by root. With “Create CSR” the key never leaves the server.
        </Notice>
      )}
      <label className="ui-check">
        <input type="checkbox" checked={u.allowUntrusted} onChange={(e) => setU({ ...u, allowUntrusted: e.target.checked })} />
        Not publicly trusted (internal CA or self-signed): install anyway
      </label>
      {problem && <Notice tone="err" title={problem} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          Install certificate
        </Button>
        <Button onClick={onCancel}>Cancel</Button>
      </div>
    </form>
  );
}

type CSRPanelProps = { name: string; csr: string; busy: boolean; onCreate: () => void; onUpload: () => void };

function CSRPanel({ name, csr, busy, onCreate, onUpload }: CSRPanelProps) {
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
        <p className="ui-hint">
          The server creates a key that never leaves it, and a certificate signing request (CSR) for its domains. Have the CSR
          signed by your CA, then upload the certificate here without a key.
        </p>
        <div className="ui-row">
          <Button variant="primary" disabled={busy} onClick={onCreate}>
            Create key and CSR
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="ui-stack">
      <Field label="Certificate signing request" hint="Waiting for its certificate. Creating a new one replaces the key.">
        <textarea className="ui-input ui-textarea ui-mono" rows={6} value={csr} readOnly />
      </Field>
      <div className="ui-row">
        <Button icon={<Copy />} onClick={() => void copy()}>
          {copied ? "Copied" : "Copy"}
        </Button>
        <Button icon={<DownloadSimple />} onClick={download}>
          Download
        </Button>
        <Button variant="primary" onClick={onUpload}>
          Upload its certificate
        </Button>
        <Button disabled={busy} onClick={onCreate}>
          Create a new CSR
        </Button>
      </div>
    </div>
  );
}

// PanelCertificate is the certificate of the panel's own address, on the
// System page (ADR-0039). Without an address of its own there is none.
export function PanelCertificate() {
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
      title={"Panel certificate (" + tls.domain + ")"}
      tls={{ ...tls, enabled: true }}
      deletable={false}
      onChange={load}
    />
  );
}
