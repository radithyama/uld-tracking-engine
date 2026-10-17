"use client";

import { useMemo, useState } from "react";
import { Boxes, ClipboardList, PackageCheck, PlaneLanding, Scale, ScanLine, TriangleAlert, Warehouse, Weight } from "lucide-react";
import type { FlightLoad, ULD, WarehouseLocation } from "@/lib/types";
import { WarehouseGrid } from "./WarehouseGrid";
import { UldTable } from "./UldTable";
import { FlightLoads } from "./FlightLoads";
import { AlertsPanel } from "./AlertsPanel";
import { Kpi, Panel } from "./Panel";

interface Props {
  station: string;
  ulds: ULD[];
  locations: WarehouseLocation[];
  flights: FlightLoad[];
  manifest: boolean; // role may see the full manifest + flight loads
  onOpenFlight?: (f: FlightLoad) => void;
}

/** The station's warehouse floor, manifest and weight alerts. */
export function OperationsView({ station, ulds, locations, flights, manifest, onOpenFlight }: Props) {
  const [selected, setSelected] = useState<string | null>(null);

  const here = useMemo(() => ulds.filter((u) => u.station === station), [ulds, station]);
  const inbound = useMemo(() => ulds.filter((u) => u.status === "IN_FLIGHT" && u.destination === station), [ulds, station]);
  const floor = useMemo(() => locations.filter((l) => l.station === station), [locations, station]);
  const departures = useMemo(
    () => flights.filter((f) => f.origin === station && f.status === "SCHEDULED").sort((a, b) => a.departure.localeCompare(b.departure)),
    [flights, station],
  );
  const overweight = useMemo(
    () => here.filter((u) => u.overweight).sort((a, b) => b.weight_kg / b.weight_limit_kg - a.weight_kg / a.weight_limit_kg),
    [here],
  );
  const overPayload = useMemo(() => departures.filter((f) => f.over_payload), [departures]);

  const counts = useMemo(() => {
    const c = { IN_TRANSIT: 0, STAGED: 0, LOADED: 0, IN_FLIGHT: 0, weight: 0 };
    for (const u of here) {
      c[u.status]++;
      c.weight += u.weight_kg;
    }
    return c;
  }, [here]);
  const occupied = useMemo(() => new Set(here.map((u) => u.location_id)).size, [here]);
  const alertCount = overweight.length + overPayload.length;

  return (
    <div className="space-y-5">
      {alertCount > 0 && (
        <div className="flex items-center gap-3 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800 shadow-xs dark:border-rose-500/40 dark:bg-rose-500/10 dark:text-rose-200">
          <TriangleAlert size={18} className="shrink-0 text-rose-600 dark:text-rose-300" />
          <span>
            <strong className="font-semibold">
              {alertCount} weight alert{alertCount === 1 ? "" : "s"} at {station}
            </strong>
            <span className="text-rose-700/80 dark:text-rose-300/80">
              {" "}· {overweight.length} ULD{overweight.length === 1 ? "" : "s"} over limit
              {overPayload.length > 0 && `, ${overPayload.length} departure${overPayload.length === 1 ? "" : "s"} over max payload`}
            </span>
          </span>
        </div>
      )}

      <section className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6" aria-label={`${station} metrics`}>
        <Kpi label={`ULDs at ${station}`} value={here.length} unit="units" icon={Boxes} hint={`${occupied}/${floor.length} bays occupied`} progress={floor.length ? occupied / floor.length : 0} />
        <Kpi label="In transit" value={counts.IN_TRANSIT} unit="ULDs" icon={ScanLine} tone="sky" />
        <Kpi label="Staged" value={counts.STAGED} unit="ULDs" icon={ClipboardList} tone="amber" />
        <Kpi label="Loaded" value={counts.LOADED} unit="ULDs" icon={PackageCheck} tone="emerald" />
        <Kpi label="Inbound by air" value={inbound.length} unit="ULDs" icon={PlaneLanding} tone="violet" />
        <Kpi label="Gross on floor" value={(counts.weight / 1000).toFixed(1)} unit="t" icon={Weight} hint={alertCount ? `${overweight.length} over limit` : "all within limits"} tone={alertCount ? "rose" : "slate"} />
      </section>

      <div className="grid gap-5 xl:grid-cols-[1fr_360px]">
        <div className="min-w-0 space-y-5">
          <Panel title={`Warehouse floor · ${station}`} subtitle="Live ULD positions by scan location · click a container to highlight it" icon={Warehouse}>
            <WarehouseGrid locations={floor} ulds={here} selected={selected} onSelect={setSelected} />
          </Panel>
          {manifest && (
            <Panel title="ULD manifest" subtitle={`On the floor at ${station}, plus ULDs inbound by air`} icon={ClipboardList}>
              <UldTable
                ulds={[...here, ...inbound]}
                flights={[...new Set([...here, ...inbound].map((u) => u.flight_number).filter(Boolean))].sort()}
                selected={selected}
                onSelect={setSelected}
              />
            </Panel>
          )}
        </div>
        <aside className="space-y-5">
          <Panel title="Weight alerts" icon={TriangleAlert}>
            <AlertsPanel overweight={overweight} overPayload={overPayload} onSelect={setSelected} />
          </Panel>
          {manifest && (
            <Panel title="Departures" subtitle={onOpenFlight ? "Click a flight to open its aircraft hold" : "Assigned weight vs max payload"} icon={Scale}>
              <FlightLoads flights={departures} onSelect={onOpenFlight} />
            </Panel>
          )}
        </aside>
      </div>
    </div>
  );
}
