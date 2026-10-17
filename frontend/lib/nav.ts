import { Globe2, LayoutGrid, Scale, ShieldAlert, type LucideIcon } from "lucide-react";
import type { Tab } from "./roles";

export const VIEWS: Record<Tab, { label: string; short: string; icon: LucideIcon; blurb: string }> = {
  operations: { label: "Operations Floor", short: "Operations", icon: LayoutGrid, blurb: "Live warehouse map, manifest & weight alerts" },
  network: { label: "Flight Network Map", short: "Network", icon: Globe2, blurb: "Flights and ULDs in the air, ETAs" },
  flights: { label: "Load Optimization", short: "Load Planning", icon: Scale, blurb: "Departures, aircraft holds & CG optimizer" },
  security: { label: "SOC & Audit Logs", short: "SOC", icon: ShieldAlert, blurb: "Security alert stream, triage & audit trail" },
};

export const VIEW_ORDER: Tab[] = ["operations", "network", "flights", "security"];

export const REPO_URL = "https://github.com/radithyama/uld-tracking-engine";
