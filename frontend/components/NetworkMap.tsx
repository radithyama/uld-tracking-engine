"use client";

import { useEffect, useMemo, useState } from "react";
import { geoInterpolate } from "d3-geo";
import type { GeoJsonObject } from "geojson";
import { ComposableMap, Geographies, Geography, Graticule, Line, Marker, ZoomableGroup, useMapContext } from "react-simple-maps";
import { Globe2, Minus, Plane, Plus, RotateCcw } from "lucide-react";
import type { FlightLoad, Station, ULD } from "@/lib/types";
import { lonLat } from "@/lib/airports";
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

type LonLat = [number, number];

// Mercator framed on the network: Narita in the north to Melbourne in the
// south, Jakarta/Singapore in the west to Sydney in the east.
const MAP_W = 760;
const MAP_H = 600;
const HOME: { center: LonLat; zoom: number } = { center: [128, -1], zoom: 1 };
const PROJECTION = { center: HOME.center, scale: 390 };
const MIN_ZOOM = 1;
const MAX_ZOOM = 8;

// No focus outline: countries are scenery, not controls.
const GEO_STYLE = { outline: "none" };

/** Countries TopoJSON (Natural Earth 1:50m), loaded as its own cached chunk. */
function useWorld() {
  const [world, setWorld] = useState<GeoJsonObject | null>(null);
  useEffect(() => {
    let alive = true;
    import("world-atlas/countries-50m.json").then((m) => alive && setWorld(m.default as unknown as GeoJsonObject));
    return () => {
      alive = false;
    };
  }, []);
  return world;
}

/** A flight on the map: great-circle trail, plane icon and label. */
function FlightOnMap({ flight, from, to, t, now, scale, stagger }: {
  flight: FlightLoad;
  from: LonLat;
  to: LonLat;
  t: number;
  now: number;
  scale: number; // 1/zoom, keeps icons and labels a constant on-screen size
  stagger: number;
}) {
  const { projection } = useMapContext();
  const along = useMemo(() => geoInterpolate(from, to), [from, to]);
  const pos = along(t) as LonLat;

  // Heading on screen: direction from here to a point slightly further along
  // the great circle, after projection (so it matches the drawn path).
  const a = projection(along(Math.max(0, Math.min(t, 0.995)))) ?? [0, 0];
  const b = projection(along(Math.min(1, Math.max(t, 0) + 0.005))) ?? [0, 0];
  // Rounded: float math differs in the last digits between engines (server
  // vs browser), and 0.1° is far below what's visible.
  const heading = Math.round((Math.atan2(b[1] - a[1], b[0] - a[0]) * 1800) / Math.PI) / 10;

  // Label beside the plane, perpendicular to its track (to the left of the
  // direction of travel). That keeps it off the route line and, near either
  // end, away from the airport's own label. Same-origin departures are
  // staggered further out so they don't overprint.
  const normal = ((heading - 90) * Math.PI) / 180;
  const dist = 22 + stagger * 16;
  const lx = Math.round(Math.cos(normal) * dist);
  const ly = Math.round(Math.sin(normal) * dist);
  const LABEL_W = 94;
  const left = lx < 0;

  return (
    <g>
      <Line from={from} to={to} fill="none" className="stroke-blue-400/60 dark:stroke-blue-400/30" strokeWidth={1.5 * scale} strokeDasharray={`${5 * scale} ${4 * scale}`} />
      {t > 0 && <Line from={from} to={pos} fill="none" className="stroke-accent" strokeWidth={2.5 * scale} strokeLinecap="round" />}
      <Marker coordinates={pos}>
        <g transform={`scale(${scale})`}>
          <title>
            {`${flight.flight_number} ${flight.origin} → ${flight.destination}\n${Math.round(t * 100)}% flown · ${pos[1].toFixed(2)}°, ${pos[0].toFixed(2)}°\nETA ${flight.eta ? timeHM(flight.eta) : "—"}`}
          </title>
          <g transform={`rotate(${heading})`}>
            <circle r={10} className="fill-surface stroke-accent" strokeWidth={1.5} />
            <path d="M 6.5 0 L -4.5 -4.5 L -2 0 L -4.5 4.5 Z" className="fill-accent" />
          </g>
          <g transform={`translate(${left ? lx - LABEL_W : lx} ${ly + 4})`}>
            <rect x={-3} y={-10} width={LABEL_W} height={14} rx={4} className="fill-surface/95 stroke-line" />
            <text x={2} y={0} className="fill-accent-ink text-[10px] font-semibold">
              {flight.flight_number} · {countdown(flight.eta, now)}
            </text>
          </g>
        </g>
      </Marker>
    </g>
  );
}

