// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { accountEmailMessage, confirmAccountEmail } from "./mail";
import { Button, Card, Notice } from "./ui";

// ConfirmEmail is the page of an email confirmation link (ADR-0044). It
// works without a session: the link itself is the proof.
export function ConfirmEmail({ token, onDone }: { token: string; onDone: () => void }) {
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const started = useRef(false);

  useEffect(() => {
    // The link works once; React's development double run must not use it up.
    if (started.current) return;
    started.current = true;
    confirmAccountEmail(token).then(
      (r) => setResult({ ok: true, text: "Confirmed. Hostashi sends email to " + r.email + " from now on." }),
      (err: unknown) => setResult({ ok: false, text: accountEmailMessage(err) }),
    );
    // The link is used up; keep it out of the history and the address bar.
    window.history.replaceState(null, "", "#/");
  }, [token]);

  return (
    <Card title="Confirm your email address">
      <div className="ui-stack">
        {result === null ? <p aria-busy="true">Checking the link…</p> : <Notice tone={result.ok ? "ok" : "err"} title={result.text} />}
        {result !== null && (
          <div className="ui-auth-actions">
            <Button variant="primary" onClick={onDone}>
              Open the panel
            </Button>
          </div>
        )}
      </div>
    </Card>
  );
}
