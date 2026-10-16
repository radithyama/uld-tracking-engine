"use client";

import { signOut } from "next-auth/react";
import type { ConnectionStatus, Station } from "@/lib/types";
import { ROLE_INFO, type Role, type Tab } from "@/lib/roles";

const TAB_LABEL: Record<Tab, string> = {
  operations: "Operations",
  flights: "Flights & Load",
  network: "Network",
  security: "Security",
};

const ROLE_CHIP: Record<Role, string> = {
  ground_handler: "bg-sky-500/15 text-sky-300",
  loadmaster: "bg-amber-500/15 text-amber-300",
  security_auditor: "bg-red-500/15 text-red-300",
};

const CONN: Record<ConnectionStatus, { label: string; dot: string }> = {
  open: { label: "Live", dot: "bg-emerald-400 shadow-[0_0_8px] shadow-emerald-400" },
  connecting: { label: "Connecting…", dot: "bg-slate-400 animate-pulse" },
  reconnecting: { label: "Reconnecting…", dot: "bg-amber-400 animate-pulse" },
};

interface Props {
  stations: Station[];
  station: string;
  onStation: (s: string) => void;
  tabs: Tab[];
  tab: Tab;
  onTab: (t: Tab) => void;
  role: Role;
  name: string;
  status: ConnectionStatus;
  rate: number;
  openSecurity: number;
}

export function TopBar({ stations, station, onStation, tabs, tab, onTab, role, name, status, rate, openSecurity }: Props) {
  const conn = CONN[status];
  return (
    <header className="sticky top-0 z-30 border-b border-slate-800 bg-slate-950/90 backdrop-blur">
      <div className="mx-auto flex max-w-[1600px] flex-wrap items-center gap-x-5 gap-y-2 px-4 py-3 sm:px-6">
        <div>
          <div className="text-[10px] font-semibold uppercase tracking-[0.25em] text-sky-400">ULDPulse</div>
          <div className="text-sm font-semibold text-slate-100">Operations Control</div>
        </div>

        <label className="flex items-center gap-2 text-xs text-slate-400">
          Hub
          <select
            value={station}
            onChange={(e) => onStation(e.target.value)}
            className="rounded-md border border-slate-700 bg-slate-900 px-2 py-1.5 font-mono text-sm text-slate-100 focus:border-sky-500 focus:outline-none"
          >
            {stations.map((s) => (
              <option key={s.iata} value={s.iata}>
                {s.iata} · {s.name.replace(/ International$/, "")}
              </option>
            ))}
          </select>
        </label>

        <nav className="flex gap-1" aria-label="Views">
          {tabs.map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => onTab(t)}
              aria-current={tab === t ? "page" : undefined}
              className={`relative rounded-md px-3 py-1.5 text-sm transition ${tab === t ? "bg-slate-800 text-slate-100" : "text-slate-400 hover:text-slate-200"}`}
            >
              {TAB_LABEL[t]}
              {t === "security" && openSecurity > 0 && (
                <span className="ml-1.5 rounded-full bg-red-500 px-1.5 text-[10px] font-bold text-white">{openSecurity}</span>
              )}
            </button>
          ))}
        </nav>

        <div className="ml-auto flex items-center gap-3 text-xs text-slate-400">
          <span className="tabular-nums">{rate} msg/s</span>
          <span className="flex items-center gap-2 rounded-full border border-slate-800 px-3 py-1">
            <span className={`h-2 w-2 rounded-full ${conn.dot}`} />
            {conn.label}
          </span>
          <span className="hidden text-slate-300 sm:inline">{name}</span>
          <span className={`rounded px-2 py-0.5 text-[11px] font-medium ${ROLE_CHIP[role]}`}>{ROLE_INFO[role].label}</span>
          <button type="button" onClick={() => signOut({ redirectTo: "/signin" })} className="text-slate-500 hover:text-slate-200">
            Sign out
          </button>
        </div>
      </div>
    </header>
  );
}
