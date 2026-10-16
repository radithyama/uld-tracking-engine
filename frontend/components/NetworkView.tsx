"use client";

import { useMemo } from "react";
import type { FlightLoad, Station, ULD } from "@/lib/types";
import { kg, timeHM, useNow } from "@/lib/format";
import { countdown, progress } from "@/lib/time";
import { Panel } from "./Panel";

interface Props {
  stations: Station[];
  flights: FlightLoad[];
  ulds: ULD[];
  station: string;
  timeScale: number;
  onStation: (iata: string) => void;
}

const W = 760;
const H = 430;
const LON: [number, number] = [96, 154];
const LAT: [number, number] = [-43, 41];
const px = (lon: number) => ((lon - LON[0]) / (LON[1] - LON[0])) * W;
const py = (lat: number) => ((LAT[1] - lat) / (LAT[1] - LAT[0])) * H;

/** Quadratic arc between two points, bowed to one side. */
function arc(a: Station, b: Station) {
  const x1 = px(a.lon), y1 = py(a.lat), x2 = px(b.lon), y2 = py(b.lat);
  const mx = (x1 + x2) / 2, my = (y1 + y2) / 2;
  const dx = x2 - x1, dy = y2 - y1, len = Math.hypot(dx, dy) || 1;
  const bow = Math.min(60, len * 0.18);
  const cx = mx - (dy / len) * bow, cy = my + (dx / len) * bow;
  const at = (t: number) => ({
    x: (1 - t) ** 2 * x1 + 2 * (1 - t) * t * cx + t ** 2 * x2,
    y: (1 - t) ** 2 * y1 + 2 * (1 - t) * t * cy + t ** 2 * y2,
  });
  const heading = (t: number) => {
    const d = { x: 2 * (1 - t) * (cx - x1) + 2 * t * (x2 - cx), y: 2 * (1 - t) * (cy - y1) + 2 * t * (y2 - cy) };
    return (Math.atan2(d.y, d.x) * 180) / Math.PI;
  };
  return { d: `M ${x1} ${y1} Q ${cx} ${cy} ${x2} ${y2}`, at, heading };
}

