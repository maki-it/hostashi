// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import { getHeaders, headersMessage, setHeaders, type HeadersView } from "./headers";
import type { Site } from "./sites";
import { Button, Card, Notice } from "./ui";

// SearchEnginesCard switches X-Robots-Tag: noindex, nofollow for the whole
// site. It is stored with the security headers (ADR-0056, ADR-0070) and
// on for staging copies.
export function SearchEnginesCard({ site }: { site: Site }) {
  const [view, setView] = useState<HeadersView | null>(null);
  const [noIndex, setNoIndex] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  useEffect(() => {
    getHeaders(site.name)
      .then((v) => {
        setView(v);
        setNoIndex(v.headers.noIndex === true);
      })
      .catch((err: unknown) => setError(headersMessage(err)));
  }, [site.name]);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!view) return;
    setBusy(true);
    setError("");
    setDone("");
    try {
      const headers = { ...view.headers };
      if (noIndex) headers.noIndex = true;
      else delete headers.noIndex;
      const v = await setHeaders(site.name, headers);
      setView(v);
      setNoIndex(v.headers.noIndex === true);
      setDone(noIndex ? "Search engines are asked not to index the site." : "Search engines may index the site.");
    } catch (err) {
      setError(headersMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const unchanged = view === null || noIndex === (view.headers.noIndex === true);

  return (
    <Card title="Search engines">
      <form className="ui-stack" onSubmit={(e) => void save(e)}>
        <label className="ui-check">
          <input type="checkbox" checked={noIndex} disabled={!view || busy} onChange={(e) => setNoIndex(e.target.checked)} />
          Ask search engines not to index the site
        </label>
        <p className="ui-hint ui-flush">
          Every answer gets <span className="ui-mono">X-Robots-Tag: noindex, nofollow</span>, so well-behaved crawlers keep the site out of their results. On
          for staging copies. It does not hide the site: use a password under Security, Access for that.
        </p>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            Save
          </Button>
        </div>
      </form>
    </Card>
  );
}
