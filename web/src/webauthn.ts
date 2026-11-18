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
