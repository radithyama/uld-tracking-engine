import { audit, json, rateLimited, requireCapability, SIM_URL } from "@/lib/bff";

// Demo traffic generator (extra warehouse activity from the simulator). One
// shared switch for every viewer; it turns itself off after a time limit.

async function sim(init?: RequestInit) {
  const res = await fetch(`${SIM_URL}/generator`, { ...init, cache: "no-store", signal: AbortSignal.timeout(5000) });
  if (!res.ok) throw new Error(`simulator ${res.status}`);
  return res.json();
}

export async function GET() {
  const c = await requireCapability("sandbox.generator");
  if (c instanceof Response) return c;
  try {
    return json(await sim());
  } catch {
    return json({ error: "simulator not running" }, 502);
  }
}

// { enabled: true | false }
export async function POST(req: Request) {
  const c = await requireCapability("sandbox.generator");
  if (c instanceof Response) return c;
  if (rateLimited(c)) return json({ error: "rate limit: 20 sandbox actions per minute" }, 429);
  const { enabled } = (await req.json().catch(() => ({}))) as { enabled?: unknown };
  if (typeof enabled !== "boolean") return json({ error: "body must be { enabled: boolean }" }, 400);
  try {
    const state = await sim({ method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ enabled }) });
    await audit(c, enabled ? "sandbox.generator_on" : "sandbox.generator_off", "simulator", {});
    return json(state);
  } catch {
    return json({ error: "simulator not running" }, 502);
  }
}
