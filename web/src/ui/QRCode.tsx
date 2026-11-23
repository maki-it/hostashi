// SPDX-License-Identifier: AGPL-3.0-or-later

import { useMemo } from "react";
import { encode } from "uqr";

type Props = {
  /** The text to encode, for example an otpauth:// URI. */
  value: string;
  /** Accessible name of the image. */
  label: string;
};

/**
 * qrPath turns the module matrix into one SVG path of 1×1 squares, so the
 * code renders without inline styles (the panel's CSP forbids them).
 */
export function qrPath(data: boolean[][]): string {
  const parts: string[] = [];
  data.forEach((row, y) => {
    row.forEach((dark, x) => {
      if (dark) parts.push(`M${x} ${y}h1v1h-1z`);
    });
  });
  return parts.join("");
}

/**
 * A QR code as an SVG image. It is always dark on white with a quiet zone,
 * whatever the colour scheme, because scanners need that contrast.
 */
export function QRCode({ value, label }: Props) {
  const qr = useMemo(() => encode(value, { ecc: "M", border: 4 }), [value]);
  return (
    <svg className="ui-qr" role="img" aria-label={label} viewBox={`0 0 ${qr.size} ${qr.size}`} shapeRendering="crispEdges">
      <rect className="ui-qr-bg" width={qr.size} height={qr.size} />
      <path className="ui-qr-fg" d={qrPath(qr.data)} />
    </svg>
  );
}