export function NetworkView({ stations, flights, ulds, station, timeScale, onStation }: Props) {
  const now = useNow(1000);
  const byIata = useMemo(() => Object.fromEntries(stations.map((s) => [s.iata, s])), [stations]);
  const airborne = flights.filter((f) => f.status === "DEPARTED");
  const routes = useMemo(() => {
    const seen = new Set<string>();
    return flights.filter((f) => {
      const k = [f.origin, f.destination].sort().join("-");
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
  }, [flights]);
  const inFlight = ulds.filter((u) => u.status === "IN_FLIGHT");
  const ulDsByFlight = useMemo(() => {
    const m: Record<number, ULD[]> = {};
    for (const u of inFlight) if (u.flight_id != null) (m[u.flight_id] ??= []).push(u);
    return m;
  }, [inFlight]);
  const atStation = useMemo(() => {
    const m: Record<string, number> = {};
    for (const u of ulds) if (u.station) m[u.station] = (m[u.station] ?? 0) + 1;
    return m;
  }, [ulds]);

  return (
    <div className="grid gap-5 xl:grid-cols-[1fr_420px]">
      <Panel title="Network" subtitle={`Live flights and ULDs in the air · network clock ×${timeScale}`}>
        <svg viewBox={`0 0 ${W} ${H}`} className="w-full rounded-lg bg-slate-950/60" role="img" aria-label="Network map">
          {Array.from({ length: 6 }, (_, i) => 100 + i * 10).map((lon) => (
            <line key={`lon${lon}`} x1={px(lon)} x2={px(lon)} y1={0} y2={H} className="stroke-slate-800/70" />
          ))}
          {Array.from({ length: 9 }, (_, i) => -40 + i * 10).map((lat) => (
            <g key={`lat${lat}`}>
              <line x1={0} x2={W} y1={py(lat)} y2={py(lat)} className={lat === 0 ? "stroke-slate-700" : "stroke-slate-800/70"} />
              <text x={4} y={py(lat) - 3} className="fill-slate-700 text-[9px]">
                {lat === 0 ? "EQ" : `${Math.abs(lat)}°${lat > 0 ? "N" : "S"}`}
              </text>
            </g>
          ))}
          {routes.map((f) => {
            const a = byIata[f.origin], b = byIata[f.destination];
            if (!a || !b) return null;
            return <path key={`r${f.id}`} d={arc(a, b).d} fill="none" className="stroke-slate-700" strokeDasharray="3 5" />;
          })}
          {airborne.map((f, i) => {
            const a = byIata[f.origin], b = byIata[f.destination];
            if (!a || !b) return null;
            const g = arc(a, b);
            const t = progress(f.departed_at, f.eta, now);
            const p = g.at(t);
            return (
              <g key={`f${f.id}`}>
                <path d={g.d} fill="none" className="stroke-violet-500/50" strokeWidth={1.5} />
                <g transform={`translate(${p.x} ${p.y}) rotate(${g.heading(t)})`}>
                  <path d="M 9 0 L -6 -6 L -3 0 L -6 6 Z" className="fill-violet-300" />
                </g>
                {/* staggered so simultaneous departures don't overprint */}
                <text x={p.x + 10} y={p.y - 8 - (i % 3) * 12} className="fill-violet-200 text-[10px] font-mono">
                  {f.flight_number} · {countdown(f.eta, now)}
                </text>
              </g>
            );
          })}
          {stations.map((s) => {
            const active = s.iata === station;
            const flip = px(s.lon) > W - 70; // keep labels inside the frame
            const lx = flip ? px(s.lon) - 12 : px(s.lon) + 12;
            const anchor = flip ? "end" : "start";
            return (
              <g key={s.iata} onClick={() => onStation(s.iata)} className="cursor-pointer" role="button" aria-label={`Select ${s.iata}`}>
                <circle cx={px(s.lon)} cy={py(s.lat)} r={active ? 9 : 6} className={active ? "fill-sky-400" : "fill-slate-500"} />
                <circle cx={px(s.lon)} cy={py(s.lat)} r={active ? 15 : 0} className="fill-none stroke-sky-400/40" />
                <text x={lx} y={py(s.lat) + 4} textAnchor={anchor} className={`text-[12px] font-semibold ${active ? "fill-sky-200" : "fill-slate-300"}`}>
                  {s.iata}
                </text>
                <text x={lx} y={py(s.lat) + 16} textAnchor={anchor} className="fill-slate-500 text-[9px]">
                  {atStation[s.iata] ?? 0} ULDs
                </text>
              </g>
            );
          })}
        </svg>
      </Panel>

      <Panel title="In flight" subtitle={`${inFlight.length} ULDs airborne on ${airborne.length} flights`}>
        <div className="space-y-3">
          {airborne
            .slice()
            .sort((a, b) => (a.eta ?? "").localeCompare(b.eta ?? ""))
            .map((f) => {
              const list = ulDsByFlight[f.id] ?? [];
              const t = progress(f.departed_at, f.eta, now);
              return (
                <div key={f.id} className="rounded-lg border border-slate-800 bg-slate-900/50 p-3">
                  <div className="flex items-baseline justify-between">
                    <span className="font-mono font-semibold text-slate-100">
                      {f.flight_number} <span className="text-xs font-normal text-slate-400">{f.origin} → {f.destination}</span>
                    </span>
                    <span className="text-xs tabular-nums text-sky-300">ETA {f.eta ? timeHM(f.eta) : "—"} · {countdown(f.eta, now)}</span>
                  </div>
                  <div className="mt-2 h-1.5 overflow-hidden rounded bg-slate-800">
                    <div className="h-full bg-violet-400 transition-[width] duration-1000" style={{ width: `${t * 100}%` }} />
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {list.map((u) => (
                      <span key={u.id} className="rounded bg-violet-500/10 px-1.5 py-0.5 font-mono text-[10px] text-violet-200" title={kg(u.weight_kg)}>
                        {u.id}
                      </span>
                    ))}
                    {list.length === 0 && <span className="text-[11px] text-slate-500">ferry flight (no ULDs)</span>}
                  </div>
                </div>
              );
            })}
          {airborne.length === 0 && <p className="text-sm text-slate-500">No flights airborne right now.</p>}
        </div>
      </Panel>
    </div>
  );
}
