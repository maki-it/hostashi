// SPDX-License-Identifier: AGPL-3.0-or-later

import { useId, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

type SwitchProps = {
  checked: boolean;
  onChange: (on: boolean) => void;
  disabled?: boolean | undefined;
  /** The setting, the switch's accessible name. */
  children: ReactNode;
  /** A sentence below the label. */
  hint?: ReactNode;
};

/**
 * A setting that applies the moment it is switched, unlike a checkbox,
 * which belongs to a form with a save button. The card that holds it says
 * "Applies at once" with AppliesAtOnce.
 */
export function Switch({ checked, onChange, disabled, children, hint }: SwitchProps) {
  const id = useId();
  const hintId = `${id}-hint`;
  const labelId = `${id}-label`;
  return (
    <label className="ui-switch">
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        aria-labelledby={labelId}
        aria-describedby={hint !== undefined ? hintId : undefined}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span className="ui-switch-track" aria-hidden="true" />
      <span className="ui-switch-text">
        <span className="ui-switch-label" id={labelId}>
          {children}
        </span>
        {hint !== undefined && (
          <span className="ui-hint" id={hintId}>
            {hint}
          </span>
        )}
      </span>
    </label>
  );
}

/** The note in a card header that its switches need no save button. */
export function AppliesAtOnce() {
  const { t } = useTranslation();
  return <span className="ui-applies">{t("common.form.appliesAtOnce")}</span>;
}

/** The note beside a save button: whether the form has changes. */
export function UnsavedNote({ dirty }: { dirty: boolean }) {
  const { t } = useTranslation();
  return dirty ? <span className="ui-unsaved">{t("common.form.unsaved")}</span> : <span className="ui-hint">{t("common.form.noChanges")}</span>;
}
