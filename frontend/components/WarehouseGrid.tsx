"use client";

import { useMemo } from "react";
import type { ULD, WarehouseLocation } from "@/lib/types";
import { statusStyle } from "./StatusBadge";

interface Props {
  locations: WarehouseLocation[];
  ulds: ULD[];
  selected: string | null;
  onSelect: (id: string | null) => void;
}

/** Live spatial view: rows are zones (landside at top, airside at bottom). */
export function WarehouseGrid({ locations, ulds, selected, onSelect }: Props) {
  const { rows, cols, byLocation, zoneNames } = useMemo(() => {
    const byLocation = new Map<string, ULD[]>();
    for (const u of ulds) {
      const list = byLocation.get(u.location_id);
      if (list) list.push(u);
      else byLocation.set(u.location_id, [u]);
    }
    const rows = new Map<number, WarehouseLocation[]>();
    const zoneNames = new Map<number, string>();
    let cols = 0;
    for (const l of locations) {
      if (!rows.has(l.y)) rows.set(l.y, []);
      rows.get(l.y)!.push(l);
      zoneNames.set(l.y, l.label.replace(/\s+\d+$/, ""));
      cols = Math.max(cols, l.x + 1);
    }
    for (const r of rows.values()) r.sort((a, b) => a.x - b.x);
    return {
      rows: [...rows.entries()].sort(([a], [b]) => a - b),
      cols,
      byLocation,
      zoneNames,
    };
  }, [locations, ulds]);

  if (locations.length === 0) {
    return <div className="grid h-64 place-items-center text-sm text-slate-500">Waiting for warehouse layout…</div>;
  }

  return (
    <div className="space-y-2 overflow-x-auto">
      {rows.map(([y, locs]) => (
        <div key={y} className="flex items-stretch gap-2">
          <div className="flex w-20 shrink-0 items-center text-[11px] font-semibold uppercase tracking-wider text-slate-500">
            {zoneNames.get(y)}
          </div>
          <div className="grid flex-1 gap-2" style={{ gridTemplateColumns: `repeat(${cols}, minmax(84px, 1fr))` }}>
            {locs.map((loc) => {
              const here = byLocation.get(loc.id) ?? [];
              const alert = here.some((u) => u.overweight);
              return (
                <div
                  key={loc.id}
                  className={`min-h-[68px] rounded-md border p-1.5 transition-colors ${
                    alert
                      ? "border-red-500/60 bg-red-500/10"
                      : here.length
                        ? "border-slate-700 bg-slate-800/60"
                        : "border-slate-800 bg-slate-900/40"
                  }`}
                >
                  <div className="mb-1 flex items-center justify-between font-mono text-[10px] text-slate-500">
                    <span>{loc.id}</span>
                    {here.length > 0 && <span>{here.length}</span>}
                  </div>
                  <div className="flex flex-wrap gap-1">
                    {here.map((u) => (
                      <button
                        // Keyed on version so the flash animation replays on every update.
                        key={`${u.id}:${u.version}`}
                        type="button"
                        onClick={() => onSelect(selected === u.id ? null : u.id)}
                        title={`${u.id} · ${u.flight_number || "unassigned"} · ${Math.round(u.weight_kg)} kg`}
                        className={`rounded px-1 py-px font-mono text-[10px] leading-4 ring-1 ring-inset transition ${
                          u.overweight
                            ? "animate-pulse bg-red-500/80 text-white ring-red-300"
                            : `flash ${statusStyle[u.status].chip}`
                        } ${selected === u.id ? "outline outline-2 outline-offset-1 outline-white" : ""}`}
                      >
                        {u.id.slice(0, 8)}
                      </button>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
}
