// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type FormEvent, type MouseEvent, type ReactNode } from "react";
import { Button } from "./ui";

// The cards of a tab each save on their own. useUnsaved notices typing in
// a form that was not saved yet, names its card, and holds a switch to
// another tab until the change is saved or dropped. It watches the DOM,
// so the cards need no changes: after a submit or a click on the main
// button, a success notice (role=status) in the card makes the form clean
// again, an error (role=alert) leaves it unsaved.
export function useUnsaved(key: string) {
  const forms = useRef(new Set<HTMLFormElement>());
  const saving = useRef(new Set<HTMLFormElement>());
  const box = useRef<HTMLDivElement | null>(null);
  const [names, setNames] = useState<string[]>([]);
  const [pending, setPending] = useState<(() => void) | null>(null);

  const refresh = () =>
    setNames(
      [...forms.current]
        .filter((f) => f.isConnected)
        .map((f) => f.closest("section[aria-label]")?.getAttribute("aria-label") ?? "this tab")
        .filter((n, i, all) => all.indexOf(n) === i),
    );

  useEffect(() => {
    forms.current.clear();
    saving.current.clear();
    setNames([]);
    setPending(null);
  }, [key]);

  // settle decides a save by the notice its card shows afterwards.
  const settle = () => {
    let changed = false;
    for (const f of saving.current) {
      const card = f.closest("section[aria-label]") ?? f;
      if (card.querySelector('[role="alert"]')) saving.current.delete(f);
      else if (card.querySelector('[role="status"]')) {
        saving.current.delete(f);
        changed = forms.current.delete(f) || changed;
      }
    }
    if (changed) refresh();
  };

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const obs = new MutationObserver(settle);
    obs.observe(el, { childList: true, subtree: true });
    return () => obs.disconnect();
  });

  useEffect(() => {
    if (names.length === 0) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [names.length]);

  const formOf = (t: EventTarget | null) => (t instanceof Element ? t.closest("form") : null);
  const watch = {
    ref: box,
    onInputCapture: (e: FormEvent) => {
      const f = formOf(e.target);
      if (!f || f.role === "search" || f.dataset.unsaved === "ignore") return;
      forms.current.add(f);
      refresh();
    },
    onSubmitCapture: (e: FormEvent) => {
      const f = formOf(e.target);
      if (f && forms.current.has(f)) saving.current.add(f);
    },
    onClickCapture: (e: MouseEvent) => {
      const b = e.target instanceof Element ? e.target.closest(".ui-btn-primary") : null;
      const f = formOf(b);
      if (f && forms.current.has(f)) saving.current.add(f);
    },
  };

  /** guard runs go now, or after the person drops the unsaved changes. */
  const guard = (go: () => void) => {
    refresh();
    if ([...forms.current].some((f) => f.isConnected)) setPending(() => go);
    else go();
  };

  const bar: ReactNode =
    names.length > 0 ? (
      <div className="ui-unsaved" role="status">
        <p>
          {pending ? "Save or drop the changes first: " : "Not saved yet: "}
          <strong>{names.join(", ")}</strong>
        </p>
        {pending && (
          <>
            <Button onClick={() => setPending(null)}>Stay here</Button>
            <Button
              variant="danger"
              onClick={() => {
                const go = pending;
                forms.current.clear();
                setNames([]);
                setPending(null);
                go();
              }}
            >
              Drop changes
            </Button>
          </>
        )}
      </div>
    ) : null;

  return { watch, guard, bar };
}
