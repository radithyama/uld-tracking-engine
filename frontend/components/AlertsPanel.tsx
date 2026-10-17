import { CheckCircle2, Plane, TriangleAlert } from "lucide-react";
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
      <div className="flex items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2.5 text-sm text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-300">
        <CheckCircle2 size={16} /> All ULDs and flights within weight limits
      </div>
    );
  }
  const item = "rounded-lg border border-rose-200 bg-rose-50 px-3 py-2.5 text-sm dark:border-rose-500/40 dark:bg-rose-500/10";
  return (
    <ul className="space-y-2" role="alert" aria-live="polite">
      {overPayload.map((f) => (
        <li key={`f-${f.id}`} className={item}>
          <div className="flex items-center gap-1.5 font-semibold text-rose-700 dark:text-rose-200">
            <Plane size={14} /> {f.flight_number} over max payload
          </div>
          <div className="mt-0.5 text-xs text-rose-700/80 dark:text-rose-300/80">
            {`${tonnes(f.total_weight_kg)} assigned vs ${tonnes(f.max_payload_kg)} allowed on ${f.aircraft_type} (+${kg(f.total_weight_kg - f.max_payload_kg)})`}
          </div>
        </li>
      ))}
      {overweight.map((u) => (
        <li key={`u-${u.id}`}>
          <button type="button" onClick={() => onSelect(u.id)} className={`${item} w-full text-left transition hover:border-rose-300`}>
            <div className="flex items-center justify-between gap-2">
              <span className="flex items-center gap-1.5 font-mono font-semibold text-rose-700 dark:text-rose-200">
                <TriangleAlert size={14} /> {u.id}
              </span>
              <span className="font-mono text-xs text-muted">{u.location_id}</span>
            </div>
            <div className="mt-0.5 text-xs text-rose-700/80 dark:text-rose-300/80">
              {`${kg(u.weight_kg)} exceeds ${u.flight_number ? `${u.flight_number} ` : ""}limit of ${kg(u.weight_limit_kg)} (+${kg(u.weight_kg - u.weight_limit_kg)})`}
            </div>
          </button>
        </li>
      ))}
    </ul>
  );
}
