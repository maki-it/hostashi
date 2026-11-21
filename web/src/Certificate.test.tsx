// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CertificateCard, PanelCertificate } from "./Certificate";
import { daysLeft, fingerprintText, uploadProblem, type CertInfo } from "./certs";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const pemCert = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----";
const pemKey = "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: {
    enabled: true, active: true, hsts: false, domains: ["blog.example.com"], notAfter: "2099-01-01T00:00:00Z",
    source: "letsencrypt", certSource: "letsencrypt", trusted: true, renewPending: false,
  },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const leCert: CertInfo = {
  certificate: { domains: ["blog.example.com"], issuer: "R11", notBefore: "2026-11-01T00:00:00Z", notAfter: "2099-01-01T00:00:00Z", fingerprint: "ab12cd", trusted: true },
};

const noop = async () => {};

describe("uploadProblem", () => {
  const ok = { certificate: pemCert, chain: "", key: pemKey, allowUntrusted: false };
  it.each([
    ["a good pair", ok, false, ""],
    ["no certificate", { ...ok, certificate: "hello" }, false, "PEM form"],
    ["a bad chain", { ...ok, chain: "junk" }, false, "chain must be"],
    ["a key with the certificate", { ...ok, chain: pemKey.replace("PRIVATE KEY", "CERTIFICATE").replace("PRIVATE KEY", "CERTIFICATE") + "\nPRIVATE KEY" }, false, "key field"],
    ["no key and no CSR", { ...ok, key: "" }, false, "create a CSR"],
    ["no key with a CSR", { ...ok, key: "" }, true, ""],
    ["an encrypted key", { ...ok, key: "-----BEGIN ENCRYPTED PRIVATE KEY-----\nx\n-----END ENCRYPTED PRIVATE KEY-----" }, false, "passphrase"],
    ["an RSA key", { ...ok, key: "-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----" }, false, ""],
    ["too large", { ...ok, chain: pemCert + "x".repeat(33 << 10) }, false, "too large"],
  ] as const)("%s", (_, u, csr, want) => {
    const got = uploadProblem(u, csr);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });

  it("counts days and groups fingerprints", () => {
    expect(daysLeft("2026-12-01T00:00:00Z", new Date("2026-11-21T00:00:00Z"))).toBe(10);
    expect(daysLeft(null)).toBeNull();
    expect(fingerprintText("ab12cd")).toBe("AB:12:CD");
  });
});

