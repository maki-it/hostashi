// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowSquareOut } from "@phosphor-icons/react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { durationLabel, openDBTool, safeToolURL, toolMessage, withLook } from "./dbtool";
import { LOCAL_SERVER } from "./databases";
import { Button, Notice } from "./ui";

type Props = {
  db: string;
  // server is the database server's name; LOCAL_SERVER for this machine.
  server: string;
  minutes: number[];
  onClose: () => void;
};

// OpenToolDialog opens one database in Adminer (ADR-0038) for a chosen
// time. The tab is opened in the click itself, so a popup blocker lets it
// through, and gets the address once the server answers. Without a tab the
// dialog shows the link instead; its ticket is valid for a minute.
export function OpenToolDialog({ db, server, minutes, onClose }: Props) {
  const { t: tr } = useTranslation();
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
        {tr("databases.tool.title", { db })}
      </h2>
      <p className="ui-flush">{tr("databases.tool.text", { db })}</p>
      {server !== LOCAL_SERVER && (
        <p className="ui-hint ui-flush">{tr("databases.tool.remote", { server })}</p>
      )}
      {link ? (
        <Notice tone="info" title={tr("databases.tool.blocked")}>
          <a className="ui-btn ui-btn-primary" href={link.url} target="_blank" rel="noopener noreferrer" onClick={onClose}>
            <ArrowSquareOut size={16} aria-hidden="true" />
            {tr("databases.tool.openAdminer")}
          </a>{" "}
          {tr("databases.tool.linkOnce")}
        </Notice>
      ) : (
        <fieldset className="ui-fieldset ui-stack-tight" disabled={busy}>
          <legend>{tr("databases.tool.length")}</legend>
          {minutes.map((m) => (
            <label className="ui-check" key={m}>
              <input type="radio" name={"dbtool-" + db} checked={choice === m} onChange={() => setChoice(m)} />
              {durationLabel(m)}
            </label>
          ))}
          <p className="ui-hint ui-flush">{tr("databases.tool.lengthHint")}</p>
        </fieldset>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        {!link && (
          <Button variant="primary" icon={<ArrowSquareOut size={16} aria-hidden="true" />} disabled={busy} onClick={() => void open()}>
            {tr("databases.tool.open")}
          </Button>
        )}
        <Button disabled={busy} onClick={onClose}>
          {link ? tr("databases.tool.close") : tr("common.actions.cancel")}
        </Button>
      </div>
    </section>
  );
}
