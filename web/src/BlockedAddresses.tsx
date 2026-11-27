// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, CaretLeft, CaretRight, LockOpen } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
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
const count = new Intl.NumberFormat("en");

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
      setDone(b.address + " is unblocked.");
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
    { key: "address", header: "Address", cell: (b) => <code className="ui-mono">{b.address}</code> },
    {
      key: "jails",
      header: "Blocked by",
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
    { key: "since", header: "Since", cell: (b) => <RelTime iso={b.since} /> },
    { key: "until", header: "Until", cell: (b) => (b.until === null ? <Badge tone="err">No end</Badge> : <RelTime iso={b.until} />) },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (b) => (
        <RowActions>
          <IconButton
            label={"Unblock " + b.address}
            text="Unblock"
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
      title="Blocked addresses"
      padded={false}
      actions={
        <Button icon={<ArrowClockwise size={16} aria-hidden="true" />} disabled={busy || loading} onClick={() => setReload((n) => n + 1)}>
          Refresh
        </Button>
      }
    >
      <div className="ui-card-body ui-stack">
        <p className="ui-flush ui-hint">
          fail2ban blocks an address for a while after too many failed logins. A blocked address reaches neither the sites nor the panel,
          and after repeated blocks not SSH either. Unblock an address here when a customer or you were locked out by mistake; it is
          blocked again after new failed logins.
        </p>
        {unblocking && (
          <Notice tone="warn" title={"Unblock " + unblocking.address + "?"}>
            <p className="ui-flush">
              <code className="ui-confirm-phrase">{unblocking.address}</code> is unblocked in every jail at once:{" "}
              {unblocking.jails.map(jailLabel).join(", ")}.
            </p>
            <div className="ui-row">
              <Button variant="primary" disabled={busy} onClick={() => void doUnban(unblocking)}>
                Unblock address
              </Button>
              <Button disabled={busy} onClick={() => setUnblocking(null)}>
                Cancel
              </Button>
            </div>
          </Notice>
        )}
        {done && <Notice tone="ok" title={done} />}
        {error && <Notice tone="err" title={error} />}
        {st === null && !error && <p aria-busy="true">Loading…</p>}
        {st && <JailList st={st} />}
        {st?.truncated && (
          <Notice tone="warn" title="fail2ban blocks more addresses than the panel reads, so some are missing below.">
            The counters above are complete. Narrow the list with the filters, or use fail2ban-client on the server.
          </Notice>
        )}
      </div>
      {st && (
        <>
          <div className="ui-filters">
            <select
              className="ui-select"
              aria-label="Block type"
              value={filter.jail}
              onChange={(e) => set({ jail: e.target.value, page: "" })}
            >
              <option value="">All block types</option>
              {st.jails.map((j) => (
                <option key={j.name} value={j.name}>
                  {jailLabel(j.name)}
                </option>
              ))}
            </select>
            <input
              className="ui-input ui-mono"
              type="search"
              aria-label="IP address"
              placeholder="IP address or range"
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
                Clear filters
              </Button>
            )}
          </div>
          <div aria-busy={loading}>
            <Table
              label="Blocked addresses"
              columns={columns}
              rows={st.addresses}
              rowKey={(b) => b.address}
              empty={filtered ? "No blocked address matches the filters." : "No address is blocked."}
            />
          </div>
          {st.total > 0 && (
            <div className="ui-pager">
              <span className="ui-hint" aria-live="polite">
                {first === 0
                  ? "No address on this page; " + count.format(st.total) + " in all."
                  : count.format(first) + "–" + count.format(last) + " of " + count.format(st.total) + (st.total === 1 ? " address" : " addresses")}
              </span>
              {pages > 1 && (
                <span className="ui-row">
                  <Button
                    icon={<CaretLeft size={16} aria-hidden="true" />}
                    disabled={loading || st.page <= 1}
                    onClick={() => set({ page: st.page > 2 ? String(Math.min(st.page - 1, pages)) : "" })}
                  >
                    Previous
                  </Button>
                  <span className="ui-hint">
                    Page {count.format(Math.min(st.page, pages))} of {count.format(pages)}
                  </span>
                  <Button disabled={loading || st.page >= pages} onClick={() => set({ page: String(st.page + 1) })}>
                    Next
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
  return (
    <ul className="ui-jails" aria-label="Jails">
      {st.jails.map((j) => (
        <li key={j.name}>
          <span className="ui-cell-main">
            <strong>{jailLabel(j.name)}</strong>
            <span className="ui-mono">{j.name}</span>
          </span>
          {j.running ? (
            <>
              <span className="ui-hint">
                Failed logins: {count.format(j.currentlyFailed)} now, {count.format(j.totalFailed)} since start
              </span>
              <span className="ui-hint">
                Blocked: {count.format(j.currentlyBanned)} now, {count.format(j.totalBanned)} since start
              </span>
            </>
          ) : (
            <Badge tone="err">Not running</Badge>
          )}
        </li>
      ))}
    </ul>
  );
}
