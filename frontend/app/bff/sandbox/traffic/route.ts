import { audit, json, rateLimited, requireCapability, SIM_URL } from "@/lib/bff";

async function sim(path: string, init?: RequestInit) {
  const res = await fetch(`${SIM_URL}${path}`, { ...init, cache: "no-store", signal: AbortSignal.timeout(5000) });
  return res.json().catch(() => ({}));
}

export async function GET() {
  const c = await requireCapability("sandbox.traffic");
  if (c instanceof Response) return c;
  try {
    return json(await sim("/stats"));
  } catch {
    return json({ error: "simulator not running" }, 502);
  }
}

// { action: "pause" | "resume" } or { action: "rate", rate: 1-200 }
export async function POST(req: Request) {
  const c = await requireCapability("sandbox.traffic");
  if (c instanceof Response) return c;
  if (rateLimited(c)) return json({ error: "rate limit: 20 sandbox actions per minute" }, 429);
  const { action, rate } = (await req.json().catch(() => ({}))) as { action?: string; rate?: number };
  try {
    if (action === "pause" || action === "resume") await sim(`/${action}`, { method: "POST" });
    else if (action === "rate" && Number.isInteger(rate) && rate! >= 1 && rate! <= 200)
      await sim("/rate", { method: "POST", body: JSON.stringify({ rate }) });
    else return json({ error: "action must be pause | resume | rate (1-200)" }, 400);
    await audit(c, `sandbox.traffic_${action}`, "simulator", { rate });
    return json(await sim("/stats"));
  } catch {
    return json({ error: "simulator not running" }, 502);
  }
}
