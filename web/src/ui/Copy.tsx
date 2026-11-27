// SPDX-License-Identifier: AGPL-3.0-or-later

import { Check, Copy } from "@phosphor-icons/react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** copyText puts text on the clipboard; false where the browser refuses. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/**
 * CopyButton copies a value and says "Copied" for a moment. Where the
 * browser refuses, it asks to copy by hand.
 */
export function CopyButton({ text, label }: { text: string; label: string }) {
  const { t } = useTranslation();
  const [state, setState] = useState<"" | "copied" | "failed">("");
  useEffect(() => {
    if (state !== "copied") return;
    const t = setTimeout(() => setState(""), 2000);
    return () => clearTimeout(t);
  }, [state]);
  return (
    <span className="ui-copy-btn">
      <button type="button" className="ui-btn ui-btn-sm" aria-label={label} onClick={() => void copyText(text).then((ok) => setState(ok ? "copied" : "failed"))}>
        {state === "copied" ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
        {state === "copied" ? t("ui.copy.copied") : t("ui.copy.copy")}
      </button>
      {state === "failed" && <span className="ui-hint">{t("ui.copy.byHand")}</span>}
    </span>
  );
}

/** CopyValue shows a value in code style with a copy button beside it. */
export function CopyValue({ value, label }: { value: string; label: string }) {
  return (
    <span className="ui-copy">
      <code>{value}</code>
      <CopyButton text={value} label={label} />
    </span>
  );
}
