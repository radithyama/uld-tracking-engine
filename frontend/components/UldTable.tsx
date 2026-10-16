"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type { ULD, ULDStatus } from "@/lib/types";
import { ago, kg, useNow } from "@/lib/format";
import { StatusBadge, WeightBar } from "./StatusBadge";

type SortKey = "id" | "flight_number" | "status" | "location_id" | "weight_kg" | "load" | "last_scan_at";

interface Props {
  ulds: ULD[];
  flights: string[];
  selected: string | null;
  onSelect: (id: string | null) => void;
}

const columns: { key: SortKey; label: string; className?: string }[] = [
  { key: "id", label: "ULD" },
  { key: "flight_number", label: "Flight" },
  { key: "status", label: "Status" },
  { key: "location_id", label: "Location" },
  { key: "weight_kg", label: "Weight", className: "text-right" },
  { key: "load", label: "Load vs limit", className: "w-40" },
  { key: "last_scan_at", label: "Last scan" },
];

function sortValue(u: ULD, key: SortKey): string | number {
  switch (key) {
    case "load":
      return u.weight_kg / (u.weight_limit_kg || 1);
    case "last_scan_at":
      return u.last_scan_at ? Date.parse(u.last_scan_at) : 0;
    default:
      return u[key];
  }
}

export function UldTable({ ulds, flights, selected, onSelect }: Props) {
  const now = useNow();
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<ULDStatus | "">("");
  const [flight, setFlight] = useState("");
  const [alertsOnly, setAlertsOnly] = useState(false);
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "load", dir: -1 });

  const rows = useMemo(() => {
    const q = query.trim().toUpperCase();
    return ulds
      .filter(
        (u) =>
          (!q || u.id.includes(q) || u.location_id.includes(q)) &&
          (!status || u.status === status) &&
          (!flight || u.flight_number === flight) &&
          (!alertsOnly || u.overweight),
      )
      .sort((a, b) => {
        const av = sortValue(a, sort.key);
        const bv = sortValue(b, sort.key);
        return (av < bv ? -1 : av > bv ? 1 : a.id.localeCompare(b.id)) * sort.dir;
      });
  }, [ulds, query, status, flight, alertsOnly, sort]);

  // Bring the selected ULD (e.g. clicked on the map) into view.
  const selectedRef = useRef<HTMLTableRowElement>(null);
  useEffect(() => {
    selectedRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [selected]);

  const toggleSort = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, dir: s.dir === 1 ? -1 : 1 } : { key, dir: 1 }));

  const input = "rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-sm text-slate-200 focus:border-sky-500 focus:outline-none";

  return (
    <div className="flex min-h-0 flex-col">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <input
          className={`${input} w-48`}
          placeholder="Search ULD or location…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <select className={input} value={status} onChange={(e) => setStatus(e.target.value as ULDStatus | "")}>
          <option value="">All statuses</option>
          <option value="IN_TRANSIT">In Transit</option>
          <option value="STAGED">Staged</option>
          <option value="LOADED">Loaded</option>
        </select>
        <select className={input} value={flight} onChange={(e) => setFlight(e.target.value)}>
          <option value="">All flights</option>
          {flights.map((f) => (
            <option key={f} value={f}>
              {f}
            </option>
          ))}
        </select>
        <label className="flex cursor-pointer items-center gap-1.5 text-sm text-slate-300">
          <input type="checkbox" checked={alertsOnly} onChange={(e) => setAlertsOnly(e.target.checked)} className="accent-red-500" />
          Overweight only
        </label>
        <span className="ml-auto text-xs text-slate-500">
          {rows.length} of {ulds.length} ULDs
        </span>
      </div>

      <div className="max-h-[520px] overflow-auto rounded-lg border border-slate-800">
        <table className="w-full min-w-[760px] text-sm">
          <thead className="sticky top-0 z-10 bg-slate-900/95 backdrop-blur">
            <tr className="text-left text-xs uppercase tracking-wider text-slate-500">
              {columns.map((c) => (
                <th key={c.key} className={`px-3 py-2 font-medium ${c.className ?? ""}`}>
                  <button type="button" onClick={() => toggleSort(c.key)} className="hover:text-slate-300">
                    {c.label}
                    {sort.key === c.key && <span className="ml-1">{sort.dir === 1 ? "▲" : "▼"}</span>}
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/80">
            {rows.map((u) => {
              const isSel = selected === u.id;
              return (
                <tr
                  key={u.id}
                  ref={isSel ? selectedRef : undefined}
                  onClick={() => onSelect(isSel ? null : u.id)}
                  className={`cursor-pointer transition-colors ${
                    u.overweight ? "bg-red-500/10 hover:bg-red-500/15" : "hover:bg-slate-800/50"
                  } ${isSel ? "outline outline-1 -outline-offset-1 outline-sky-400" : ""}`}
                >
                  <td className="px-3 py-2 font-mono text-slate-100">
                    <span className="flex items-center gap-2">
                      {u.overweight && (
                        <span className="text-red-400" title="Exceeds weight limit">
                          ⚠
                        </span>
                      )}
                      {u.id}
                      <span className="text-[10px] text-slate-500">{u.uld_type}</span>
                    </span>
                  </td>
                  <td className="px-3 py-2 font-mono text-slate-300">{u.flight_number || "—"}</td>
                  <td className="px-3 py-2">
                    <StatusBadge status={u.status} />
                  </td>
                  <td className="px-3 py-2 font-mono text-slate-400">{u.location_id || "—"}</td>
                  {/* key on version replays the flash when the value changes */}
                  <td
                    key={u.version}
                    className={`flash px-3 py-2 text-right font-mono tabular-nums ${
                      u.overweight ? "font-semibold text-red-300" : "text-slate-200"
                    }`}
                  >
                    {kg(u.weight_kg)}
                  </td>
                  <td className="px-3 py-2">
                    <WeightBar weight={u.weight_kg} limit={u.weight_limit_kg} />
                    <div className="mt-0.5 text-[10px] text-slate-500">limit {kg(u.weight_limit_kg)}</div>
                  </td>
                  <td className="px-3 py-2 text-xs text-slate-400" title={u.last_scanner_id}>
                    {ago(u.last_scan_at, now)}
                  </td>
                </tr>
              );
            })}
            {rows.length === 0 && (
              <tr>
                <td colSpan={columns.length} className="px-3 py-10 text-center text-slate-500">
                  {ulds.length === 0 ? "Waiting for telemetry…" : "No ULDs match the current filters."}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
