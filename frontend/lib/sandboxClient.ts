// Browser-side calls to the sandbox BFF routes, shared by the bottom toolbar
// and the guide's Quick Start checklist.

export interface ActionResult {
  ok: boolean;
  title: string;
  detail: string;
}

async function post(url: string, body: unknown): Promise<{ ok: boolean; status: number; body: Record<string, unknown> }> {
  try {
    const res = await fetch(url, { method: "POST", body: JSON.stringify(body) });
    return { ok: res.ok, status: res.status, body: await res.json().catch(() => ({})) };
  } catch {
    return { ok: false, status: 0, body: { error: "network error" } };
  }
}

function errorOf(b: Record<string, unknown>, status: number): string {
  const ingest = b.ingest as { details?: unknown } | undefined;
  if (ingest?.details) return JSON.stringify(ingest.details);
  return String(b.error ?? `HTTP ${status}`);
}

export async function sandboxScan(scan: {
  uld_id: string;
  location_id: string;
  weight_kg: number;
  flight_number?: string;
}): Promise<ActionResult> {
  const r = await post("/bff/sandbox/scan", scan);
  if (!r.ok) return { ok: false, title: "Scan rejected", detail: errorOf(r.body, r.status) };
  const durability = (r.body.ingest as { durability?: string } | undefined)?.durability ?? "kafka";
  return { ok: true, title: `Scan accepted · durable in ${durability}`, detail: `${scan.uld_id} @ ${scan.location_id} · ${scan.weight_kg} kg` };
}

export type ChaosKind = "tamper" | "impossible_travel" | "unauthorized";

export async function sandboxChaos(kind: ChaosKind): Promise<ActionResult> {
  const r = await post("/bff/sandbox/chaos", { kind });
  if (!r.ok) return { ok: false, title: "Drill failed", detail: errorOf(r.body, r.status) };
  return {
    ok: true,
    title: `${kind.replace("_", " ")} → expect ${r.body.expected_rule}`,
    detail: String(r.body.detail ?? ""),
  };
}

export interface TrafficState {
  rate_per_sec?: number;
  paused?: boolean;
}

export async function sandboxTraffic(body: { action: "pause" | "resume" } | { action: "rate"; rate: number }) {
  const r = await post("/bff/sandbox/traffic", body);
  return { ok: r.ok, state: r.body as TrafficState, error: r.ok ? "" : errorOf(r.body, r.status) };
}

export interface GeneratorState {
  enabled: boolean;
  rate_per_sec?: number;
  remaining_seconds?: number;
  max_run_seconds?: number;
}

export async function getGenerator(): Promise<GeneratorState | null> {
  try {
    const res = await fetch("/bff/sandbox/generator", { cache: "no-store" });
    return res.ok ? ((await res.json()) as GeneratorState) : null;
  } catch {
    return null;
  }
}

export async function setGenerator(enabled: boolean) {
  const r = await post("/bff/sandbox/generator", { enabled });
  return { ok: r.ok, state: r.body as unknown as GeneratorState, error: r.ok ? "" : errorOf(r.body, r.status) };
}
