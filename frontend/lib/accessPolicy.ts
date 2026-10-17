import type { Capability } from "@/lib/roles";

// Edge access policy for the backend-for-frontend, enforced in proxy.ts
// before any route handler runs. It is deny-by-default: a /bff route that
// isn't listed here is unreachable, so a new handler that forgets its own
// check can't become an open door. Route handlers still check their
// capability (requireCapability), and the Go services verify a signed role
// token again, so no single layer is the only gate.
interface Rule {
  path: RegExp;
  methods: readonly string[];
  need: Capability | "session"; // "session": any signed-in role
}

const ID = "[0-9]{1,18}";

export const BFF_POLICY: readonly Rule[] = [
  // The route itself requires security.view for ?stream=security.
  { path: /^\/bff\/ws\/ticket$/, methods: ["GET"], need: "session" },
  { path: new RegExp(`^/bff/flights/${ID}/loadplan$`), methods: ["GET"], need: "loadplan.view" },
  { path: new RegExp(`^/bff/flights/${ID}/loadplan/move$`), methods: ["POST"], need: "loadplan.edit" },
  { path: new RegExp(`^/bff/flights/${ID}/optimize$`), methods: ["POST"], need: "loadplan.edit" },
  { path: /^\/bff\/security\/alerts$/, methods: ["GET"], need: "security.view" },
  { path: /^\/bff\/security\/alerts\/[0-9a-f-]{36}\/(ack|resolve)$/, methods: ["POST"], need: "security.view" },
  { path: /^\/bff\/audit$/, methods: ["GET"], need: "security.view" },
  { path: /^\/bff\/sandbox\/scan$/, methods: ["POST"], need: "sandbox.scan" },
  { path: /^\/bff\/sandbox\/chaos$/, methods: ["POST"], need: "sandbox.chaos" },
  { path: /^\/bff\/sandbox\/traffic$/, methods: ["GET", "POST"], need: "sandbox.traffic" },
  { path: /^\/bff\/sandbox\/generator$/, methods: ["GET", "POST"], need: "sandbox.generator" },
];

export type Decision = { rule: Rule } | { status: 404 | 405 };

export function matchBff(pathname: string, method: string): Decision {
  const candidates = BFF_POLICY.filter((r) => r.path.test(pathname));
  if (candidates.length === 0) return { status: 404 };
  const rule = candidates.find((r) => r.methods.includes(method));
  return rule ? { rule } : { status: 405 };
}
