import { Plane } from "lucide-react";
import type { FlightLoad } from "@/lib/types";
import { kg, timeHM, tonnes } from "@/lib/format";

export const FLIGHT_STATUS_CHIP: Record<string, string> = {
  SCHEDULED: "bg-sky-50 text-sky-700 ring-sky-200 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30",
  DEPARTED: "bg-violet-50 text-violet-700 ring-violet-200 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-400/30",
  ARRIVED: "bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
  CANCELLED: "bg-surface-2 text-muted ring-line",
};

/** Payload utilisation: indigo, amber from 90%, rose when over max payload. */
export function payloadBar(f: FlightLoad) {
  const pct = (f.total_weight_kg / f.max_payload_kg) * 100;
  return { pct, cls: f.over_payload ? "bg-rose-500" : pct >= 90 ? "bg-amber-500" : "bg-accent" };
}

export function FlightLoads({ flights, onSelect }: { flights: FlightLoad[]; onSelect?: (f: FlightLoad) => void }) {
  if (flights.length === 0) {
    return <p className="rounded-lg border border-dashed border-line px-3 py-6 text-center text-sm text-muted">No departures scheduled from this hub.</p>;
  }
  return (
    <div className="space-y-2.5">
      {flights.map((f) => {
        const { pct, cls } = payloadBar(f);
        const alert = f.over_payload || f.overweight_ulds > 0;
        const Tag = onSelect ? "button" : "div";
        return (
          <Tag
            key={f.id}
            type={onSelect ? "button" : undefined}
            onClick={onSelect ? () => onSelect(f) : undefined}
            className={`block w-full rounded-lg border p-3 text-left transition ${
              alert ? "border-rose-200 bg-rose-50/60 dark:border-rose-500/40 dark:bg-rose-500/10" : "border-line bg-surface shadow-xs"
            } ${onSelect ? "hover:border-accent/50 hover:shadow-sm" : ""}`}
          >
            <div className="flex items-center justify-between gap-2">
              <div className="flex items-center gap-2">
                <Plane size={14} className="text-muted" />
                <span className="font-mono text-sm font-semibold text-ink">{f.flight_number}</span>
                <span className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ring-1 ring-inset ${FLIGHT_STATUS_CHIP[f.status] ?? ""}`}>{f.status}</span>
              </div>
              <div className="text-xs text-muted">
                {f.origin} → {f.destination} · {timeHM(f.departure)}
              </div>
            </div>
            <div className="mt-1 text-[11px] text-muted">
              {f.aircraft_type} · {f.aircraft} · max/ULD {kg(f.max_uld_weight_kg)}
            </div>
            <div className="mt-2.5 flex items-center justify-between text-[11px]">
              <span className="font-medium text-muted">Payload utilisation</span>
              <span className={`tabular-nums ${f.over_payload ? "font-semibold text-rose-600 dark:text-rose-300" : "text-ink-2"}`}>
                {tonnes(f.total_weight_kg)} / {tonnes(f.max_payload_kg)} · {pct.toFixed(0)}%
              </span>
            </div>
            <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-surface-2">
              <div className={`h-full rounded-full ${cls} transition-[width] duration-500`} style={{ width: `${Math.min(pct, 100)}%` }} />
            </div>
            <div className="mt-1.5 text-[11px] text-muted">
              {f.uld_count}/{f.uld_positions} ULD positions
              {f.overweight_ulds > 0 && <span className="ml-1 font-medium text-rose-600 dark:text-rose-300">· {f.overweight_ulds} over limit</span>}
            </div>
          </Tag>
        );
      })}
    </div>
  );
}
