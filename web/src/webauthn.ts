// SPDX-License-Identifier: AGPL-3.0-or-later

// WebAuthn glue between the JSON the server sends (go-webauthn, binary
// fields as base64url) and the browser API, which wants ArrayBuffers.

export function base64urlToBytes(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(s.length / 4) * 4, "=");
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function bytesToBase64url(buf: ArrayBuffer | ArrayBufferView): string {
  const bytes = buf instanceof ArrayBuffer ? new Uint8Array(buf) : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

type JSONCredentialDescriptor = { id: string; type: "public-key"; transports?: AuthenticatorTransport[] };

type JSONRequestOptions = {
  challenge: string;
  timeout?: number;
  rpId?: string;
  allowCredentials?: JSONCredentialDescriptor[];
  userVerification?: UserVerificationRequirement;
};

// toRequestOptions converts the server's {publicKey: {...}} into what
// navigator.credentials.get expects. Unknown fields are dropped.
export function toRequestOptions(options: unknown): PublicKeyCredentialRequestOptions {
  const pk = (options as { publicKey?: JSONRequestOptions } | null)?.publicKey;
  if (!pk || typeof pk.challenge !== "string") throw new Error("invalid passkey options from server");
  const out: PublicKeyCredentialRequestOptions = { challenge: base64urlToBytes(pk.challenge) };
  if (pk.timeout !== undefined) out.timeout = pk.timeout;
  if (pk.rpId !== undefined) out.rpId = pk.rpId;
  if (pk.userVerification !== undefined) out.userVerification = pk.userVerification;
  if (pk.allowCredentials) {
    out.allowCredentials = pk.allowCredentials.map((c) => {
      const d: PublicKeyCredentialDescriptor = { id: base64urlToBytes(c.id), type: c.type };
      if (c.transports) d.transports = c.transports;
      return d;
    });
  }
  return out;
}

export type AssertionJSON = {
  id: string;
  rawId: string;
  type: string;
  response: { authenticatorData: string; clientDataJSON: string; signature: string; userHandle?: string };
};

export function assertionToJSON(cred: PublicKeyCredential): AssertionJSON {
  const r = cred.response as AuthenticatorAssertionResponse;
  const response: AssertionJSON["response"] = {
    authenticatorData: bytesToBase64url(r.authenticatorData),
    clientDataJSON: bytesToBase64url(r.clientDataJSON),
    signature: bytesToBase64url(r.signature),
  };
  if (r.userHandle) response.userHandle = bytesToBase64url(r.userHandle);
  return { id: cred.id, rawId: bytesToBase64url(cred.rawId), type: cred.type, response };
}

export function passkeysSupported(): boolean {
  return typeof window !== "undefined" && typeof window.PublicKeyCredential === "function" && !!navigator.credentials;
}

export async function getAssertion(options: unknown): Promise<AssertionJSON> {
  const cred = await navigator.credentials.get({ publicKey: toRequestOptions(options) });
  if (!(cred instanceof PublicKeyCredential)) throw new Error("no passkey returned");
  return assertionToJSON(cred);
}

type JSONCreationOptions = {
  challenge: string;
  rp: PublicKeyCredentialRpEntity;
  user: { id: string; name: string; displayName: string };
  pubKeyCredParams: PublicKeyCredentialParameters[];
  timeout?: number;
  excludeCredentials?: JSONCredentialDescriptor[];
  authenticatorSelection?: AuthenticatorSelectionCriteria;
  attestation?: AttestationConveyancePreference;
};

// toCreationOptions converts the server's {publicKey: {...}} into what
// navigator.credentials.create expects. Unknown fields are dropped.
export function toCreationOptions(options: unknown): PublicKeyCredentialCreationOptions {
  const pk = (options as { publicKey?: JSONCreationOptions } | null)?.publicKey;
  if (!pk || typeof pk.challenge !== "string" || !pk.user || typeof pk.user.id !== "string" || !pk.rp) {
    throw new Error("invalid passkey options from server");
  }
  const out: PublicKeyCredentialCreationOptions = {
    challenge: base64urlToBytes(pk.challenge),
    rp: pk.rp,
    user: { id: base64urlToBytes(pk.user.id), name: pk.user.name, displayName: pk.user.displayName },
    pubKeyCredParams: pk.pubKeyCredParams ?? [],
  };
  if (pk.timeout !== undefined) out.timeout = pk.timeout;
  if (pk.authenticatorSelection !== undefined) out.authenticatorSelection = pk.authenticatorSelection;
  if (pk.attestation !== undefined) out.attestation = pk.attestation;
  if (pk.excludeCredentials) {
    out.excludeCredentials = pk.excludeCredentials.map((c) => {
      const d: PublicKeyCredentialDescriptor = { id: base64urlToBytes(c.id), type: c.type };
      if (c.transports) d.transports = c.transports;
      return d;
    });
  }
  return out;
}

export type AttestationJSON = {
  id: string;
  rawId: string;
  type: string;
  response: { attestationObject: string; clientDataJSON: string; transports?: string[] };
};

export function attestationToJSON(cred: PublicKeyCredential): AttestationJSON {
  const r = cred.response as AuthenticatorAttestationResponse;
  const response: AttestationJSON["response"] = {
    attestationObject: bytesToBase64url(r.attestationObject),
    clientDataJSON: bytesToBase64url(r.clientDataJSON),
  };
  if (typeof r.getTransports === "function") response.transports = r.getTransports();
  return { id: cred.id, rawId: bytesToBase64url(cred.rawId), type: cred.type, response };
}

export async function createCredential(options: unknown): Promise<AttestationJSON> {
  const cred = await navigator.credentials.create({ publicKey: toCreationOptions(options) });
  if (!(cred instanceof PublicKeyCredential)) throw new Error("no passkey returned");
  return attestationToJSON(cred);
}
