// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { getHealth, type Health } from "./api";

type State = { kind: "loading" } | { kind: "ok"; health: Health } | { kind: "error"; message: string };

export function App() {
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    let active = true;
    getHealth()
      .then((health) => active && setState({ kind: "ok", health }))
      .catch((err: unknown) => active && setState({ kind: "error", message: err instanceof Error ? err.message : String(err) }));
    return () => {
      active = false;
    };
  }, []);

  return (
    <main className="shell">
      <h1>Hostashi</h1>
      <ServerStatus state={state} />
    </main>
  );
}

function ServerStatus({ state }: { state: State }) {
  switch (state.kind) {
    case "loading":
      return <p aria-busy="true">Checking server…</p>;
    case "error":
      return <p role="alert">Server not reachable: {state.message}</p>;
    case "ok": {
      const h = state.health;
      return (
        <dl className="status" aria-label="Server status">
          <dt>Status</dt>
          <dd>{h.status}</dd>
          <dt>Agent</dt>
          <dd>{h.agent}</dd>
          <dt>Database</dt>
          <dd>{h.db}</dd>
          <dt>Version</dt>
          <dd>{h.version || "dev"}</dd>
        </dl>
      );
    }
  }
}
