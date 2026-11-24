// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from "react";
import { bytes, serviceMessage } from "./services";
import { listPools, poolNameProblem, poolNote, poolUsed, renamePool, setDefaultPool, type Pool } from "./sites";
import { Badge, Button, Card, Field, Meter, Notice, PageHead } from "./ui";

const quotaText: Record<NonNullable<Pool["quota"]>, string> = {
  enforced: "Disk limits enforced",
  counting: "Usage counted, limits not enforced",
  off: "No disk quota",
};

/**
 * Pools lists the storage pools with their usage (ADR-0016) and lets an
 * admin rename a pool or make it the default (addendum 2). Adding and
 * removing a pool stay on the server, because they need a mounted volume.
 */
export function Pools() {
  const [pools, setPools] = useState<Pool[] | null>(null);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [busy, setBusy] = useState("");

  const load = useCallback(async () => {
    try {
      setPools(await listPools());
      setError("");
    } catch (err) {
      setError(serviceMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function run(name: string, f: () => Promise<Pool>, text: (p: Pool) => string) {
    setBusy(name);
    setDone("");
    setError("");
    try {
      const p = await f();
      setDone(text(p));
      await load();
    } catch (err) {
      setError(serviceMessage(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <>
      <PageHead
        title="Storage pools"
        description="Where the home directories of the sites live. A site stays on the pool it was created on; new sites land on the default pool unless you pick another."
      />
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
      {pools === null ? (
        !error && <p aria-busy="true">Loading the storage pools…</p>
      ) : (
        <>
          {pools.map((p) => (
            <PoolCard
              key={p.name}
              pool={p}
              pools={pools}
              busy={busy !== ""}
              onDefault={() => void run(p.name, () => setDefaultPool(p.name), (n) => `New sites now land on ${n.name}.`)}
              onRename={(to) => void run(p.name, () => renamePool(p.name, to), (n) => `The pool ${p.name} is now called ${n.name}.`)}
            />
          ))}
          <AddHint />
        </>
      )}
    </>
  );
}

function PoolCard({
  pool: p,
  pools,
  busy,
  onDefault,
  onRename,
}: {
  pool: Pool;
  pools: Pool[];
  busy: boolean;
  onDefault: () => void;
  onRename: (to: string) => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState(p.name);
  const used = poolUsed(p);
  const problem = poolNameProblem(name, pools, p.name);
  const note = poolNote(p);

  return (
    <Card
      title={p.name}
      actions={
        <>
          {p.default && <Badge tone="off">Default</Badge>}
          {p.available ? <Badge tone="ok">Mounted</Badge> : <Badge tone="err">Not available</Badge>}
        </>
      }
    >
      <div className="ui-stack">
        <dl className="ui-facts">
          <dt>Path</dt>
          <dd>
            <code>{p.path}</code>
          </dd>
          <dt>Sites</dt>
          <dd>{p.sites === 1 ? "1 site" : `${p.sites} sites`}</dd>
          <dt>Disk quota</dt>
          <dd>{p.quota ? quotaText[p.quota] : "Unknown while the volume is missing"}</dd>
        </dl>
        {used !== null && p.totalBytes ? (
          <Meter label="Used" value={used} max={p.totalBytes} detail={`${bytes(used)} of ${bytes(p.totalBytes)}, ${bytes(p.freeBytes ?? 0)} free`} />
        ) : (
          <p className="ui-hint ui-flush">No usage while the volume is not mounted.</p>
        )}
        {note && <Notice tone="warn" title={note} />}
        {renaming ? (
          <form
            className="ui-stack"
            onSubmit={(e) => {
              e.preventDefault();
              onRename(name);
              setRenaming(false);
            }}
          >
            <div className="ui-narrow">
              <Field
                label="New name"
                hint="The path, the sites and their files stay where they are. Scripts and API calls that use the old name need the new one."
                {...(name !== p.name && problem ? { error: problem } : {})}
              >
                <input className="ui-input" value={name} onChange={(e) => setName(e.target.value.trim())} autoComplete="off" spellCheck={false} autoFocus />
              </Field>
            </div>
            <div className="ui-row">
              <Button variant="primary" type="submit" disabled={busy || name === p.name || problem !== ""}>
                Rename pool
              </Button>
              <Button
                onClick={() => {
                  setRenaming(false);
                  setName(p.name);
                }}
              >
                Cancel
              </Button>
            </div>
          </form>
        ) : (
          <div className="ui-row">
            {!p.default && (
              <Button onClick={onDefault} disabled={busy || !p.available}>
                Make default
              </Button>
            )}
            <Button onClick={() => setRenaming(true)} disabled={busy}>
              Rename
            </Button>
          </div>
        )}
      </div>
    </Card>
  );
}

function AddHint() {
  return (
    <Card title="Add or remove a pool">
      <div className="ui-stack">
        <p className="ui-flush">
          A new pool is a mounted, empty ext4 or XFS volume with <code>nosuid,nodev</code> and an entry in <code>/etc/fstab</code>. Hostashi never
          formats or mounts disks, so this happens on the server as root:
        </p>
        <pre className="ui-code">{"hostashi pool add [-default] NAME PATH\nhostashi pool remove NAME"}</pre>
        <p className="ui-hint ui-flush">A pool can be removed when no site is on it and it is not the default.</p>
      </div>
    </Card>
  );
}
