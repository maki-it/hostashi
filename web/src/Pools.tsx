// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatBytes } from "./format";
import { serviceMessage } from "./services";
import { listPools, poolNameProblem, poolNote, poolUsed, renamePool, setDefaultPool, type Pool } from "./sites";
import { Badge, Button, Card, Field, Meter, Notice, PageHead } from "./ui";

/**
 * Pools lists the storage pools with their usage (ADR-0016) and lets an
 * admin rename a pool or make it the default (addendum 2). Adding and
 * removing a pool stay on the server, because they need a mounted volume.
 */
export function Pools() {
  const { t } = useTranslation();
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
        title={t("server.pools.title")}
        description={t("server.pools.description")}
      />
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
      {pools === null ? (
        !error && <p aria-busy="true">{t("server.pools.loading")}</p>
      ) : (
        <>
          {pools.map((p) => (
            <PoolCard
              key={p.name}
              pool={p}
              pools={pools}
              busy={busy !== ""}
              onDefault={() => void run(p.name, () => setDefaultPool(p.name), (n) => t("server.pools.defaultDone", { name: n.name }))}
              onRename={(to) => void run(p.name, () => renamePool(p.name, to), (n) => t("server.pools.renameDone", { old: p.name, name: n.name }))}
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
  const { t } = useTranslation();
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
          {p.default && <Badge tone="off">{t("server.pools.default")}</Badge>}
          {p.available ? <Badge tone="ok">{t("server.pools.mounted")}</Badge> : <Badge tone="err">{t("server.pools.notAvailable")}</Badge>}
        </>
      }
    >
      <div className="ui-stack">
        <dl className="ui-facts">
          <dt>{t("server.pools.path")}</dt>
          <dd>
            <code>{p.path}</code>
          </dd>
          <dt>{t("server.pools.sites")}</dt>
          <dd>{t("server.pools.siteCount", { count: p.sites })}</dd>
          <dt>{t("server.pools.diskQuota")}</dt>
          <dd>{p.quota ? t(`server.pools.quota.${p.quota}`) : t("server.pools.quota.unknown")}</dd>
        </dl>
        {used !== null && p.totalBytes ? (
          <Meter
            label={t("server.pools.used")}
            value={used}
            max={p.totalBytes}
            detail={t("server.pools.usedDetail", { used: formatBytes(used), total: formatBytes(p.totalBytes), free: formatBytes(p.freeBytes ?? 0) })}
          />
        ) : (
          <p className="ui-hint ui-flush">{t("server.pools.noUsage")}</p>
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
                label={t("server.pools.newName")}
                hint={t("server.pools.newNameHint")}
                {...(name !== p.name && problem ? { error: problem } : {})}
              >
                <input className="ui-input" value={name} onChange={(e) => setName(e.target.value.trim())} autoComplete="off" spellCheck={false} autoFocus />
              </Field>
            </div>
            <div className="ui-row">
              <Button variant="primary" type="submit" disabled={busy || name === p.name || problem !== ""}>
                {t("server.pools.renameSubmit")}
              </Button>
              <Button
                onClick={() => {
                  setRenaming(false);
                  setName(p.name);
                }}
              >
                {t("common.actions.cancel")}
              </Button>
            </div>
          </form>
        ) : (
          <div className="ui-row">
            {!p.default && (
              <Button onClick={onDefault} disabled={busy || !p.available}>
                {t("server.pools.makeDefault")}
              </Button>
            )}
            <Button onClick={() => setRenaming(true)} disabled={busy}>
              {t("server.pools.rename")}
            </Button>
          </div>
        )}
      </div>
    </Card>
  );
}

// The commands stay the same in every language.
const poolCommands = "hostashi pool add [-default] NAME PATH\nhostashi pool remove NAME";

function AddHint() {
  const { t } = useTranslation();
  return (
    <Card title={t("server.pools.add.title")}>
      <div className="ui-stack">
        <p className="ui-flush">
          <Trans i18nKey="server.pools.add.text" components={{ code1: <code />, code2: <code /> }} />
        </p>
        <pre className="ui-code">{poolCommands}</pre>
        <p className="ui-hint ui-flush">{t("server.pools.add.remove")}</p>
      </div>
    </Card>
  );
}
