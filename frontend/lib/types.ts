// Mirrors backend/internal/model/model.go and the realtime API responses.

export type ULDStatus = "IN_TRANSIT" | "STAGED" | "LOADED" | "IN_FLIGHT";
export type FlightStatus = "SCHEDULED" | "DEPARTED" | "ARRIVED" | "CANCELLED";

export interface ULD {
  id: string;
  uld_type: string;
  weight_kg: number;
  max_gross_weight_kg: number;
  /** Effective limit: min(ULD rating, aircraft per-position limit). */
  weight_limit_kg: number;
  overweight: boolean;
  location_id: string;
  zone: string;
  /** IATA code of the current location; empty while airborne. */
  station: string;
  flight_id: number | null;
  flight_number: string;
  flight_status: FlightStatus | "";
  origin: string;
  destination: string;
  eta: string | null;
  status: ULDStatus;
  last_scanner_id: string;
  last_scan_at: string | null;
  version: number;
  updated_at: string;
}

export interface FlightLoad {
  id: number;
  flight_number: string;
  status: FlightStatus;
  aircraft: string;
  aircraft_type: string;
  origin: string;
  destination: string;
  departure: string;
  departed_at: string | null;
  eta: string | null;
  arrived_at: string | null;
  max_payload_kg: number;
  max_uld_weight_kg: number;
  uld_positions: number;
  total_weight_kg: number;
  uld_count: number;
  overweight_ulds: number;
  over_payload: boolean;
  version: number;
}

export interface WarehouseLocation {
  id: string;
  station: string;
  zone: string;
  label: string;
  x: number;
  y: number;
  implied_status: ULDStatus;
}

export interface Station {
  iata: string;
  name: string;
  country: string;
  lat: number;
  lon: number;
}

export type ServerMessage =
  | {
      type: "snapshot";
      data: {
        ulds: ULD[];
        flights: FlightLoad[];
        locations: WarehouseLocation[];
        stations: Station[];
        time_scale: number;
      };
    }
  | { type: "uld_update"; data: ULD }
  | { type: "uld_removed"; data: { id: string } }
  | { type: "flight_update"; data: FlightLoad }
  | { type: "flight_removed"; data: { id: number } }
  | { type: "loadplan_update"; data: { flight_id: number; by: string } };

export type ConnectionStatus = "connecting" | "open" | "reconnecting";

// ---- load planning (GET /bff/flights/{id}/loadplan) ----

export interface Position {
  code: string;
  deck: "MAIN" | "LOWER";
  hold: "MAIN" | "FWD" | "AFT" | "BULK";
  arm_m: number;
  lateral: -1 | 0 | 1;
  max_weight_kg: number;
  uld_types: string[];
  seq: number;
}

export interface PlanULD {
  id: string;
  uld_type: string;
  weight_kg: number;
  status: ULDStatus;
  locked: boolean;
  current_position?: string;
}

export interface Assignment {
  uld_id: string;
  position: string;
}

export interface PlanIssue {
  code: string;
  uld_id?: string;
  position?: string;
  message: string;
}

export interface Balance {
  cargo_kg: number;
  zfw_kg: number;
  cg_arm_m: number;
  cg_pct_mac: number;
  target_pct_mac: number;
  fwd_limit_pct_mac: number;
  aft_limit_pct_mac: number;
  within_envelope: boolean;
  lateral_imbalance_kg: number;
  hold_kg: Record<string, number>;
}

export interface Plan {
  assignments: Assignment[];
  unassigned: PlanIssue[];
  issues: PlanIssue[];
  balance: Balance;
  cost: number;
  iterations: number;
}

export interface LoadPlanView {
  flight: {
    flight_id: number;
    flight_number: string;
    status: FlightStatus;
    origin: string;
    destination: string;
    departure: string;
    aircraft: {
      registration: string;
      type: string;
      max_payload_kg: number;
      lemac_m: number;
      mac_m: number;
      cg_fwd_pct: number;
      cg_aft_pct: number;
      cg_target_pct: number;
    };
    positions: Position[];
    ulds: PlanULD[];
    current: Assignment[];
  };
  plan: Plan;
  plannable: boolean;
}

// ---- security (/ws/security, /bff/security/alerts) ----

export type AlertStatus = "OPEN" | "ACKNOWLEDGED" | "RESOLVED";

export interface SecurityAlert {
  id: string;
  occurred_at: string;
  rule_id: string;
  rule_name: string;
  action: string;
  kind: "alert" | "event";
  severity: number;
  service: string;
  reason: string;
  source_ip: string | null;
  principal: string | null;
  uld_id: string | null;
  details: Record<string, unknown>;
  status: AlertStatus;
  acked_by: string | null;
  acked_at: string | null;
  resolved_by: string | null;
  resolved_at: string | null;
  note: string | null;
}

export interface AuditEntry {
  id: number;
  at: string;
  actor: string;
  role: string;
  action: string;
  target: string;
  details: Record<string, unknown>;
}
