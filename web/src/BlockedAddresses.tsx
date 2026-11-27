// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, CaretLeft, CaretRight, LockOpen } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatCount } from "./format";
import {
  banPageSize,
  fail2banMessage,
  getBans,
  jailLabel,
  jailLabels,
  unban,
  type BanFilter,
  type BlockedAddress,
  type Fail2banStatus,
} from "./fail2ban";
import { Badge, Button, Card, IconButton, Notice, RelTime, RowActions, Table, type Column } from "./ui";

const typingDelay = 300;

// filterOf reads the filter from the address of the page, so a filtered
// list can be linked and survives a reload.
export function filterOf(query: URLSearchParams): BanFilter {
  const jail = query.get("jail") ?? "";
  const page = Number(query.get("page") ?? "1");
  return {
    jail: jail in jailLabels ? jail : "",
    address: query.get("address") ?? "",
    page: Number.isInteger(page) && page > 1 ? page : 1,
  };
}

type Props = { query: URLSearchParams; onQuery: (q: URLSearchParams) => void };

// BlockedAddressesCard shows what fail2ban blocks (ADR-0078): the Hostashi
// jails with their counters, and every blocked address once, with all jails
// that block it. The server filters by jail and address and returns one
// page, so a server with thousands of blocked addresses stays usable.
// Unblocking lifts the address in every jail at once, so a ban of the
// repeat-offender jail does not stay behind. Thresholds are not set here;
// they stay in fail2ban's files (ADR-0021).
export function BlockedAddressesCard({ query, onQuery }: Props) {
  const { t } = useTranslation();
  const filter = filterOf(query);
  const key = JSON.stringify(filter);
  const [st, setSt] = useState<Fail2banStatus | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [unblocking, setUnblocking] = useState<BlockedAddress | null>(null);
  const [done, setDone] = useState("");
  // typed follows the input at once; the filter follows after a pause.
  const [typed, setTyped] = useState(filter.address);
  const [reload, setReload] = useState(0);

  const set = useCallback(
    (changes: Partial<Record<"jail" | "address" | "page", string>>) => {
      const q = new URLSearchParams(query);
      for (const [k, v] of Object.entries(changes)) {
        if (v) q.set(k, v);
        else q.delete(k);
      }
      onQuery(q);
    },
    [query, onQuery],
  );

  useEffect(() => {
    if (typed.trim() === filter.address.trim()) return;
    const t = window.setTimeout(() => set({ address: typed.trim(), page: "" }), typingDelay);
    return () => window.clearTimeout(t);
  }, [typed, filter.address, set]);

  useEffect(() => {
    let live = true;
    setLoading(true);
    getBans(JSON.parse(key) as BanFilter).then(
      (s) => {
        if (!live) return;
        setSt(s);
        setError("");
        setLoading(false);
      },
      (err: unknown) => {
        if (!live) return;
        setError(fail2banMessage(err));
        setLoading(false);
      },
    );
    return () => {
      live = false;
    };
  }, [key, reload]);

  async function doUnban(b: BlockedAddress) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      await unban(b.address);
      setDone(t("server.blocked.unblocked", { address: b.address }));
    } catch (err) {
      setError(fail2banMessage(err));
    } finally {
      setUnblocking(null);
      setBusy(false);
      setReload((n) => n + 1);
    }
  }

  const filtered = filter.jail !== "" || filter.address.trim() !== "";
  const columns: Column<BlockedAddress>[] = [
    { key: "address", header: t("server.blocked.columns.address"), cell: (b) => <code className="ui-mono">{b.address}</code> },
    {
      key: "jails",
      header: t("server.blocked.columns.jails"),
      cell: (b) => (
        <span className="ui-row">
          {b.jails.map((j) => (
            <Badge key={j} tone="warn">
              {jailLabel(j)}
            </Badge>
          ))}
        </span>
      ),
    },
    { key: "since", header: t("server.blocked.columns.since"), cell: (b) => <RelTime iso={b.since} /> },
    {
      key: "until",
      header: t("server.blocked.columns.until"),
      cell: (b) => (b.until === null ? <Badge tone="err">{t("server.blocked.noEnd")}</Badge> : <RelTime iso={b.until} />),
    },
    {
      key: "actions",
      header: t("server.blocked.columns.actions"),
      srHeader: true,
      cell: (b) => (
        <RowActions>
          <IconButton
            label={t("server.blocked.unblockLabel", { address: b.address })}
            text={t("server.blocked.unblock")}
            icon={<LockOpen size={16} />}
            disabled={busy}
            onClick={() => {
              setDone("");
              setUnblocking(b);
            }}
          />
        </RowActions>
      ),
    },
  ];

  const pages = st ? Math.max(1, Math.ceil(st.total / (st.perPage || banPageSize))) : 1;
  const first = st && st.addresses.length > 0 ? (st.page - 1) * st.perPage + 1 : 0;
  const last = st ? first + st.addresses.length - (first ? 1 : 0) : 0;

  return (
    <Card
      title={t("server.blocked.title")}
      padded={false}
      actions={
        <Button icon={<ArrowClockwise size={16} aria-hidden="true" />} disabled={busy || loading} onClick={() => setReload((n) => n + 1)}>
          {t("server.blocked.refresh")}
        </Button>
      }
    >
      <div className="ui-card-body ui-stack">
        <p className="ui-flush ui-hint">{t("server.blocked.intro")}</p>
        {unblocking && (
          <Notice tone="warn" title={t("server.blocked.unblockTitle", { address: unblocking.address })}>
            <p className="ui-flush">
              <Trans
                i18nKey="server.blocked.unblockText"
                values={{ address: unblocking.address, jails: unblocking.jails.map(jailLabel).join(", ") }}
                components={{ code: <code className="ui-confirm-phrase" /> }}
              />
            </p>
            <div className="ui-row">
              <Button variant="primary" disabled={busy} onClick={() => void doUnban(unblocking)}>
                {t("server.blocked.unblockSubmit")}
              </Button>
              <Button disabled={busy} onClick={() => setUnblocking(null)}>
                {t("common.actions.cancel")}
              </Button>
            </div>
          </Notice>
        )}
        {done && <Notice tone="ok" title={done} />}
        {error && <Notice tone="err" title={error} />}
        {st === null && !error && <p aria-busy="true">{t("common.status.loading")}</p>}
        {st && <JailList st={st} />}
        {st?.truncated && (
          <Notice tone="warn" title={t("server.blocked.truncated")}>
            {t("server.blocked.truncatedText")}
          </Notice>
        )}
      </div>
      {st && (
        <>
          <div className="ui-filters">
            <select
              className="ui-select"
              aria-label={t("server.blocked.blockType")}
              value={filter.jail}
              onChange={(e) => set({ jail: e.target.value, page: "" })}
            >
              <option value="">{t("server.blocked.allTypes")}</option>
              {st.jails.map((j) => (
                <option key={j.name} value={j.name}>
                  {jailLabel(j.name)}
                </option>
              ))}
            </select>
            <input
              className="ui-input ui-mono"
              type="search"
              aria-label={t("server.blocked.ipAddress")}
              placeholder={t("server.blocked.ipPlaceholder")}
              maxLength={64}
              spellCheck={false}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
            />
            {filtered && (
              <Button
                onClick={() => {
                  setTyped("");
                  set({ jail: "", address: "", page: "" });
                }}
              >
                {t("server.blocked.clear")}
              </Button>
            )}
          </div>
          <div aria-busy={loading}>
            <Table
              label={t("server.blocked.title")}
              columns={columns}
              rows={st.addresses}
              rowKey={(b) => b.address}
              empty={filtered ? t("server.blocked.noMatch") : t("server.blocked.none")}
            />
          </div>
          {st.total > 0 && (
            <div className="ui-pager">
              <span className="ui-hint" aria-live="polite">
                {first === 0
                  ? t("server.blocked.emptyPage", { total: formatCount(st.total) })
                  : t("server.blocked.range", { count: st.total, first: formatCount(first), last: formatCount(last), total: formatCount(st.total) })}
              </span>
              {pages > 1 && (
                <span className="ui-row">
                  <Button
                    icon={<CaretLeft size={16} aria-hidden="true" />}
                    disabled={loading || st.page <= 1}
                    onClick={() => set({ page: st.page > 2 ? String(Math.min(st.page - 1, pages)) : "" })}
                  >
                    {t("server.blocked.previous")}
                  </Button>
                  <span className="ui-hint">
                    {t("server.blocked.page", { page: formatCount(Math.min(st.page, pages)), pages: formatCount(pages) })}
                  </span>
                  <Button disabled={loading || st.page >= pages} onClick={() => set({ page: String(st.page + 1) })}>
                    {t("server.blocked.next")}
                    <CaretRight size={16} aria-hidden="true" />
                  </Button>
                </span>
              )}
            </div>
          )}
        </>
      )}
    </Card>
  );
}

function JailList({ st }: { st: Fail2banStatus }) {
  const { t } = useTranslation();
  return (
    <ul className="ui-jails" aria-label={t("server.blocked.jailList")}>
      {st.jails.map((j) => (
        <li key={j.name}>
          <span className="ui-cell-main">
            <strong>{jailLabel(j.name)}</strong>
            <span className="ui-mono">{j.name}</span>
          </span>
          {j.running ? (
            <>
              <span className="ui-hint">{t("server.blocked.failed", { now: formatCount(j.currentlyFailed), total: formatCount(j.totalFailed) })}</span>
              <span className="ui-hint">{t("server.blocked.banned", { now: formatCount(j.currentlyBanned), total: formatCount(j.totalBanned) })}</span>
            </>
          ) : (
            <Badge tone="err">{t("server.blocked.notRunning")}</Badge>
          )}
        </li>
      ))}
    </ul>
  );
}
