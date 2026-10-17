"use client";

import { useMemo } from "react";
import { PlaneLanding, PlaneTakeoff } from "lucide-react";
import type { FlightLoad, ULD } from "@/lib/types";
import { timeHM, tonnes, useNow } from "@/lib/format";
import { countdown, progress } from "@/lib/time";
import { Panel } from "./Panel";

interface Props {
  station: string;
  flights: FlightLoad[];
  ulds: ULD[];
  timeScale: number;
  onOpen: (f: FlightLoad) => void;
}

/** Departures and arrivals boards for the selected station. */
export function FlightsView({ station, flights, ulds, timeScale, onOpen }: Props) {
  const now = useNow();
  const departures = useMemo(
    () => flights.filter((f) => f.origin === station && f.status === "SCHEDULED").sort((a, b) => a.departure.localeCompare(b.departure)),
    [flights, station],
  );
  const arrivals = useMemo(
    () =>
      flights
        .filter((f) => f.destination === station && (f.status === "DEPARTED" || f.status === "ARRIVED"))
        .sort((a, b) => (a.eta ?? "").localeCompare(b.eta ?? "")),
    [flights, station],
  );
  const readyByFlight = useMemo(() => {
    const m: Record<number, { staged: number; loaded: number; building: number }> = {};
    for (const u of ulds) {
      if (u.flight_id == null) continue;
      const r = (m[u.flight_id] ??= { staged: 0, loaded: 0, building: 0 });
      if (u.status === "LOADED") r.loaded++;
      else if (u.status === "STAGED") r.staged++;
      else if (u.status === "IN_TRANSIT") r.building++;
    }
    return m;
  }, [ulds]);

  const th = "px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-muted";
  const td = "px-3 py-2.5";

  return (
    <div className="grid gap-5 xl:grid-cols-2">
      <Panel icon={PlaneTakeoff} title={`Departures · ${station}`} subtitle="Click a flight to open its load plan and CG optimizer">
        <table className="w-full text-sm">
          <thead>
            <tr>
              <th className={th}>Flight</th>
              <th className={th}>To</th>
              <th className={th}>STD</th>
              <th className={th}>Departs in</th>
              <th className={th}>ULDs ready</th>
              <th className={`${th} text-right`}>Load</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {departures.map((f) => {
              const r = readyByFlight[f.id] ?? { staged: 0, loaded: 0, building: 0 };
              const pct = (f.total_weight_kg / f.max_payload_kg) * 100;
              return (
                <tr key={f.id} onClick={() => onOpen(f)} className="cursor-pointer hover:bg-surface-2/70">
                  <td className={`${td} font-mono font-semibold text-ink`}>
                    {f.flight_number}
                    <div className="text-[10px] font-normal text-muted">{f.aircraft_type}</div>
                  </td>
                  <td className={`${td} font-mono text-ink-2`}>{f.destination}</td>
                  <td className={`${td} tabular-nums text-ink-2`}>{timeHM(f.departure)}</td>
                  <td className={`${td} tabular-nums text-sky-700 dark:text-sky-300`}>{countdown(f.departure, now)}</td>
                  <td className={`${td} text-xs text-muted`}>
                    <span className="text-emerald-700 dark:text-emerald-300">{r.loaded} loaded</span> · <span className="text-amber-700 dark:text-amber-300">{r.staged} staged</span>
                    {r.building > 0 && <> · {r.building} building</>}
                  </td>
                  <td className={`${td} text-right tabular-nums ${f.over_payload ? "font-semibold text-rose-600 dark:text-rose-300" : "text-ink-2"}`}>
                    {tonnes(f.total_weight_kg)} <span className="text-muted">({pct.toFixed(0)}%)</span>
                    {f.overweight_ulds > 0 && <div className="text-[10px] text-rose-600 dark:text-rose-400">{f.overweight_ulds} ULD over limit</div>}
                  </td>
                </tr>
              );
            })}
            {departures.length === 0 && (
              <tr>
                <td colSpan={6} className="px-3 py-8 text-center text-muted">
                  No scheduled departures from {station}.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </Panel>

      <Panel icon={PlaneLanding} title={`Arrivals · ${station}`} subtitle={`Network clock ×${timeScale}: flights are time-compressed for the demo`}>
        <table className="w-full text-sm">
          <thead>
            <tr>
              <th className={th}>Flight</th>
              <th className={th}>From</th>
              <th className={th}>Status</th>
              <th className={th}>ETA</th>
              <th className={th}>ULDs</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {arrivals.map((f) => (
              <tr key={f.id} onClick={() => onOpen(f)} className="cursor-pointer hover:bg-surface-2/70">
                <td className={`${td} font-mono font-semibold text-ink`}>{f.flight_number}</td>
                <td className={`${td} font-mono text-ink-2`}>{f.origin}</td>
                <td className={td}>
                  {f.status === "DEPARTED" ? (
                    <div>
                      <span className="text-xs text-violet-700 dark:text-violet-300">✈ airborne</span>
                      <div className="mt-1 h-1 w-24 overflow-hidden rounded bg-surface-2">
                        <div className="h-full bg-violet-500" style={{ width: `${progress(f.departed_at, f.eta, now) * 100}%` }} />
                      </div>
                    </div>
                  ) : (
                    <span className="text-xs text-emerald-700 dark:text-emerald-300">landed {f.arrived_at ? timeHM(f.arrived_at) : ""}</span>
                  )}
                </td>
                <td className={`${td} tabular-nums text-ink-2`}>
                  {f.eta ? timeHM(f.eta) : "—"}
                  {f.status === "DEPARTED" && <div className="text-[10px] text-sky-700 dark:text-sky-300">{countdown(f.eta, now)}</div>}
                </td>
                <td className={`${td} tabular-nums text-muted`}>{f.uld_count}</td>
              </tr>
            ))}
            {arrivals.length === 0 && (
              <tr>
                <td colSpan={5} className="px-3 py-8 text-center text-muted">
                  Nothing inbound to {station}.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </Panel>
    </div>
  );
}
