"use client";

import { useEffect, useRef, useState } from "react";
import { signIn, signOut } from "next-auth/react";
import { BookOpen, Check, Lock, LogOut, MapPin, X } from "lucide-react";
import type { Station, ULD } from "@/lib/types";
import { ROLE_INFO, ROLES, type Role, type Tab } from "@/lib/roles";
import { VIEWS, VIEW_ORDER } from "@/lib/nav";
import { Logo, ROLE_CHIP, ThemeToggle } from "./Navbar";

interface Props {
  open: boolean;
  onClose: () => void;
  stations: Station[];
  station: string;
  onStation: (s: string) => void;
  ulds: ULD[];
  tabs: Tab[];
  tab: Tab;
  onTab: (t: Tab) => void;
  role: Role;
  name: string;
  demoSession: boolean;
  onGuide: () => void;
}

/** Which persona unlocks a view (for the locked-view hint). */
function unlockedBy(t: Tab): string {
  return ROLES.filter((r) => ROLE_INFO[r].tabs.includes(t)).map((r) => ROLE_INFO[r].label).join(" / ");
}

export function NavDrawer({ open, onClose, stations, station, onStation, ulds, tabs, tab, onTab, role, name, demoSession, onGuide }: Props) {
  const closeRef = useRef<HTMLButtonElement>(null);
  const [switching, setSwitching] = useState<Role | null>(null);

  useEffect(() => {
    if (!open) return;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [open, onClose]);

  if (!open) return null;

  const counts: Record<string, number> = {};
  for (const u of ulds) if (u.station) counts[u.station] = (counts[u.station] ?? 0) + 1;

  const section = "px-3 pb-1.5 pt-4 text-[11px] font-semibold uppercase tracking-wider text-muted";

  return (
    <div className="fixed inset-0 z-50" role="dialog" aria-modal="true" aria-label="Navigation">
      <div className="fade-in absolute inset-0 bg-slate-900/30 backdrop-blur-[2px] dark:bg-black/50" onClick={onClose} />
      <aside className="slide-in-left absolute inset-y-0 left-0 flex w-[19rem] max-w-[88vw] flex-col border-r border-line bg-surface shadow-xl">
        <div className="flex h-14 items-center justify-between border-b border-line px-4">
          <Logo />
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            className="grid h-9 w-9 place-items-center rounded-lg text-muted hover:bg-surface-2 hover:text-ink"
            aria-label="Close navigation"
          >
            <X size={18} />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto px-2 pb-4">
          <div className={section}>Hub</div>
          <ul className="space-y-0.5">
            {stations.map((s) => {
              const active = s.iata === station;
              return (
                <li key={s.iata}>
                  <button
                    type="button"
                    onClick={() => {
                      onStation(s.iata);
                      onClose();
                    }}
                    className={`flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left transition ${active ? "bg-accent-soft" : "hover:bg-surface-2"}`}
                  >
                    <MapPin size={15} className={active ? "text-accent" : "text-muted"} />
                    <span className="min-w-0 flex-1">
                      <span className={`font-mono text-sm font-semibold ${active ? "text-accent-ink" : "text-ink"}`}>{s.iata}</span>
                      <span className="ml-2 truncate text-xs text-muted">{s.name.replace(/ International$/, "")}</span>
                    </span>
                    <span className="rounded-full bg-surface-2 px-2 py-0.5 text-[11px] tabular-nums text-ink-2">{counts[s.iata] ?? 0}</span>
                  </button>
                </li>
              );
            })}
          </ul>

          <div className={section}>Views</div>
          <ul className="space-y-0.5">
            {VIEW_ORDER.map((t) => {
              const V = VIEWS[t];
              const allowed = tabs.includes(t);
              const active = tab === t;
              return (
                <li key={t}>
                  <button
                    type="button"
                    disabled={!allowed}
                    onClick={() => {
                      onTab(t);
                      onClose();
                    }}
                    className={`flex w-full items-start gap-3 rounded-lg px-3 py-2 text-left transition ${
                      active ? "bg-accent-soft" : allowed ? "hover:bg-surface-2" : "cursor-not-allowed opacity-55"
                    }`}
                    title={allowed ? V.blurb : `Available to ${unlockedBy(t)}`}
                  >
                    <V.icon size={16} className={`mt-0.5 ${active ? "text-accent" : "text-muted"}`} />
                    <span className="min-w-0 flex-1">
                      <span className={`block text-sm font-medium ${active ? "text-accent-ink" : "text-ink"}`}>{V.label}</span>
                      <span className="block text-xs text-muted">{allowed ? V.blurb : `Requires ${unlockedBy(t)}`}</span>
                    </span>
                    {!allowed && <Lock size={13} className="mt-1 text-muted" />}
                  </button>
                </li>
              );
            })}
          </ul>

          <div className={section}>Persona</div>
          <div className="mx-1 rounded-lg border border-line p-3">
            <div className="text-sm font-medium text-ink">{name}</div>
            <span className={`mt-1 inline-block rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset ${ROLE_CHIP[role]}`}>
              {ROLE_INFO[role].label}
            </span>
            {demoSession ? (
              <ul className="mt-3 space-y-1">
                {ROLES.map((r) => (
                  <li key={r}>
                    <button
                      type="button"
                      disabled={r === role || switching !== null}
                      onClick={() => {
                        setSwitching(r);
                        void signIn("demo", { role: r, redirectTo: "/" });
                      }}
                      className="flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-xs transition hover:bg-surface-2 disabled:cursor-default disabled:hover:bg-transparent"
                    >
                      <span className={r === role ? "font-medium text-ink" : "text-ink-2"}>{ROLE_INFO[r].label}</span>
                      {r === role ? (
                        <Check size={13} className="text-accent" />
                      ) : (
                        <span className="text-accent">{switching === r ? "Switching…" : "Switch"}</span>
                      )}
                    </button>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mt-2 text-xs text-muted">Your role is assigned by your organisation.</p>
            )}
          </div>
        </div>

        <div className="flex items-center gap-2 border-t border-line p-3">
          <button
            type="button"
            onClick={() => {
              onClose();
              onGuide();
            }}
            className="btn-ghost flex-1"
          >
            <BookOpen size={15} /> How it works
          </button>
          <ThemeToggle />
          <button
            type="button"
            onClick={() => signOut({ redirectTo: "/signin" })}
            className="grid h-9 w-9 place-items-center rounded-lg text-muted hover:bg-surface-2 hover:text-ink"
            aria-label="Sign out"
            title="Sign out"
          >
            <LogOut size={17} />
          </button>
        </div>
      </aside>
    </div>
  );
}
