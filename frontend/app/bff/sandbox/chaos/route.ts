import { audit, json, rateLimited, requireCapability } from "@/lib/bff";
import { isoAgo, postScans, sandboxULD } from "@/lib/sandbox";

type Kind = "tamper" | "impossible_travel" | "unauthorized";

// Fires real stimuli through the real pipeline; the processor's detections
// (not this code) raise the alerts.
export async function POST(req: Request) {
  const c = await requireCapability("sandbox.chaos");
  if (c instanceof Response) return c;
  if (rateLimited(c)) return json({ error: "rate limit: 20 sandbox actions per minute" }, 429);
  const { kind } = (await req.json().catch(() => ({}))) as { kind?: Kind };
  const scanner = `WEB-${c.role.toUpperCase()}`;
  const uld = sandboxULD();

  let res: { status: number; body: unknown };
  let expected: string;
  let detail: string;
  switch (kind) {
    case "tamper": {
      // Load a ULD at 1,000 kg, then report it 30% heavier while still loaded.
      res = await postScans([
        { uld_id: uld, scanner_id: scanner, scan_type: "RFID", weight_kg: 1000, location_id: "RMP-03", timestamp: isoAgo(3) },
        { uld_id: uld, scanner_id: scanner, scan_type: "RFID", weight_kg: 1300, location_id: "RMP-03", timestamp: isoAgo(0) },
      ]);
      expected = "ULD-INT-003";
      detail = `${uld} loaded at 1,000 kg, then re-scanned at 1,300 kg (+30%) on the ramp`;
      break;
    }
    case "impossible_travel": {
      // Scanned in Jakarta, then in Sydney (~5,500 km) five seconds later.
      res = await postScans([
        { uld_id: uld, scanner_id: scanner, scan_type: "RFID", weight_kg: 640, location_id: "STA-01", timestamp: isoAgo(5) },
        { uld_id: uld, scanner_id: scanner, scan_type: "RFID", weight_kg: 640, location_id: "SYD-RCV-01", timestamp: isoAgo(0) },
      ]);
      expected = "ULD-GEO-001";
      detail = `${uld} scanned at CGK STA-01, then SYD-RCV-01 five seconds later`;
      break;
    }
    case "unauthorized": {
      // A scan signed with a key that was never issued.
      res = await postScans(
        [{ uld_id: uld, scanner_id: "ROGUE-01", scan_type: "BARCODE", weight_kg: 500, location_id: "RCV-01" }],
        `forged-${crypto.randomUUID().replaceAll("-", "")}`,
      );
      expected = "ULD-AUTH-003";
      detail = "scan submitted with a forged API key (rejected with 401)";
      break;
    }
    default:
      return json({ error: "kind must be tamper | impossible_travel | unauthorized" }, 400);
  }
  await audit(c, `sandbox.${kind}`, uld, { ingest_status: res.status, expected_rule: expected });
  return json({ kind, uld_id: uld, ingest_status: res.status, expected_rule: expected, detail });
}
