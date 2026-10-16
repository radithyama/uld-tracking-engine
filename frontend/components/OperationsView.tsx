"use client";

import { useMemo, useState } from "react";
import type { FlightLoad, ULD, WarehouseLocation } from "@/lib/types";
import { tonnes } from "@/lib/format";
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
  const alertCount = overweight.length + overPayload.length;

  return (
    <div className="space-y-5">
      {alertCount > 0 && (
        <div className="flex items-center gap-3 rounded-lg border border-red-500/60 bg-red-500/15 px-4 py-2.5 text-sm text-red-100">
          <span className="relative flex h-2.5 w-2.5">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-red-400 opacity-75" />
            <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-red-500" />
          </span>
          <strong>
            {alertCount} weight alert{alertCount === 1 ? "" : "s"} at {station}:
          </strong>
          <span className="text-red-200/90">
            {overweight.length} ULD{overweight.length === 1 ? "" : "s"} over limit
            {overPayload.length > 0 && `, ${overPayload.length} departure${overPayload.length === 1 ? "" : "s"} over max payload`}
          </span>
        </div>
      )}

      <section className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Kpi label={`ULDs at ${station}`} value={here.length} />
        <Kpi label="In Transit" value={counts.IN_TRANSIT} accent="text-sky-300" />
        <Kpi label="Staged" value={counts.STAGED} accent="text-amber-300" />
        <Kpi label="Loaded" value={counts.LOADED} accent="text-emerald-300" />
        <Kpi label="Inbound (air)" value={inbound.length} accent="text-violet-300" />
        <Kpi label="Gross on floor" value={tonnes(counts.weight)} />
      </section>

      <div className="grid gap-5 xl:grid-cols-[1fr_360px]">
        <div className="min-w-0 space-y-5">
          <Panel title={`Warehouse floor · ${station}`} subtitle="Live ULD positions by scan location">
            <WarehouseGrid locations={floor} ulds={here} selected={selected} onSelect={setSelected} />
          </Panel>
          {manifest && (
            <Panel title="ULD manifest" subtitle={`On the floor at ${station}, plus ULDs inbound by air`}>
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
          <Panel title="Weight alerts">
            <AlertsPanel overweight={overweight} overPayload={overPayload} onSelect={setSelected} />
          </Panel>
          {manifest && (
            <Panel title="Departures load" subtitle={onOpenFlight ? "Click to open the aircraft deck" : "Assigned ULD weight vs max payload"}>
              <FlightLoads flights={departures} onSelect={onOpenFlight} />
            </Panel>
          )}
        </aside>
      </div>
    </div>
  );
}
