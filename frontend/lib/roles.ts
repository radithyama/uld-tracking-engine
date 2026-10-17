// Role model shared by the UI (what to show) and the BFF route handlers (what
// to allow). The Go realtime service enforces the same matrix again on every
// request, so hiding a control here is UX, never the security boundary.

export const ROLES = ["ground_handler", "loadmaster", "security_auditor"] as const;
export type Role = (typeof ROLES)[number];

export const isRole = (v: unknown): v is Role => typeof v === "string" && (ROLES as readonly string[]).includes(v);

export type Tab = "operations" | "flights" | "network" | "security";

export type Capability =
  | "view.manifest" // full ULD manifest + flight loads
  | "loadplan.view"
  | "loadplan.edit" // optimize + manual moves
  | "security.view" // live SOC stream, alert triage, audit log
  | "sandbox.scan"
  | "sandbox.chaos" // tamper / impossible travel / unauthorized scan
  | "sandbox.traffic"
  | "sandbox.generator"; // demo traffic generator on/off (shared by all viewers)

interface RoleInfo {
  label: string;
  blurb: string;
  tabs: Tab[];
  can: Capability[];
}

export const ROLE_INFO: Record<Role, RoleInfo> = {
  ground_handler: {
    label: "Ground Handler",
    blurb: "Read-only warehouse map and the manual scanner tool.",
    tabs: ["operations", "network"],
    can: ["sandbox.scan", "sandbox.generator"],
  },
  loadmaster: {
    label: "Loadmaster",
    blurb: "Flight manifests, load planning & CG optimization, weight overrides.",
    tabs: ["operations", "flights", "network"],
    can: ["view.manifest", "loadplan.view", "loadplan.edit", "sandbox.scan", "sandbox.chaos", "sandbox.traffic", "sandbox.generator"],
  },
  security_auditor: {
    label: "Security Auditor",
    blurb: "Live SOC security stream, alert triage and the audit log.",
    tabs: ["operations", "flights", "network", "security"],
    can: ["view.manifest", "loadplan.view", "security.view", "sandbox.chaos", "sandbox.generator"],
  },
};

export const can = (role: Role | undefined, cap: Capability) => !!role && ROLE_INFO[role].can.includes(cap);
