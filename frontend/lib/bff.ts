// Backend-for-frontend helpers, used only by route handlers under app/bff.
// The browser never talks to privileged Go endpoints directly: route handlers
// check the session's role, then call the Go services on the private network
// with a short-lived HS256 token the Go side verifies independently.
import "server-only";
import { SignJWT } from "jose";
import { auth } from "@/auth";
import { can, type Capability, type Role } from "@/lib/roles";

export const REALTIME_URL = process.env.REALTIME_INTERNAL_URL ?? "http://127.0.0.1:8081";
export const INGEST_URL = process.env.INGEST_INTERNAL_URL ?? "http://127.0.0.1:8080";
export const SIM_URL = process.env.SIM_CONTROL_URL ?? "http://127.0.0.1:8090";

export interface Caller {
  sub: string;
  name: string;
  role: Role;
}

export const json = (body: unknown, status = 200) =>
  Response.json(body, { status, headers: { "Cache-Control": "no-store" } });

/** Resolves the signed-in caller (any role), or a 401 Response. */
export async function requireSession(): Promise<Caller | Response> {
  const session = await auth();
  const role = session?.user?.role;
  if (!session?.user || !role) return json({ error: "sign in required" }, 401);
  return { sub: session.user.email ?? session.user.name ?? "unknown", name: session.user.name ?? "unknown", role };
}

/** Resolves the signed-in caller if their role holds cap, or a 401/403 Response. */
export async function requireCapability(cap: Capability): Promise<Caller | Response> {
  const c = await requireSession();
  if (c instanceof Response) return c;
  return can(c.role, cap) ? c : json({ error: `${c.role} may not ${cap}` }, 403);
}

// Must match backend/internal/authz: issuer, audiences and lifetime caps.
const ISSUER = "uldpulse-bff";
const API_AUDIENCE = "uld-realtime";
const TICKET_AUDIENCE = "uld-ws";

function sign(c: Caller, audience: string, ttlSeconds: number): Promise<string> {
  const secret = process.env.INTERNAL_API_SECRET;
  if (!secret) throw new Error("INTERNAL_API_SECRET is not configured");
  return new SignJWT({ name: c.name, role: c.role })
    .setProtectedHeader({ alg: "HS256", typ: "JWT" })
    .setIssuer(ISSUER)
    .setSubject(c.sub)
    .setAudience(audience)
    .setJti(crypto.randomUUID())
    .setIssuedAt()
    .setExpirationTime(`${ttlSeconds}s`)
    .sign(new TextEncoder().encode(secret));
}

/** API token for server-to-server calls (Authorization: Bearer). */
export const mintToken = (c: Caller, ttlSeconds = 120) => sign(c, API_AUDIENCE, ttlSeconds);

/**
 * Single-use WebSocket ticket. Browsers can't set headers on a WebSocket
 * upgrade, so it travels in the URL; a distinct audience, a 60-second
 * lifetime and one-time use (enforced in Go) make a leaked ticket useless.
 */
export const mintTicket = (c: Caller) => sign(c, TICKET_AUDIENCE, 60);

/** Calls the Go realtime API as the caller and relays its JSON response. */
export async function realtime(c: Caller, path: string, init: RequestInit = {}): Promise<Response> {
  try {
    const res = await fetch(`${REALTIME_URL}/api/v1${path}`, {
      ...init,
      cache: "no-store",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${await mintToken(c)}`, ...init.headers },
      signal: AbortSignal.timeout(10_000),
    });
    return json(await res.json().catch(() => ({ error: `upstream ${res.status}` })), res.status);
  } catch (err) {
    return json({ error: `realtime service unavailable: ${(err as Error).message}` }, 502);
  }
}

/** Fire-and-forget audit entry for actions the BFF performs itself. */
export async function audit(c: Caller, action: string, target: string, details: Record<string, unknown>) {
  await realtime(c, "/audit", { method: "POST", body: JSON.stringify({ action, target, details }) }).catch(() => {});
}

// Per-user rate limit for sandbox actions (in-memory, per Next.js instance).
const buckets = new Map<string, { tokens: number; at: number }>();
export function rateLimited(c: Caller, perMinute = 20): boolean {
  const now = Date.now();
  const b = buckets.get(c.sub) ?? { tokens: perMinute, at: now };
  b.tokens = Math.min(perMinute, b.tokens + ((now - b.at) / 60_000) * perMinute);
  b.at = now;
  if (b.tokens < 1) {
    buckets.set(c.sub, b);
    return true;
  }
  b.tokens -= 1;
  buckets.set(c.sub, b);
  return false;
}
