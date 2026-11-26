// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise, LockOpen } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { blockedAddresses, fail2banMessage, getBans, jailLabel, unban, type BlockedAddress, type Fail2banStatus } from "./fail2ban";
import { Badge, Button, Card, IconButton, Notice, RelTime, RowActions, Table, type Column } from "./ui";

// BlockedAddressesCard shows what fail2ban blocks (ADR-0078): the Hostashi
// jails with their counters, and every blocked address once, with all jails
// that block it. Unblocking lifts the address in every jail at once, so a
// ban of the repeat-offender jail does not stay behind. Thresholds are not
// set here; they stay in fail2ban's files (ADR-0021).
export function BlockedAddressesCard() {
  const [st, setSt] = useState<Fail2banStatus | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [unblocking, setUnblocking] = useState<BlockedAddress | null>(null);
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    setError("");
    try {
      setSt(await getBans());
    } catch (err) {
      setSt(null);
      setError(fail2banMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

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
      void load();
    }
  }

  const rows = st ? blockedAddresses(st) : [];
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

  return (
    <Card
      title="Blocked addresses"
      padded={false}
      actions={
        <Button icon={<ArrowClockwise size={16} aria-hidden="true" />} disabled={busy} onClick={() => void load()}>
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
      </div>
      {st &&
        (rows.length === 0 ? (
          <div className="ui-card-body">
            <p className="ui-hint ui-flush">No address is blocked.</p>
          </div>
        ) : (
          <Table label="Blocked addresses" columns={columns} rows={rows} rowKey={(b) => b.address} />
        ))}
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
                Failed logins: {j.currentlyFailed} now, {j.totalFailed} since start
              </span>
              <span className="ui-hint">
                Blocked: {j.currentlyBanned} now, {j.totalBanned} since start
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
