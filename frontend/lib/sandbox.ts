import "server-only";
import { INGEST_URL } from "@/lib/bff";

// Scans submitted from the web sandbox use a dedicated API key (principal
// "web-sandbox"), so they are attributable in the SIEM and revocable. The
// fallback is the public dev key, which production deployments revoke.
const DEV_SANDBOX_KEY = "dev-web-sandbox-key-5c2e81f0a9";
const SANDBOX_KEY = process.env.SANDBOX_API_KEY ?? (process.env.NODE_ENV === "production" ? "" : DEV_SANDBOX_KEY);

export interface Scan {
  event_id?: string;
  uld_id: string;
  scanner_id: string;
  scan_type: "RFID" | "BARCODE";
  weight_kg: number;
  location_id: string;
  flight_number?: string;
  timestamp?: string;
}

export async function postScans(scans: Scan[], apiKey = SANDBOX_KEY): Promise<{ status: number; body: unknown }> {
  if (!apiKey) return { status: 503, body: { error: "SANDBOX_API_KEY is not configured" } };
  const res = await fetch(`${INGEST_URL}/v1/scans`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${apiKey}`, "User-Agent": "uldpulse-web-sandbox/2.0" },
    body: JSON.stringify(scans.map((s) => ({ event_id: crypto.randomUUID(), ...s }))),
    cache: "no-store",
    signal: AbortSignal.timeout(10_000),
  });
  return { status: res.status, body: await res.json().catch(() => null) };
}

/** A fresh throwaway ULD id (AKE9nnnnSB); the network clock purges these. */
export const sandboxULD = () => `AKE9${String(Math.floor(Math.random() * 10_000)).padStart(4, "0")}SB`;

export const isoAgo = (seconds: number) => new Date(Date.now() - seconds * 1000).toISOString();
