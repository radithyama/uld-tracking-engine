import { NextResponse, type NextRequest } from "next/server";
import { auth } from "@/auth";
import { matchBff } from "@/lib/accessPolicy";
import { can, isRole } from "@/lib/roles";

// Edge security layer, run before every page and route handler:
//   1. a per-request nonce Content-Security-Policy for pages;
//   2. deny-by-default role checks for /bff (lib/accessPolicy.ts);
//   3. an Origin check on state-changing requests (CSRF defence in depth on
//      top of Auth.js's SameSite=Lax session cookie);
//   4. rate limits on sign-in attempts;
//   5. a server-side redirect to /signin for signed-out page views.
// Route handlers and the Go services repeat their own authorization checks;
// this layer narrows what reaches them.

const isDev = process.env.NODE_ENV === "development";

// The realtime WebSocket may live on another origin (local dev: :8081).
const wsOrigin = (() => {
  try {
    return new URL(process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8081/ws").origin;
  } catch {
    return "";
  }
})();

function contentSecurityPolicy(nonce: string): string {
  return [
    "default-src 'self'",
    // 'strict-dynamic': only scripts carrying this response's nonce (and what
    // they load) run, so injected markup can't execute script.
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${isDev ? " 'unsafe-eval'" : ""}`,
    // React renders style="" attributes, which nonces can't cover. Style
    // injection is far less dangerous than script, and scripts stay locked.
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self'",
    `connect-src 'self' ${wsOrigin}`.trim(),
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self' https://accounts.google.com",
    "frame-ancestors 'none'",
    ...(isDev ? [] : ["upgrade-insecure-requests"]),
  ].join("; ");
}

// --- sign-in rate limit (per instance; Nginx limits globally) -------------
const SIGNIN_PER_MIN = 10;
const attempts = new Map<string, { n: number; reset: number }>();

function signinLimited(ip: string): boolean {
  const now = Date.now();
  if (attempts.size > 10_000) for (const [k, v] of attempts) if (v.reset < now) attempts.delete(k);
  const a = attempts.get(ip);
  if (!a || a.reset < now) {
    attempts.set(ip, { n: 1, reset: now + 60_000 });
    return false;
  }
  return ++a.n > SIGNIN_PER_MIN;
}

// Next.js listens on loopback behind Nginx, which overwrites X-Real-IP with
// the real peer address, so the header can be trusted here.
const clientIP = (req: NextRequest) => req.headers.get("x-real-ip") ?? "local";

const deny = (status: number, error: string, headers?: HeadersInit) =>
  NextResponse.json({ error }, { status, headers: { "Cache-Control": "no-store", ...headers } });

const UNSAFE = new Set(["POST", "PUT", "PATCH", "DELETE"]);

/** Same-origin check for state-changing requests. */
function crossSite(req: NextRequest): boolean {
  if (!UNSAFE.has(req.method)) return false;
  const site = req.headers.get("sec-fetch-site");
  if (site) return site !== "same-origin" && site !== "none";
  const origin = req.headers.get("origin");
  return origin !== null && origin !== req.nextUrl.origin;
}

export default auth(async (req) => {
  const { pathname } = req.nextUrl;
  const role = isRole(req.auth?.user?.role) ? req.auth.user.role : undefined;

  if (pathname.startsWith("/auth/")) {
    if (req.method === "POST" && (pathname.startsWith("/auth/callback/") || pathname.startsWith("/auth/signin/"))) {
      if (signinLimited(clientIP(req))) return deny(429, "too many sign-in attempts", { "Retry-After": "60" });
    }
    return NextResponse.next();
  }

  if (pathname.startsWith("/bff/")) {
    if (crossSite(req)) return deny(403, "cross-site request refused");
    const d = matchBff(pathname, req.method);
    if ("status" in d) return deny(d.status, d.status === 404 ? "not found" : "method not allowed");
    if (!role) return deny(401, "sign in required");
    if (d.rule.need !== "session" && !can(role, d.rule.need)) return deny(403, "forbidden");
    return NextResponse.next();
  }

  // Pages.
  if (!role && pathname !== "/signin") {
    return NextResponse.redirect(new URL("/signin", req.nextUrl));
  }
  const nonce = Buffer.from(crypto.randomUUID()).toString("base64");
  const csp = contentSecurityPolicy(nonce);
  const requestHeaders = new Headers(req.headers);
  requestHeaders.set("x-nonce", nonce);
  requestHeaders.set("Content-Security-Policy", csp); // Next.js reads the nonce from here
  const res = NextResponse.next({ request: { headers: requestHeaders } });
  res.headers.set("Content-Security-Policy", csp);
  return res;
});

export const config = {
  matcher: [
    {
      // Everything except static assets, which need neither CSP nor auth.
      source: "/((?!_next/static|_next/image|favicon.ico|icon.png|apple-icon.png|brand/).*)",
      missing: [
        { type: "header", key: "next-router-prefetch" },
        { type: "header", key: "purpose", value: "prefetch" },
      ],
    },
  ],
};
