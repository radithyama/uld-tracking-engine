import type { ULDStatus } from "@/lib/types";

export const statusStyle: Record<ULDStatus, { label: string; chip: string; dot: string }> = {
  IN_TRANSIT: {
    label: "In Transit",
    chip: "bg-sky-500/10 text-sky-300 ring-sky-400/30",
    dot: "bg-sky-400",
  },
  STAGED: {
    label: "Staged",
    chip: "bg-amber-500/10 text-amber-300 ring-amber-400/30",
    dot: "bg-amber-400",
  },
  LOADED: {
    label: "Loaded",
    chip: "bg-emerald-500/10 text-emerald-300 ring-emerald-400/30",
    dot: "bg-emerald-400",
  },
};

export function StatusBadge({ status }: { status: ULDStatus }) {
  const s = statusStyle[status] ?? statusStyle.IN_TRANSIT;
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${s.chip}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${s.dot}`} />
      {s.label}
    </span>
  );
}

/** Horizontal gauge of weight vs. limit; turns amber at 90% and red above 100%. */
export function WeightBar({ weight, limit }: { weight: number; limit: number }) {
  const pct = limit > 0 ? (weight / limit) * 100 : 0;
  const color = pct > 100 ? "bg-red-500" : pct >= 90 ? "bg-amber-400" : "bg-slate-400";
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-800" title={`${pct.toFixed(0)}% of limit`}>
      <div className={`h-full ${color} transition-[width] duration-500`} style={{ width: `${Math.min(pct, 100)}%` }} />
    </div>
  );
}
