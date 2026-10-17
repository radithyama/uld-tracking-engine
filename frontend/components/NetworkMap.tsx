"use client";

import { useMemo } from "react";
import { Globe2, Plane } from "lucide-react";
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
  // Path of the part already flown (for the progress trail).
  const flown = (t: number) => {
    const p = at(t);
    const qx = (1 - t) * x1 + t * cx, qy = (1 - t) * y1 + t * cy;
    return `M ${x1} ${y1} Q ${qx} ${qy} ${p.x} ${p.y}`;
  };
  return { d: `M ${x1} ${y1} Q ${cx} ${cy} ${x2} ${y2}`, at, heading, flown };
}

export function NetworkMap({ stations, flights, ulds, station, timeScale, onStation }: Props) {
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
  const uldsByFlight = useMemo(() => {
    const m: Record<number, ULD[]> = {};
    for (const u of inFlight) if (u.flight_id != null) (m[u.flight_id] ??= []).push(u);
    return m;
  }, [inFlight]);
  const atStation = useMemo(() => {
    const m: Record<string, number> = {};
    for (const u of ulds) if (u.station) m[u.station] = (m[u.station] ?? 0) + 1;
    return m;
  }, [ulds]);
  const hub = byIata.CGK;

  return (
    <div className="grid gap-5 xl:grid-cols-[1fr_400px]">
      <Panel title="Flight network" subtitle={`Live flights and ULDs in the air · network clock ×${timeScale}`} icon={Globe2}>
        <svg
          viewBox={`0 0 ${W} ${H}`}
          className="w-full rounded-lg border border-line bg-gradient-to-b from-sky-50/70 to-indigo-50/40 dark:from-slate-900 dark:to-slate-950"
          role="img"
          aria-label="Network map"
        >
          {/* radar-style grid: graticule + range rings around the hub */}
          {Array.from({ length: 6 }, (_, i) => 100 + i * 10).map((lon) => (
            <line key={`lon${lon}`} x1={px(lon)} x2={px(lon)} y1={0} y2={H} className="stroke-sky-200/70 dark:stroke-slate-800" />
          ))}
          {Array.from({ length: 9 }, (_, i) => -40 + i * 10).map((lat) => (
            <g key={`lat${lat}`}>
              <line x1={0} x2={W} y1={py(lat)} y2={py(lat)} className={lat === 0 ? "stroke-sky-300 dark:stroke-slate-700" : "stroke-sky-200/70 dark:stroke-slate-800"} />
              <text x={4} y={py(lat) - 3} className="fill-sky-700/50 text-[9px] dark:fill-slate-600">
                {lat === 0 ? "EQ" : `${Math.abs(lat)}°${lat > 0 ? "N" : "S"}`}
              </text>
            </g>
          ))}
          {hub &&
            [80, 170, 270, 380].map((r) => (
              <circle key={r} cx={px(hub.lon)} cy={py(hub.lat)} r={r} className="fill-none stroke-indigo-200/70 dark:stroke-indigo-500/15" strokeDasharray="2 6" />
            ))}

          {routes.map((f) => {
            const a = byIata[f.origin], b = byIata[f.destination];
            if (!a || !b) return null;
            return <path key={`r${f.id}`} d={arc(a, b).d} fill="none" className="stroke-slate-300 dark:stroke-slate-700" strokeDasharray="4 5" />;
          })}

          {airborne.map((f, i) => {
            const a = byIata[f.origin], b = byIata[f.destination];
            if (!a || !b) return null;
            const g = arc(a, b);
            const t = progress(f.departed_at, f.eta, now);
            const p = g.at(t);
            return (
              <g key={`f${f.id}`}>
                <path d={g.d} fill="none" className="stroke-indigo-300/70 dark:stroke-indigo-500/30" strokeWidth={1.5} strokeDasharray="5 4" />
                <path d={g.flown(t)} fill="none" className="stroke-accent" strokeWidth={2.5} strokeLinecap="round" />
                <g transform={`translate(${p.x} ${p.y}) rotate(${g.heading(t)})`}>
                  <circle r={9} className="fill-surface stroke-accent" strokeWidth={1.5} />
                  <path d="M 6 0 L -4 -4 L -2 0 L -4 4 Z" className="fill-accent" />
                </g>
                {/* staggered so simultaneous departures don't overprint */}
                <g transform={`translate(${p.x + 13} ${p.y - 10 - (i % 3) * 16})`}>
                  <rect x={-3} y={-10} width={92} height={14} rx={4} className="fill-surface/90 stroke-line" />
                  <text x={2} y={0} className="fill-accent-ink text-[10px] font-semibold">
                    {f.flight_number} · {countdown(f.eta, now)}
                  </text>
                </g>
              </g>
            );
          })}

          {stations.map((s) => {
            const active = s.iata === station;
            const flip = px(s.lon) > W - 70; // keep labels inside the frame
            const lx = flip ? px(s.lon) - 13 : px(s.lon) + 13;
            const anchor = flip ? "end" : "start";
            return (
              <g key={s.iata} onClick={() => onStation(s.iata)} className="cursor-pointer" role="button" aria-label={`Select ${s.iata}`}>
                {active && <circle cx={px(s.lon)} cy={py(s.lat)} r={16} className="fill-accent/10 stroke-accent/40" />}
                <circle cx={px(s.lon)} cy={py(s.lat)} r={active ? 7 : 5.5} className={active ? "fill-accent" : "fill-slate-500 dark:fill-slate-400"} strokeWidth={2} stroke="white" />
                <text x={lx} y={py(s.lat) + 4} textAnchor={anchor} className={`text-[12px] font-bold ${active ? "fill-accent-ink" : "fill-ink"}`}>
                  {s.iata}
                </text>
                <text x={lx} y={py(s.lat) + 16} textAnchor={anchor} className="fill-muted text-[9px]">
                  {atStation[s.iata] ?? 0} ULDs
                </text>
              </g>
            );
          })}
        </svg>
      </Panel>

      <Panel title="In flight" subtitle={`${inFlight.length} ULDs airborne on ${airborne.length} flights`} icon={Plane}>
        <div className="space-y-3">
          {airborne
            .slice()
            .sort((a, b) => (a.eta ?? "").localeCompare(b.eta ?? ""))
            .map((f) => {
              const list = uldsByFlight[f.id] ?? [];
              const t = progress(f.departed_at, f.eta, now);
              const weight = list.reduce((s, u) => s + u.weight_kg, 0);
              return (
                <div key={f.id} className="rounded-lg border border-line bg-surface p-3 shadow-xs">
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="font-mono text-sm font-semibold text-ink">
                      {f.flight_number} <span className="font-sans text-xs font-normal text-muted">{f.origin} → {f.destination}</span>
                    </span>
                    <span className="text-xs tabular-nums text-accent-ink">ETA {f.eta ? timeHM(f.eta) : "—"} · {countdown(f.eta, now)}</span>
                  </div>
                  <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-surface-2">
                    <div className="h-full rounded-full bg-accent transition-[width] duration-1000" style={{ width: `${t * 100}%` }} />
                  </div>
                  <div className="mt-1 flex justify-between text-[11px] text-muted">
                    <span>{f.aircraft_type}</span>
                    <span className="tabular-nums">
                      {list.length} ULDs · {kg(weight)}
                    </span>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {list.map((u) => (
                      <span key={u.id} className="rounded-md bg-violet-50 px-1.5 py-0.5 font-mono text-[10px] text-violet-700 ring-1 ring-inset ring-violet-200 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-400/30" title={kg(u.weight_kg)}>
                        {u.id}
                      </span>
                    ))}
                    {list.length === 0 && <span className="text-[11px] text-muted">Ferry flight (no ULDs on board)</span>}
                  </div>
                </div>
              );
            })}
          {airborne.length === 0 && (
            <div className="rounded-lg border border-dashed border-line px-4 py-8 text-center text-sm text-muted">
              No flights airborne right now. Departures are listed under Load Optimization.
            </div>
          )}
        </div>
      </Panel>
    </div>
  );
}