export function NetworkMap({ stations, flights, ulds, station, timeScale, onStation }: Props) {
  const now = useNow(1000);
  const world = useWorld();
  const [view, setView] = useState(HOME);
  const [liveZoom, setLiveZoom] = useState(HOME.zoom); // updates during gestures
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

  const s = 1 / liveZoom;
  const zoomTo = (zoom: number) => {
    const z = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom));
    setView((v) => ({ ...v, zoom: z }));
    setLiveZoom(z);
  };

  return (
    <div className="grid gap-5 xl:grid-cols-[1fr_400px]">
      <Panel title="Flight network" subtitle={`Live flights and ULDs in the air · network clock ×${timeScale} · scroll or pinch to zoom, drag to pan`} icon={Globe2}>
        <div className="relative overflow-hidden rounded-lg border border-line bg-slate-200/80 dark:bg-slate-950">
          <ComposableMap
            projection="geoMercator"
            projectionConfig={PROJECTION}
            width={MAP_W}
            height={MAP_H}
            className="block h-auto w-full touch-none select-none"
            role="img"
            aria-label="Flight network map"
          >
            <ZoomableGroup
              center={view.center}
              zoom={view.zoom}
              minZoom={MIN_ZOOM}
              maxZoom={MAX_ZOOM}
              translateExtent={[
                [-MAP_W * 0.5, -MAP_H * 0.5],
                [MAP_W * 1.5, MAP_H * 1.5],
              ]}
              onMove={({ zoom }) => zoom && setLiveZoom(zoom)}
              onMoveEnd={({ coordinates, zoom }) => {
                if (coordinates && zoom) setView({ center: coordinates, zoom });
              }}
            >
              <Graticule step={[10, 10]} fill="none" className="stroke-slate-300/70 dark:stroke-slate-800" strokeWidth={0.6 * s} />
              {world && (
                <Geographies geography={world}>
                  {({ geographies }) =>
                    geographies.map((geo) => (
                      <Geography
                        key={geo.rsmKey}
                        geography={geo}
                        style={GEO_STYLE}
                        className="fill-white stroke-slate-300 dark:fill-slate-800 dark:stroke-slate-700"
                        strokeWidth={0.5 * s}
                      />
                    ))
                  }
                </Geographies>
              )}

              {/* Scheduled routes as great-circle arcs. */}
              {routes.map((f) => {
                const a = byIata[f.origin], b = byIata[f.destination];
                if (!a || !b) return null;
                return (
                  <Line
                    key={`r${f.id}`}
                    from={lonLat(a)}
                    to={lonLat(b)}
                    fill="none"
                    className="stroke-slate-400/80 dark:stroke-slate-600"
                    strokeWidth={1.2 * s}
                    strokeDasharray={`${4 * s} ${5 * s}`}
                  />
                );
              })}

              {airborne.map((f, i) => {
                const a = byIata[f.origin], b = byIata[f.destination];
                if (!a || !b) return null;
                return (
                  <FlightOnMap
                    key={`f${f.id}`}
                    flight={f}
                    from={lonLat(a)}
                    to={lonLat(b)}
                    t={progress(f.departed_at, f.eta, now)}
                    now={now}
                    scale={s}
                    stagger={i % 3}
                  />
                );
              })}

              {stations.map((st) => {
                const active = st.iata === station;
                const [lon] = lonLat(st);
                const flip = lon > 145; // eastern airports: label on the left, toward the map
                const lx = flip ? -13 : 13;
                const anchor = flip ? "end" : "start";
                return (
                  <Marker key={st.iata} coordinates={lonLat(st)} onClick={() => onStation(st.iata)} className="cursor-pointer" aria-label={`Select ${st.iata}`}>
                    <g transform={`scale(${s})`}>
                      <title>{`${st.name} (${st.iata}) · ${atStation[st.iata] ?? 0} ULDs`}</title>
                      {active && <circle r={16} className="fill-accent/10 stroke-accent/40" />}
                      <circle r={active ? 7 : 5.5} className={active ? "fill-accent" : "fill-slate-500 dark:fill-slate-400"} strokeWidth={2} stroke="white" />
                      <text x={lx} y={4} textAnchor={anchor} className={`text-[12px] font-bold ${active ? "fill-accent-ink" : "fill-ink"}`} paintOrder="stroke" strokeWidth={3} strokeLinejoin="round" stroke="var(--surface)">
                        {st.iata}
                      </text>
                      <text x={lx} y={16} textAnchor={anchor} className="fill-muted text-[9px]" paintOrder="stroke" strokeWidth={3} strokeLinejoin="round" stroke="var(--surface)">
                        {atStation[st.iata] ?? 0} ULDs
                      </text>
                    </g>
                  </Marker>
                );
              })}
            </ZoomableGroup>
          </ComposableMap>

          {!world && (
            <div className="pointer-events-none absolute inset-0 grid place-items-center text-xs text-muted">Loading map…</div>
          )}

          <div className="absolute right-2 top-2 flex flex-col overflow-hidden rounded-lg border border-line bg-surface/95 shadow-sm">
            <button type="button" onClick={() => zoomTo(view.zoom * 1.6)} className="grid h-8 w-8 place-items-center text-ink-2 hover:bg-surface-2 hover:text-ink" aria-label="Zoom in">
              <Plus size={15} />
            </button>
            <button type="button" onClick={() => zoomTo(view.zoom / 1.6)} className="grid h-8 w-8 place-items-center border-t border-line text-ink-2 hover:bg-surface-2 hover:text-ink" aria-label="Zoom out">
              <Minus size={15} />
            </button>
            <button
              type="button"
              onClick={() => {
                setView(HOME);
                setLiveZoom(HOME.zoom);
              }}
              className="grid h-8 w-8 place-items-center border-t border-line text-ink-2 hover:bg-surface-2 hover:text-ink"
              aria-label="Reset view"
            >
              <RotateCcw size={14} />
            </button>
          </div>
          <div className="pointer-events-none absolute bottom-1 right-2 text-[9px] text-muted/80">Natural Earth</div>
        </div>
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
