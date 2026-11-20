// SPDX-License-Identifier: AGPL-3.0-or-later

import { cloneElement, useId, type ReactElement } from "react";

type ControlProps = {
  id?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
};

type FieldProps = {
  label: string;
  hint?: string;
  error?: string;
  /** One input, select or textarea; the field wires id, hint and error to it. */
  children: ReactElement<ControlProps>;
};

/** A label above one control, with an optional hint and error below. */
export function Field({ label, hint, error, children }: FieldProps) {
  const base = useId();
  const id = children.props.id ?? `${base}-control`;
  const hintId = hint !== undefined ? `${base}-hint` : undefined;
  const errorId = error !== undefined ? `${base}-error` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(" ");
  const control = cloneElement(children, {
    id,
    ...(describedBy !== "" && { "aria-describedby": describedBy }),
    ...(error !== undefined && { "aria-invalid": true }),
  });
  return (
    <div className="ui-field">
      <label htmlFor={id}>{label}</label>
      {control}
      {hint !== undefined && (
        <p className="ui-hint" id={hintId}>
          {hint}
        </p>
      )}
      {error !== undefined && (
        <p className="ui-error" id={errorId}>
          {error}
        </p>
      )}
    </div>
  );
}
