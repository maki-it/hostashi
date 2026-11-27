// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowSquareOut } from "@phosphor-icons/react";
import { useEffect, useRef, useState } from "react";
import { durationLabel, openDBTool, safeToolURL, toolMessage, withLook } from "./dbtool";
import { LOCAL_SERVER } from "./databases";
import { Button, Notice } from "./ui";

type Props = {
  db: string;
  // server is the database server's name; LOCAL_SERVER for this machine.
  server: string;
  // plain is set when the panel reaches that server without TLS.
  plain?: boolean;
  minutes: number[];
  onClose: () => void;
};

// OpenToolDialog opens one database in Adminer (ADR-0038) for a chosen
// time. The tab is opened in the click itself, so a popup blocker lets it
// through, and gets the address once the server answers. Without a tab the
// dialog shows the link instead; its ticket is valid for a minute.
export function OpenToolDialog({ db, server, plain = false, minutes, onClose }: Props) {
  const [choice, setChoice] = useState(minutes[0] ?? 30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [link, setLink] = useState<{ url: string; expires: string } | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    heading.current?.focus();
  }, [db]);

  async function open() {
    setBusy(true);
    setError("");
    const tab = window.open("about:blank", "_blank");
    try {
      const s = await openDBTool(db, choice);
      const safe = safeToolURL(s.url);
      if (!safe) throw new Error("bad address");
      const url = withLook(safe, document.documentElement.dataset.scheme);
      if (tab) {
        tab.opener = null;
        tab.location.replace(url);
        onClose();
        return;
      }
      setLink({ url, expires: s.expires });
    } catch (err) {
      tab?.close();
      setError(toolMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ui-card ui-card-body ui-stack" role="dialog" aria-labelledby="dbtool-title">
      <h2 id="dbtool-title" className="ui-flush" tabIndex={-1} ref={heading}>
        Open {db} in Adminer
      </h2>
      <p className="ui-flush">
        Adminer opens in a new tab with a temporary database user for {db} only. It runs any SQL on this database, including deleting tables;
        backups are the safety net.
      </p>
      {server !== LOCAL_SERVER && (
        <p className="ui-hint ui-flush">
          Adminer connects from this machine to {server}, where the panel creates the temporary user. The first session on a server, or one after
          its address changed, ends the Adminer sessions open at that moment.
        </p>
      )}
      {plain && (
        <Notice tone="warn" title={`${server} is set up without TLS.`}>
          The temporary user's password and everything Adminer shows or changes cross the network unencrypted. Use this only inside a private
          network you trust, or switch the server to TLS under Database servers.
        </Notice>
      )}
      {link ? (
        <Notice tone="info" title="Your browser blocked the new tab.">
          <a className="ui-btn ui-btn-primary" href={link.url} target="_blank" rel="noopener noreferrer" onClick={onClose}>
            <ArrowSquareOut size={16} aria-hidden="true" />
            Open Adminer
          </a>{" "}
          The link works once, within a minute.
        </Notice>
      ) : (
        <fieldset className="ui-fieldset ui-stack-tight" disabled={busy}>
          <legend>Session length</legend>
          {minutes.map((m) => (
            <label className="ui-check" key={m}>
              <input type="radio" name={"dbtool-" + db} checked={choice === m} onChange={() => setChoice(m)} />
              {durationLabel(m)}
            </label>
          ))}
          <p className="ui-hint ui-flush">The temporary user and its connections end after this time.</p>
        </fieldset>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        {!link && (
          <Button variant="primary" icon={<ArrowSquareOut size={16} aria-hidden="true" />} disabled={busy} onClick={() => void open()}>
            Open in Adminer
          </Button>
        )}
        <Button disabled={busy} onClick={onClose}>
          {link ? "Close" : "Cancel"}
        </Button>
      </div>
    </section>
  );
}