describe("CertificateCard", () => {
  it("shows the live certificate and renews it", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/blog/tls/certificate": { body: leCert },
      "POST /api/v1/sites/blog/tls/renew": { status: 202, body: site },
    });
    render(<CertificateCard base="/sites/blog/tls" name="blog" tls={site.tls} onChange={noop} />);
    const card = within(await screen.findByRole("region", { name: "Certificate" }));
    const facts = within(await card.findByLabelText("Live certificate"));
    expect(facts.getByText("R11")).toBeInTheDocument();
    expect(facts.getByText("AB:12:CD")).toBeInTheDocument();
    expect(card.getByLabelText(/^Let's Encrypt/)).toBeChecked();
    // Delete waits for HTTPS to be off.
    expect(card.getByRole("button", { name: /Delete certificate/ })).toBeDisabled();

    await user.click(card.getByRole("button", { name: "Renew now" }));
    expect(await card.findByText(/Renewal requested/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/sites/blog/tls/renew")).toBe(true);
  });

  it("explains a renewal too soon", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/sites/blog/tls/certificate": { body: leCert },
      "POST /api/v1/sites/blog/tls/renew": { status: 429, body: { error: { code: "renew_too_soon", message: "x" } } },
    });
    render(<CertificateCard base="/sites/blog/tls" name="blog" tls={site.tls} onChange={noop} />);
    const card = within(await screen.findByRole("region", { name: "Certificate" }));
    await user.click(card.getByRole("button", { name: "Renew now" }));
    expect(await card.findByText(/less than 10 minutes ago/)).toBeInTheDocument();
  });

  it("uploads an own certificate after checking the form", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/blog/tls/certificate": { body: leCert },
      "PUT /api/v1/sites/blog/tls/certificate": { body: site },
    });
    render(<CertificateCard base="/sites/blog/tls" name="blog" tls={site.tls} onChange={noop} />);
    const card = within(await screen.findByRole("region", { name: "Certificate" }));
    // Choosing "own" opens the upload; the source changes with the upload.
    await user.click(card.getByLabelText(/^Own certificate/));
    const form = within(card.getByRole("form", { name: "Upload certificate" }));

    await user.click(form.getByRole("button", { name: "Install certificate" }));
    expect(form.getByText(/PEM form/)).toBeInTheDocument();

    await user.click(form.getByLabelText("Certificate"));
    await user.paste(pemCert);
    await user.click(form.getByLabelText(/^Private key/));
    await user.paste(pemKey);
    expect(form.getByText(/passes through your browser/)).toBeInTheDocument();
    await user.click(form.getByLabelText(/Not publicly trusted/));
    await user.click(form.getByRole("button", { name: "Install certificate" }));

    expect(await card.findByText(/Certificate installed/)).toBeInTheDocument();
    const put = calls.find((c) => c.method === "PUT");
    expect(put?.body).toEqual({ certificate: pemCert, chain: "", key: pemKey, allowUntrusted: true });
  });

  it("creates a CSR and takes its certificate without a key", async () => {
    const user = userEvent.setup();
    let info: CertInfo = { certificate: null };
    const calls = mockFetch({
      "GET /api/v1/sites/blog/tls/certificate": () => ({ body: info }),
      "POST /api/v1/sites/blog/tls/csr": () => {
        info = { certificate: null, csr: "-----BEGIN CERTIFICATE REQUEST-----\nMIIC\n-----END CERTIFICATE REQUEST-----" };
        return { body: { csr: info.csr } };
      },
      "PUT /api/v1/sites/blog/tls/certificate": { body: site },
    });
    render(<CertificateCard base="/sites/blog/tls" name="blog" tls={{ ...site.tls, enabled: false, active: false }} onChange={noop} />);
    const card = within(await screen.findByRole("region", { name: "Certificate" }));
    expect(await card.findByText("No certificate yet.")).toBeInTheDocument();
    expect(card.queryByRole("button", { name: "Renew now" })).toBeNull();

    await user.click(card.getByRole("button", { name: "Create CSR" }));
    await user.click(card.getByRole("button", { name: "Create key and CSR" }));
    expect(await card.findByDisplayValue(/BEGIN CERTIFICATE REQUEST/)).toBeInTheDocument();

    await user.click(card.getByRole("button", { name: "Upload its certificate" }));
    const form = within(card.getByRole("form", { name: "Upload certificate" }));
    expect(form.getByLabelText(/leave empty for the key of the CSR/)).toBeInTheDocument();
    await user.click(form.getByLabelText("Certificate"));
    await user.paste(pemCert);
    await user.click(form.getByRole("button", { name: "Install certificate" }));
    expect(await card.findByText(/Certificate installed/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ certificate: pemCert, chain: "", key: "", allowUntrusted: false });
  });

  it("deletes a certificate with HTTPS off, after a confirmation", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/blog/tls/certificate": { body: leCert },
      "DELETE /api/v1/sites/blog/tls/certificate": { body: site },
    });
    const off = { ...site, tls: { ...site.tls, enabled: false, active: false } };
    render(<CertificateCard base="/sites/blog/tls" name="blog" tls={off.tls} onChange={noop} />);
    const card = within(await screen.findByRole("region", { name: "Certificate" }));
    await card.findByLabelText("Live certificate");
    await user.click(card.getByRole("button", { name: "Delete certificate" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(within(card.getByRole("group", { name: "Confirm delete" })).getByRole("button", { name: "Delete certificate" }));
    expect(await card.findByText("Certificate deleted.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("switches an own certificate back to Let's Encrypt", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/blog/tls/certificate": { body: { certificate: { ...leCert.certificate, issuer: "Corp CA", trusted: false } } },
      "PUT /api/v1/sites/blog/tls/source": { body: site },
    });
    const own = { ...site, tls: { ...site.tls, source: "custom" as const, certSource: "custom" as const, trusted: false } };
    render(<CertificateCard base="/sites/blog/tls" name="blog" tls={own.tls} onChange={noop} />);
    const card = within(await screen.findByRole("region", { name: "Certificate" }));
    expect(await card.findByText("Not publicly trusted")).toBeInTheDocument();
    expect(card.getByText(/Not renewed automatically/)).toBeInTheDocument();
    expect(card.queryByRole("button", { name: "Renew now" })).toBeNull();
    await user.click(card.getByLabelText(/^Let's Encrypt/));
    expect(await card.findByText(/source again/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ source: "letsencrypt" });
  });
});

describe("PanelCertificate", () => {
  it("shows nothing without a panel address", async () => {
    const calls = mockFetch({ "GET /api/v1/panel/tls": { body: { enabled: false, domain: "", active: false, domains: [], notAfter: null } } });
    const { container } = render(<PanelCertificate />);
    await vi.waitFor(() => expect(calls.length).toBe(1));
    expect(container).toBeEmptyDOMElement();
  });

  it("offers upload and CSR, but no delete, for the panel address", async () => {
    mockFetch({
      "GET /api/v1/panel/tls": {
        body: { enabled: true, domain: "panel.example.com", active: true, domains: ["panel.example.com"], notAfter: "2099-01-01T00:00:00Z", source: "letsencrypt", certSource: "letsencrypt", trusted: true, renewPending: false },
      },
      "GET /api/v1/panel/tls/certificate": { body: leCert },
    });
    render(<PanelCertificate />);
    const card = within(await screen.findByRole("region", { name: "Panel certificate (panel.example.com)" }));
    expect(card.getByRole("button", { name: "Renew now" })).toBeInTheDocument();
    expect(card.getByRole("button", { name: "Upload certificate" })).toBeInTheDocument();
    expect(card.queryByRole("button", { name: /Delete/ })).toBeNull();
  });
});
