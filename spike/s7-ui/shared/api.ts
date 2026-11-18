// Framework-independent client code, shared by both candidates so that the
// comparison measures UI glue and bundle cost, not different fetch code.

export type SiteKind = "php" | "static" | "node" | "proxy";

export interface Site {
  domain: string;
  user: string;
  kind: SiteKind;
  php?: string;
  diskMb: number;
  quotaMb: number;
}

export interface NewSite {
  domain: string;
  user: string;
  kind: SiteKind;
}

export type FieldErrors = Partial<Record<keyof NewSite, string>>;

// The token comes from the URL fragment (never sent to a server, not in logs).
export function token(): string {
  const m = /(?:^|[#&])token=([0-9a-f]+)/.exec(location.hash);
  return m ? m[1] : "";
}

function headers(): HeadersInit {
  return { Authorization: `Bearer ${token()}`, "Content-Type": "application/json" };
}

export async function listSites(signal?: AbortSignal): Promise<Site[]> {
  const r = await fetch("/api/sites", { headers: headers(), signal });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return r.json();
}

export type CreateResult = { ok: true; site: Site } | { ok: false; errors: FieldErrors };

export async function createSite(s: NewSite): Promise<CreateResult> {
  const r = await fetch("/api/sites", { method: "POST", headers: headers(), body: JSON.stringify(s) });
  if (r.status === 201) return { ok: true, site: await r.json() };
  if (r.status === 422) return { ok: false, errors: (await r.json()).errors };
  throw new Error(`HTTP ${r.status}`);
}

// Same rules as the server. The server stays the authority.
const domainRe = /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;
const userRe = /^[a-z][a-z0-9_-]{2,31}$/;

export function validate(s: NewSite): FieldErrors {
  const e: FieldErrors = {};
  if (!domainRe.test(s.domain)) e.domain = "invalid domain";
  if (!userRe.test(s.user)) e.user = "invalid user name";
  return e;
}

export interface Sample {
  t: number;
  cpu: number;
  mem: number;
}

// SSE over fetch: EventSource cannot send an Authorization header.
export async function streamMetrics(
  onSample: (s: Sample) => void,
  signal: AbortSignal,
  intervalMs = 1000,
): Promise<void> {
  const r = await fetch(`/api/metrics/stream?ms=${intervalMs}`, { headers: headers(), signal });
  if (!r.ok || !r.body) throw new Error(`HTTP ${r.status}`);
  const reader = r.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value;
    let i: number;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      const data = block.split("\n").find((l) => l.startsWith("data: "));
      if (data) onSample(JSON.parse(data.slice(6)));
    }
  }
}
