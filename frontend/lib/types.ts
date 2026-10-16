// Mirrors backend/internal/model/model.go.

export type ULDStatus = "IN_TRANSIT" | "STAGED" | "LOADED";

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
  flight_number: string;
  status: ULDStatus;
  last_scanner_id: string;
  last_scan_at: string | null;
  version: number;
  updated_at: string;
}

export interface FlightLoad {
  flight_number: string;
  aircraft: string;
  aircraft_type: string;
  origin: string;
  destination: string;
  departure: string;
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
  zone: string;
  label: string;
  x: number;
  y: number;
  implied_status: ULDStatus;
}

export type ServerMessage =
  | {
      type: "snapshot";
      data: { ulds: ULD[]; flights: FlightLoad[]; locations: WarehouseLocation[] };
    }
  | { type: "uld_update"; data: ULD }
  | { type: "flight_update"; data: FlightLoad };

export type ConnectionStatus = "connecting" | "open" | "reconnecting";
