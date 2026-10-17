import type { NextConfig } from "next";

// Response headers for every route. The Content-Security-Policy is set per
// request in proxy.ts, because it carries a fresh script nonce.
const securityHeaders = [
  // Two years, all subdomains, eligible for browser preload lists. Browsers
  // ignore HSTS on plain-HTTP responses, so this is harmless in development.
  { key: "Strict-Transport-Security", value: "max-age=63072000; includeSubDomains; preload" },
  { key: "X-Frame-Options", value: "DENY" }, // legacy twin of CSP frame-ancestors
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()" },
  { key: "Cross-Origin-Opener-Policy", value: "same-origin" },
  { key: "Cross-Origin-Resource-Policy", value: "same-origin" },
  { key: "X-DNS-Prefetch-Control", value: "off" },
];

const nextConfig: NextConfig = {
  // Don't advertise the framework (X-Powered-By: Next.js).
  poweredByHeader: false,
  // Pages render per request so each response can carry a CSP nonce (see
  // proxy.ts); Cache Components / partial prerendering can't serve a nonce.
  async headers() {
    return [{ source: "/:path*", headers: securityHeaders }];
  },
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
};

export default nextConfig;
