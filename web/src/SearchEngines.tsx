// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { getHeaders, headersMessage, setHeaders, type HeadersView } from "./headers";
import type { Site } from "./sites";
import { Button, Card, Notice } from "./ui";

// SearchEnginesCard switches X-Robots-Tag: noindex, nofollow for the whole
// site. It is stored with the security headers (ADR-0056, ADR-0070) and
// on for staging copies.
export function SearchEnginesCard({ site }: { site: Site }) {
  const { t } = useTranslation();
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
      setDone(noIndex ? t("siteHeaders.search.doneOn") : t("siteHeaders.search.doneOff"));
    } catch (err) {
      setError(headersMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const unchanged = view === null || noIndex === (view.headers.noIndex === true);

  return (
    <Card title={t("siteHeaders.search.title")}>
      <form className="ui-stack" onSubmit={(e) => void save(e)}>
        <label className="ui-check">
          <input type="checkbox" checked={noIndex} disabled={!view || busy} onChange={(e) => setNoIndex(e.target.checked)} />
          {t("siteHeaders.search.label")}
        </label>
        <p className="ui-hint ui-flush">
          <Trans i18nKey="siteHeaders.search.hint" components={{ mono: <span className="ui-mono" /> }} />
        </p>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            {t("common.actions.save")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
