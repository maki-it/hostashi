// SPDX-License-Identifier: AGPL-3.0-or-later

import { Plus } from "@phosphor-icons/react";
import { useEffect, useState, type FormEvent } from "react";
import { parseList } from "./firewall";
import { getTokenAllowlist, setTokenAllowlist, tokenMessage } from "./tokens";
import { Button, Card, Field, Notice } from "./ui";

// TokenAllowlistCard limits where API tokens may be used from (ADR-0018).
// It is not part of the firewall: it applies at once, has no rollback and
// works when Hostashi's firewall is off. Browser logins are not affected, so
// a wrong list cannot lock an admin out of the panel. The server itself is
// always allowed.
export function TokenAllowlistCard({ clientIp }: { clientIp?: string | undefined }) {
  const [saved, setSaved] = useState<string[] | null>(null);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);

  useEffect(() => {
    getTokenAllowlist().then(
      (l) => {
        setSaved(l);
        setText(l.join("\n"));
      },
      (err: unknown) => setError(tokenMessage(err)),
    );
  }, []);

  const list = parseList(text);
  const changed = saved !== null && list.join("\n") !== saved.join("\n");

  const add = (entry: string) => {
    if (!list.includes(entry)) setText([...list, entry].join("\n"));
    setDone(false);
  };

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setDone(false);
    try {
      const l = await setTokenAllowlist(list);
      setSaved(l);
      setText(l.join("\n"));
      setDone(true);
    } catch (err) {
      setError(tokenMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="API tokens">
      {saved === null && !error ? (
        <p aria-busy="true">Loading…</p>
      ) : (
        <form className="ui-stack" onSubmit={(e) => void save(e)}>
          <p className="ui-hint ui-flush">
            Addresses API tokens are accepted from, one IP address or CIDR range per line. An empty list accepts them from anywhere. This
            is not a firewall rule: it applies at once, to every account&apos;s tokens, and logins in the browser keep working. The server
            itself (127.0.0.1 and ::1) is always allowed, so the hostashi command on it keeps working.
          </p>
          <div className="ui-narrow">
            <Field label="Allowed addresses">
              <textarea
                className="ui-input ui-textarea ui-mono"
                value={text}
                onChange={(e) => {
                  setText(e.target.value);
                  setDone(false);
                }}
                rows={4}
                spellCheck={false}
                placeholder="Anywhere"
              />
            </Field>
          </div>
          {clientIp && (
            <div className="ui-row">
              <Button icon={<Plus size={16} aria-hidden="true" />} onClick={() => add(clientIp)}>
                Add my address
              </Button>
            </div>
          )}
          {error && <Notice tone="err" title={error} />}
          {done && !changed && (
            <Notice tone="ok" title={saved?.length ? "Saved. Tokens now work only from these addresses and the server itself." : "Saved. Tokens work from anywhere."} />
          )}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={busy || !changed}>
              {busy ? "Saving…" : "Save"}
            </Button>
          </div>
        </form>
      )}
    </Card>
  );
}
