import type { ULDStatus } from "@/lib/types";

// Muted pastel badges with high-contrast text (light), tinted in dark mode.
export const statusStyle: Record<ULDStatus, { label: string; chip: string; dot: string }> = {
  IN_TRANSIT: {
    label: "In Transit",
    chip: "bg-sky-50 text-sky-700 ring-sky-200 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30",
    dot: "bg-sky-500",
  },
  STAGED: {
    label: "Staged",
    chip: "bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/30",
    dot: "bg-amber-500",
  },
  LOADED: {
    label: "Loaded",
    chip: "bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
    dot: "bg-emerald-500",
  },
  IN_FLIGHT: {
    label: "In Flight",
    chip: "bg-violet-50 text-violet-700 ring-violet-200 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-400/30",
    dot: "bg-violet-500",
  },
};

export const overweightChip = "bg-rose-50 text-rose-700 ring-rose-300 dark:bg-rose-500/15 dark:text-rose-300 dark:ring-rose-400/40";

export function StatusBadge({ status }: { status: ULDStatus }) {
  const s = statusStyle[status] ?? statusStyle.IN_TRANSIT;
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${s.chip}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${s.dot}`} />
      {s.label}
    </span>
  );
}

/** Weight vs. limit gauge: neutral → amber at 90% → rose above 100%. */
export function WeightBar({ weight, limit }: { weight: number; limit: number }) {
  const pct = limit > 0 ? (weight / limit) * 100 : 0;
  const color = pct > 100 ? "bg-rose-500" : pct >= 90 ? "bg-amber-500" : "bg-slate-400 dark:bg-slate-500";
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2" title={`${pct.toFixed(0)}% of limit`}>
      <div className={`h-full rounded-full ${color} transition-[width] duration-500`} style={{ width: `${Math.min(pct, 100)}%` }} />
    </div>
  );
}
