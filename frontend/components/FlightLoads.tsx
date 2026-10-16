import type { FlightLoad } from "@/lib/types";
import { kg, timeHM, tonnes } from "@/lib/format";

const STATUS_CHIP: Record<string, string> = {
  SCHEDULED: "text-sky-300 bg-sky-500/10",
  DEPARTED: "text-violet-300 bg-violet-500/10",
  ARRIVED: "text-emerald-300 bg-emerald-500/10",
  CANCELLED: "text-slate-400 bg-slate-500/10",
};

export function FlightLoads({ flights, onSelect }: { flights: FlightLoad[]; onSelect?: (f: FlightLoad) => void }) {
  if (flights.length === 0) {
    return <p className="text-sm text-slate-500">No departures scheduled from this station.</p>;
  }
  return (
    <div className="space-y-3">
      {flights.map((f) => {
        const pct = (f.total_weight_kg / f.max_payload_kg) * 100;
        const bar = f.over_payload ? "bg-red-500" : pct >= 90 ? "bg-amber-400" : "bg-emerald-400";
        const alert = f.over_payload || f.overweight_ulds > 0;
        const Tag = onSelect ? "button" : "div";
        return (
          <Tag
            key={f.id}
            type={onSelect ? "button" : undefined}
            onClick={onSelect ? () => onSelect(f) : undefined}
            className={`block w-full rounded-lg border p-3 text-left ${alert ? "border-red-500/50 bg-red-500/5" : "border-slate-800 bg-slate-900/50"} ${onSelect ? "transition hover:border-sky-500/50" : ""}`}
          >
            <div className="flex items-baseline justify-between gap-2">
              <div className="flex items-baseline gap-2">
                <span className="font-mono text-base font-semibold text-slate-100">{f.flight_number}</span>
                <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${STATUS_CHIP[f.status] ?? ""}`}>{f.status}</span>
              </div>
              <div className="text-xs text-slate-400">
                {f.origin} → {f.destination} · STD {timeHM(f.departure)}
              </div>
            </div>
            <div className="mt-0.5 text-xs text-slate-500">
              {f.aircraft_type} · {f.aircraft} · max/ULD {kg(f.max_uld_weight_kg)}
            </div>
            <div className="mt-2 h-2 overflow-hidden rounded-full bg-slate-800">
              <div className={`h-full ${bar} transition-[width] duration-500`} style={{ width: `${Math.min(pct, 100)}%` }} />
            </div>
            <div className="mt-1 flex justify-between text-xs tabular-nums">
              <span className={f.over_payload ? "font-semibold text-red-300" : "text-slate-300"}>
                {tonnes(f.total_weight_kg)} / {tonnes(f.max_payload_kg)} ({pct.toFixed(0)}%)
              </span>
              <span className="text-slate-400">
                {f.uld_count}/{f.uld_positions} ULDs
                {f.overweight_ulds > 0 && <span className="ml-1 text-red-400">· {f.overweight_ulds} over</span>}
              </span>
            </div>
          </Tag>
        );
      })}
    </div>
  );
}
