import { useState } from "react";
import { SiteList } from "./SiteList";
import { Metrics } from "./Metrics";
import { Terminal } from "./Terminal";

const tabs = ["Sites", "Metrics", "Terminal"] as const;

export function App() {
  const [tab, setTab] = useState<(typeof tabs)[number]>("Sites");
  return (
    <>
      <header>
        <h1>Hostashi (React)</h1>
        <div role="tablist" className="tabs">
          {tabs.map((t) => (
            <button key={t} role="tab" aria-selected={tab === t} onClick={() => setTab(t)}>
              {t}
            </button>
          ))}
        </div>
      </header>
      <main>
        {tab === "Sites" && <SiteList />}
        {tab === "Metrics" && <Metrics />}
        {tab === "Terminal" && <Terminal />}
      </main>
    </>
  );
}
