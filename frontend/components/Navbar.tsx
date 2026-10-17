"use client";

import Image from "next/image";
import { BookOpen, Menu, Moon, Sun } from "lucide-react";
import type { ConnectionStatus, Station } from "@/lib/types";
import { ROLE_INFO, type Role, type Tab } from "@/lib/roles";
import { REPO_URL, VIEWS, VIEW_ORDER } from "@/lib/nav";
import { useTheme } from "@/lib/theme";

export const ROLE_CHIP: Record<Role, string> = {
  ground_handler: "bg-sky-50 text-sky-700 ring-sky-200 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30",
  loadmaster: "bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/30",
  security_auditor: "bg-rose-50 text-rose-700 ring-rose-200 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-400/30",
};

const CONN: Record<ConnectionStatus, { label: string; cls: string; dot: string }> = {
  open: {
    label: "Live",
    cls: "bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
    dot: "bg-emerald-500",
  },
  connecting: { label: "Connecting", cls: "bg-surface-2 text-muted ring-line", dot: "bg-slate-400 animate-pulse" },
  reconnecting: {
    label: "Reconnecting",
    cls: "bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/30",
    dot: "bg-amber-500 animate-pulse",
  },
};

export function LiveBadge({ status, rate }: { status: ConnectionStatus; rate: number }) {
  const c = CONN[status];
  return (
    <span className={`inline-flex items-center gap-2 whitespace-nowrap rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset ${c.cls}`}>
      <span className="relative flex h-2 w-2">
        {status === "open" && <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-60" />}
        <span className={`relative inline-flex h-2 w-2 rounded-full ${c.dot}`} />
      </span>
      {c.label}
      <span className="font-normal tabular-nums opacity-80">{rate} msg/s</span>
    </span>
  );
}

// lucide no longer ships brand icons.
function GithubMark() {
  return (
    <svg viewBox="0 0 16 16" width="17" height="17" fill="currentColor" aria-hidden="true">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

export function ThemeToggle() {
  const [theme, setTheme] = useTheme();
  const dark = theme === "dark";
  return (
    <button
      type="button"
      onClick={() => setTheme(dark ? "light" : "dark")}
      className="grid h-9 w-9 place-items-center rounded-lg text-muted transition hover:bg-surface-2 hover:text-ink"
      aria-label={dark ? "Switch to light theme" : "Switch to dark theme"}
      title={dark ? "Light theme" : "Dark theme"}
    >
      {dark ? <Sun size={17} /> : <Moon size={17} />}
    </button>
  );
}

/** The ULDPulse brand mark. The navy parts sit on a white tile in dark mode to stay legible. */
export function BrandMark({ size = 32, className = "" }: { size?: number; className?: string }) {
  return (
    <span
      className={`grid shrink-0 place-items-center rounded-lg dark:bg-white dark:shadow-sm dark:ring-1 dark:ring-white/20 ${className}`}
      style={{ width: size, height: size }}
    >
      <Image src="/brand/uldpulse-mark.png" alt="" width={size} height={size} className="h-[88%] w-[88%] object-contain" priority />
    </span>
  );
}

export function Logo() {
  return (
    <div className="flex shrink-0 items-center gap-2">
      <BrandMark size={34} />
      <div className="whitespace-nowrap leading-tight">
        <div className="text-sm font-semibold tracking-tight text-ink">ULDPulse</div>
        <div className="text-[10px] font-medium uppercase tracking-wider text-muted">Cargo Ops Control</div>
      </div>
    </div>
  );
}

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
  onMenu: () => void;
  onGuide: () => void;
}

export function Navbar({ stations, station, onStation, tabs, tab, onTab, role, name, status, rate, openSecurity, onMenu, onGuide }: Props) {
  const initials = name.replace(/^Demo /, "").split(" ").map((w) => w[0]).join("").slice(0, 2).toUpperCase();
  return (
    <header className="sticky top-0 z-30 border-b border-line bg-surface/85 backdrop-blur supports-[backdrop-filter]:bg-surface/75">
      <div className="mx-auto flex h-14 max-w-[1600px] items-center gap-3 px-3 sm:px-5">
        <button
          type="button"
          onClick={onMenu}
          className="grid h-9 w-9 place-items-center rounded-lg text-ink-2 transition hover:bg-surface-2 hover:text-ink"
          aria-label="Open navigation"
        >
          <Menu size={19} />
        </button>
        <Logo />

        <nav className="ml-4 hidden items-center gap-0.5 lg:flex" aria-label="Views">
          {VIEW_ORDER.filter((t) => tabs.includes(t)).map((t) => {
            const V = VIEWS[t];
            const active = tab === t;
            return (
              <button
                key={t}
                type="button"
                onClick={() => onTab(t)}
                aria-current={active ? "page" : undefined}
                className={`relative inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition ${
                  active ? "bg-accent-soft text-accent-ink" : "text-muted hover:bg-surface-2 hover:text-ink"
                }`}
              >
                <V.icon size={15} />
                {V.short}
                {t === "security" && openSecurity > 0 && (
                  <span className="ml-0.5 rounded-full bg-rose-600 px-1.5 text-[10px] font-semibold leading-4 text-white">{openSecurity}</span>
                )}
              </button>
            );
          })}
        </nav>

        <div className="ml-auto flex items-center gap-1.5 sm:gap-2">
          <label className="hidden items-center gap-2 md:flex">
            <span className="sr-only">Hub</span>
            <select value={station} onChange={(e) => onStation(e.target.value)} className="field h-9 max-w-52 truncate font-mono text-sm font-medium xl:max-w-none" aria-label="Hub">
              {stations.map((s) => (
                <option key={s.iata} value={s.iata}>
                  {s.iata} · {s.name.replace(/ International$/, "")}
                </option>
              ))}
            </select>
          </label>
          <span className="hidden sm:inline-flex">
            <LiveBadge status={status} rate={rate} />
          </span>
          <button type="button" onClick={onGuide} className="btn-ghost h-9 !px-2.5" aria-label="How it works">
            <BookOpen size={16} />
            <span className="hidden xl:inline">Guide</span>
          </button>
          <a
            href={REPO_URL}
            target="_blank"
            rel="noreferrer"
            className="hidden h-9 w-9 place-items-center rounded-lg text-muted transition hover:bg-surface-2 hover:text-ink lg:grid"
            aria-label="Source code on GitHub"
            title="Source on GitHub"
          >
            <GithubMark />
          </a>
          <ThemeToggle />
          <button
            type="button"
            onClick={onMenu}
            className="hidden items-center gap-2 rounded-lg p-1 transition hover:bg-surface-2 sm:flex xl:pr-2"
            aria-label={`Account and persona · ${ROLE_INFO[role].label}`}
            title={ROLE_INFO[role].label}
          >
            <span className="grid h-7 w-7 place-items-center rounded-full bg-surface-2 text-[11px] font-semibold text-ink-2 ring-1 ring-line">{initials}</span>
            <span className={`hidden whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset xl:inline ${ROLE_CHIP[role]}`}>{ROLE_INFO[role].label}</span>
          </button>
        </div>
      </div>
    </header>
  );
}
