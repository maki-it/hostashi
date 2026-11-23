// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { encode } from "uqr";
import { qrPath, QRCode } from "./QRCode";

describe("QRCode", () => {
  it("draws one square per dark module", () => {
    expect(qrPath([[true, false], [false, true]])).toBe("M0 0h1v1h-1zM1 1h1v1h-1z");
    expect(qrPath([[false]])).toBe("");
  });

  it("renders a labelled SVG with a quiet zone", () => {
    const uri = "otpauth://totp/Hostashi:kim?secret=JBSWY3DPEHPK3PXP&issuer=Hostashi";
    render(<QRCode value={uri} label="QR code" />);
    const svg = screen.getByRole("img", { name: "QR code" });
    const qr = encode(uri, { ecc: "M", border: 4 });
    expect(svg).toHaveAttribute("viewBox", `0 0 ${qr.size} ${qr.size}`);
    expect(svg.querySelector("path")).toHaveAttribute("d", qrPath(qr.data));
    // The four module quiet zone stays white.
    for (const row of [0, 1, 2, 3, qr.size - 1]) expect(qr.data[row]?.some(Boolean)).toBe(false);
  });

  it("encodes the same text to the same code", () => {
    const a = encode("otpauth://totp/x?secret=AAAA", { ecc: "M", border: 4 });
    const b = encode("otpauth://totp/x?secret=AAAA", { ecc: "M", border: 4 });
    expect(qrPath(a.data)).toBe(qrPath(b.data));
    expect(qrPath(a.data)).not.toBe(qrPath(encode("otpauth://totp/x?secret=BBBB", { ecc: "M", border: 4 }).data));
  });
});
