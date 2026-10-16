import type { FlightLoad, ULD } from "@/lib/types";
import { kg, tonnes } from "@/lib/format";

interface Props {
  overweight: ULD[];
  overPayload: FlightLoad[];
  onSelect: (id: string) => void;
}

export function AlertsPanel({ overweight, overPayload, onSelect }: Props) {
  if (overweight.length === 0 && overPayload.length === 0) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-emerald-500/30 bg-emerald-500/5 px-3 py-2 text-sm text-emerald-300">
        <span>✓</span> All ULDs and flights within weight limits
      </div>
    );
  }
  return (
    <ul className="space-y-2" role="alert" aria-live="polite">
      {overPayload.map((f) => (
        <li
          key={`f-${f.flight_number}`}
          className="rounded-lg border border-red-500/60 bg-red-500/10 px-3 py-2 text-sm"
        >
          <div className="font-semibold text-red-200">
            ✈ {f.flight_number} over max payload
          </div>
          <div className="text-xs text-red-300/80">
            {`${tonnes(f.total_weight_kg)} assigned vs ${tonnes(f.max_payload_kg)} allowed on ${f.aircraft_type} (+${kg(f.total_weight_kg - f.max_payload_kg)})`}
          </div>
        </li>
      ))}
      {overweight.map((u) => (
        <li key={`u-${u.id}`}>
          <button
            type="button"
            onClick={() => onSelect(u.id)}
            className="w-full rounded-lg border border-red-500/40 bg-red-500/5 px-3 py-2 text-left text-sm hover:bg-red-500/10"
          >
            <div className="flex justify-between">
              <span className="font-mono font-semibold text-red-200">⚠ {u.id}</span>
              <span className="font-mono text-xs text-slate-400">{u.location_id}</span>
            </div>
            <div className="text-xs text-red-300/80">
              {`${kg(u.weight_kg)} exceeds ${u.flight_number ? `${u.flight_number} ` : ""}limit of ${kg(u.weight_limit_kg)} (+${kg(u.weight_kg - u.weight_limit_kg)})`}
            </div>
          </button>
        </li>
      ))}
    </ul>
  );
}
