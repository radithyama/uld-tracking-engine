"use client";

import { useMemo } from "react";
import type { ULD, WarehouseLocation } from "@/lib/types";
import { overweightChip, statusStyle } from "./StatusBadge";

interface Props {
  locations: WarehouseLocation[];
  ulds: ULD[];
  selected: string | null;
  onSelect: (id: string | null) => void;
}

// Zone accents match the status a ULD takes on in that zone.
const ZONE_ACCENT: Record<string, string> = {
  RCV: "bg-sky-500",
  BUP: "bg-sky-500",
  STA: "bg-amber-500",
  STB: "bg-amber-500",
  RMP: "bg-emerald-500",
};

/** Visual "full" mark for the occupancy bar; bays can hold more. */
const BAY_SOFT_CAPACITY = 6;
/** Chips shown per bay before collapsing the rest into "+N". */
const MAX_CHIPS = 5;

/** Live floor plan: rows are zones (landside at top, airside at bottom). */
export function WarehouseGrid({ locations, ulds, selected, onSelect }: Props) {
  const { rows, cols, byLocation, zoneNames } = useMemo(() => {
    const byLocation = new Map<string, ULD[]>();
    for (const u of ulds) {
      const list = byLocation.get(u.location_id);
      if (list) list.push(u);
      else byLocation.set(u.location_id, [u]);
    }
    const rows = new Map<number, WarehouseLocation[]>();
    const zoneNames = new Map<number, { name: string; zone: string }>();
    let cols = 0;
    for (const l of locations) {
      if (!rows.has(l.y)) rows.set(l.y, []);
      rows.get(l.y)!.push(l);
      zoneNames.set(l.y, { name: l.label.replace(/\s+\d+$/, "").replace(/^[A-Z]{3} /, ""), zone: l.zone });
      cols = Math.max(cols, l.x + 1);
    }
    for (const r of rows.values()) r.sort((a, b) => a.x - b.x);
    return { rows: [...rows.entries()].sort(([a], [b]) => a - b), cols, byLocation, zoneNames };
  }, [locations, ulds]);

  if (locations.length === 0) {
    return <div className="grid h-64 place-items-center text-sm text-muted">No floor plan for this hub.</div>;
  }

  return (
    <div className="space-y-2.5 overflow-x-auto pb-1">
      {rows.map(([y, locs]) => {
        const z = zoneNames.get(y)!;
        return (
          <div key={y} className="flex items-stretch gap-2.5">
            <div className="flex w-24 shrink-0 items-center gap-2">
              <span className={`h-8 w-1 rounded-full ${ZONE_ACCENT[z.zone] ?? "bg-slate-400"}`} />
              <div className="leading-tight">
                <div className="text-xs font-semibold text-ink">{z.name}</div>
                <div className="font-mono text-[10px] text-muted">{z.zone}</div>
              </div>
            </div>
            <div className="grid flex-1 gap-2" style={{ gridTemplateColumns: `repeat(${cols}, minmax(92px, 1fr))` }}>
              {locs.map((loc) => {
                // Overweight containers first, so a capped bay never hides an alert.
                const here = [...(byLocation.get(loc.id) ?? [])].sort((a, b) => Number(b.overweight) - Number(a.overweight));
                const alert = here.some((u) => u.overweight);
                const bay = loc.id.match(/(\d+)$/)?.[1] ?? loc.id;
                return (
                  <div
                    key={loc.id}
                    title={loc.label}
                    className={`relative flex min-h-[76px] flex-col overflow-hidden rounded-lg border p-2 transition-colors ${
                      alert
                        ? "border-rose-300 bg-rose-50/70 dark:border-rose-500/50 dark:bg-rose-500/10"
                        : here.length
                          ? "border-line bg-surface shadow-xs"
                          : "border-dashed border-line bg-surface-2/50"
                    }`}
                  >
                    <div className="mb-1.5 flex items-center justify-between">
                      <span className="font-mono text-[10px] font-medium text-muted">
                        {loc.zone}·<span className="text-ink-2">{bay}</span>
                      </span>
                      {here.length > 0 && (
                        <span className="rounded-full bg-surface-2 px-1.5 text-[10px] font-semibold tabular-nums text-ink-2 ring-1 ring-line">
                          {here.length}
                        </span>
                      )}
                    </div>
                    <div className="flex flex-wrap gap-1">
                      {here.slice(0, MAX_CHIPS).map((u) => (
                        <button
                          // Keyed on version so the flash animation replays on every update.
                          key={`${u.id}:${u.version}`}
                          type="button"
                          onClick={() => onSelect(selected === u.id ? null : u.id)}
                          title={`${u.id} · ${u.flight_number || "unassigned"} · ${Math.round(u.weight_kg)} kg${u.overweight ? " · OVER LIMIT" : ""}`}
                          className={`rounded-md px-1.5 py-0.5 font-mono text-[10px] font-medium leading-4 ring-1 ring-inset transition ${
                            u.overweight ? `${overweightChip} font-semibold` : `flash ${statusStyle[u.status].chip}`
                          } ${selected === u.id ? "outline-2 outline-offset-1 outline-accent" : ""}`}
                        >
                          {u.id.slice(0, 8)}
                        </button>
                      ))}
                      {here.length > MAX_CHIPS && (
                        <span
                          className="rounded-md bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium leading-4 text-muted ring-1 ring-inset ring-line"
                          title={here.slice(MAX_CHIPS).map((u) => u.id).join(", ")}
                        >
                          +{here.length - MAX_CHIPS}
                        </span>
                      )}
                    </div>
                    {here.length > 0 && (
                      <span
                        className="absolute inset-x-0 bottom-0 h-0.5 bg-accent/50"
                        style={{ width: `${Math.min(1, here.length / BAY_SOFT_CAPACITY) * 100}%` }}
                        aria-hidden="true"
                      />
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}
