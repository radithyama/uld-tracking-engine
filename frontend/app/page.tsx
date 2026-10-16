"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useUldStream } from "@/lib/useUldStream";
import type { ConnectionStatus } from "@/lib/types";
import { WarehouseGrid } from "@/components/WarehouseGrid";
import { UldTable } from "@/components/UldTable";
import { FlightLoads } from "@/components/FlightLoads";
import { AlertsPanel } from "@/components/AlertsPanel";
import { tonnes } from "@/lib/format";

const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8081/ws";

const connectionStyle: Record<ConnectionStatus, { label: string; dot: string }> = {
  open: { label: "Live", dot: "bg-emerald-400 shadow-[0_0_8px] shadow-emerald-400" },
  connecting: { label: "Connecting…", dot: "bg-slate-400 animate-pulse" },
  reconnecting: { label: "Reconnecting…", dot: "bg-amber-400 animate-pulse" },
};

/** Messages/second, sampled once a second from the running total. */
function useRate(total: number): number {
  const totalRef = useRef(total);
  const [rate, setRate] = useState(0);
  useEffect(() => {
    totalRef.current = total;
  }, [total]);
  useEffect(() => {
    let prev = totalRef.current;
    const t = setInterval(() => {
      setRate(totalRef.current - prev);
      prev = totalRef.current;
    }, 1000);
    return () => clearInterval(t);
  }, []);
  return rate;
}

export default function Dashboard() {
  const stream = useUldStream(WS_URL);
  const [selected, setSelected] = useState<string | null>(null);
  const rate = useRate(stream.messageCount);

  const ulds = useMemo(() => Object.values(stream.ulds), [stream.ulds]);
  const flights = useMemo(
    () => Object.values(stream.flights).sort((a, b) => a.departure.localeCompare(b.departure)),
    [stream.flights],
  );
  const overweight = useMemo(
    () => ulds.filter((u) => u.overweight).sort((a, b) => b.weight_kg / b.weight_limit_kg - a.weight_kg / a.weight_limit_kg),
    [ulds],
  );
  const overPayload = useMemo(() => flights.filter((f) => f.over_payload), [flights]);

  const counts = useMemo(() => {
    const c = { IN_TRANSIT: 0, STAGED: 0, LOADED: 0, weight: 0 };
    for (const u of ulds) {
      c[u.status]++;
      c.weight += u.weight_kg;
    }
    return c;
  }, [ulds]);

  const conn = connectionStyle[stream.status];
  const alertCount = overweight.length + overPayload.length;

  return (
    <main className="mx-auto w-full max-w-[1600px] flex-1 px-4 py-5 sm:px-6">
      {/* Header */}
      <header className="mb-5 flex flex-wrap items-center gap-x-6 gap-y-3">
        <div>
          <h1 className="text-lg font-semibold tracking-tight text-slate-100">ULD Operations Control</h1>
          <p className="text-xs text-slate-500">Real-time unit load device tracking · CGK Cargo Terminal</p>
        </div>
        <div className="ml-auto flex items-center gap-4 text-xs text-slate-400">
          <span className="tabular-nums">{rate} msg/s</span>
          <span className="flex items-center gap-2 rounded-full border border-slate-800 px-3 py-1">
            <span className={`h-2 w-2 rounded-full ${conn.dot}`} />
            {conn.label}
          </span>
        </div>
      </header>

      {/* Alert banner */}
      {alertCount > 0 && (
        <div className="mb-5 flex items-center gap-3 rounded-lg border border-red-500/60 bg-red-500/15 px-4 py-2.5 text-sm text-red-100">
          <span className="relative flex h-2.5 w-2.5">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-red-400 opacity-75" />
            <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-red-500" />
          </span>
          <strong>{alertCount} weight alert{alertCount === 1 ? "" : "s"}:</strong>
          <span className="text-red-200/90">
            {overweight.length} ULD{overweight.length === 1 ? "" : "s"} over limit
            {overPayload.length > 0 && `, ${overPayload.length} flight${overPayload.length === 1 ? "" : "s"} over max payload`}
          </span>
        </div>
      )}

      {/* KPIs */}
      <section className="mb-5 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <Kpi label="Tracked ULDs" value={ulds.length} />
        <Kpi label="In Transit" value={counts.IN_TRANSIT} accent="text-sky-300" />
        <Kpi label="Staged" value={counts.STAGED} accent="text-amber-300" />
        <Kpi label="Loaded" value={counts.LOADED} accent="text-emerald-300" />
        <Kpi label="Gross weight on floor" value={tonnes(counts.weight)} />
      </section>

      <div className="grid gap-5 xl:grid-cols-[1fr_360px]">
        <div className="min-w-0 space-y-5">
          <Panel title="Warehouse floor" subtitle="Live ULD positions by scan location">
            <WarehouseGrid locations={stream.locations} ulds={ulds} selected={selected} onSelect={setSelected} />
          </Panel>
          <Panel title="ULD manifest" subtitle="Click a row or map chip to highlight">
            <UldTable
              ulds={ulds}
              flights={flights.map((f) => f.flight_number)}
              selected={selected}
              onSelect={setSelected}
            />
          </Panel>
        </div>
        <aside className="space-y-5">
          <Panel title="Weight alerts">
            <AlertsPanel overweight={overweight} overPayload={overPayload} onSelect={setSelected} />
          </Panel>
          <Panel title="Flight load" subtitle="Assigned ULD weight vs aircraft max payload">
            <FlightLoads flights={flights} />
          </Panel>
        </aside>
      </div>
    </main>
  );
}

function Kpi({ label, value, accent = "text-slate-100" }: { label: string; value: number | string; accent?: string }) {
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900/60 px-4 py-3">
      <div className="text-[11px] uppercase tracking-wider text-slate-500">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${accent}`}>{value}</div>
    </div>
  );
}

function Panel({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-xl border border-slate-800 bg-slate-900/40 p-4">
      <div className="mb-3">
        <h2 className="text-sm font-semibold text-slate-200">{title}</h2>
        {subtitle && <p className="text-xs text-slate-500">{subtitle}</p>}
      </div>
      {children}
    </section>
  );
}
