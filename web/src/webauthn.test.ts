// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { base64urlToBytes, bytesToBase64url, toRequestOptions } from "./webauthn";

describe("base64url", () => {
  it("round-trips every byte value and length", () => {
    for (let n = 0; n < 40; n++) {
      const bytes = new Uint8Array(n).map((_, i) => (i * 37 + n * 11) % 256);
      const s = bytesToBase64url(bytes);
      expect(s).not.toMatch(/[+/=]/);
      expect(Array.from(base64urlToBytes(s))).toEqual(Array.from(bytes));
    }
  });

  it("decodes the URL alphabet", () => {
    expect(Array.from(base64urlToBytes("-_8"))).toEqual([0xfb, 0xff]);
    expect(bytesToBase64url(new Uint8Array([0xfb, 0xff]))).toBe("-_8");
  });

  it("encodes views at an offset", () => {
    const buf = new Uint8Array([1, 2, 3, 4]);
    expect(bytesToBase64url(buf.subarray(1, 3))).toBe(bytesToBase64url(new Uint8Array([2, 3])));
  });
});

describe("toRequestOptions", () => {
  it("decodes challenge and credential ids", () => {
    const o = toRequestOptions({
      publicKey: {
        challenge: "AQID",
        timeout: 60000,
        rpId: "panel.example.com",
        userVerification: "required",
        allowCredentials: [{ id: "BAU", type: "public-key", transports: ["usb"] }],
        extra: "ignored",
      },
      mediation: "optional",
    });
    expect(Array.from(new Uint8Array(o.challenge as ArrayBuffer))).toEqual([1, 2, 3]);
    expect(o.rpId).toBe("panel.example.com");
    expect(o.userVerification).toBe("required");
    expect(o.timeout).toBe(60000);
    expect(o.allowCredentials).toHaveLength(1);
    expect(Array.from(new Uint8Array(o.allowCredentials?.[0]?.id as ArrayBuffer))).toEqual([4, 5]);
    expect(o).not.toHaveProperty("extra");
  });

  it("rejects options without a challenge", () => {
    expect(() => toRequestOptions(null)).toThrow();
    expect(() => toRequestOptions({})).toThrow();
    expect(() => toRequestOptions({ publicKey: { challenge: 1 } })).toThrow();
  });
});
