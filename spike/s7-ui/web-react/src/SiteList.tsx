import { useEffect, useMemo, useState } from "react";
import { listSites, type Site } from "../../shared/api";
import { SiteForm } from "./SiteForm";

type SortKey = "domain" | "kind" | "diskMb";

export function SiteList() {
  const [sites, setSites] = useState<Site[]>([]);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState<SortKey>("domain");

  useEffect(() => {
    const ac = new AbortController();
    listSites(ac.signal).then(setSites, (e) => !ac.signal.aborted && setError(String(e)));
    return () => ac.abort();
  }, []);

  const rows = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return sites
      .filter((s) => !f || s.domain.includes(f) || s.user.includes(f))
      .sort((a, b) => (sort === "diskMb" ? b.diskMb - a.diskMb : a[sort].localeCompare(b[sort])));
  }, [sites, filter, sort]);

  return (
    <>
      <SiteForm onCreated={(s) => setSites((cur) => [...cur, s])} />
      <div className="card">
        <input placeholder="Filter" aria-label="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
        {error && <p className="err">{error}</p>}
        <table>
          <thead>
            <tr>
              <th><button onClick={() => setSort("domain")}>Domain</button></th>
              <th>User</th>
              <th><button onClick={() => setSort("kind")}>Type</button></th>
              <th><button onClick={() => setSort("diskMb")}>Disk</button></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((s) => {
              const pct = Math.min(100, Math.round((s.diskMb / s.quotaMb) * 100));
              return (
                <tr key={s.domain}>
                  <td>{s.domain}</td>
                  <td>{s.user}</td>
                  <td>{s.kind}{s.php ? ` ${s.php}` : ""}</td>
                  <td>
                    <div className={pct > 80 ? "bar warn" : "bar"} title={`${s.diskMb} / ${s.quotaMb} MB`}>
                      <i style={{ width: `${pct}%` }} />
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}
