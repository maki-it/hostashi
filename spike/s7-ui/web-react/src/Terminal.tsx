import { useEffect, useRef } from "react";
import { openTerminal } from "../../shared/terminal";

export function Terminal() {
  const el = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let close: (() => void) | undefined;
    let cancelled = false;
    openTerminal(el.current!).then((c) => (cancelled ? c() : (close = c)));
    return () => {
      cancelled = true;
      close?.();
    };
  }, []);
  return <div className="card"><div className="term" ref={el} data-testid="terminal" /></div>;
}
