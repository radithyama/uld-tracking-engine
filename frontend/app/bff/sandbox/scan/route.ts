import { audit, json, rateLimited, requireCapability } from "@/lib/bff";
import { postScans } from "@/lib/sandbox";

// Manual scanner tool: submit one scan as the signed-in user.
export async function POST(req: Request) {
  const c = await requireCapability("sandbox.scan");
  if (c instanceof Response) return c;
  if (rateLimited(c)) return json({ error: "rate limit: 20 sandbox actions per minute" }, 429);
  const b = (await req.json().catch(() => null)) as Record<string, unknown> | null;
  const weight = Number(b?.weight_kg);
  if (typeof b?.uld_id !== "string" || typeof b.location_id !== "string" || !Number.isFinite(weight)) {
    return json({ error: "uld_id, location_id and numeric weight_kg are required" }, 400);
  }
  const scan = {
    uld_id: b.uld_id.trim().toUpperCase(),
    location_id: b.location_id.trim().toUpperCase(),
    weight_kg: Math.round(weight * 10) / 10,
    flight_number: typeof b.flight_number === "string" && b.flight_number ? b.flight_number.toUpperCase() : undefined,
    scanner_id: `WEB-${c.role.toUpperCase()}`,
    scan_type: "BARCODE" as const,
  };
  const res = await postScans([scan]);
  await audit(c, "sandbox.scan", scan.uld_id, { ...scan, ingest_status: res.status });
  return json({ ingest_status: res.status, ingest: res.body, scan }, res.status === 202 ? 200 : res.status);
}
