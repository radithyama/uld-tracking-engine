import type { LucideIcon } from "lucide-react";

export function Panel({
  title,
  subtitle,
  action,
  icon: Icon,
  children,
  className = "",
}: {
  title: string;
  subtitle?: string;
  action?: React.ReactNode;
  icon?: LucideIcon;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section className={`card p-4 sm:p-5 ${className}`}>
      <div className="mb-4 flex items-start justify-between gap-3">
        <div className="flex items-start gap-2.5">
          {Icon && (
            <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-accent-soft text-accent-ink">
              <Icon size={15} strokeWidth={2} />
            </span>
          )}
          <div>
            <h2 className="text-sm font-semibold text-ink">{title}</h2>
            {subtitle && <p className="text-xs text-muted">{subtitle}</p>}
          </div>
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

const TONES = {
  slate: "bg-surface-2 text-ink-2",
  sky: "bg-sky-50 text-sky-700 dark:bg-sky-500/10 dark:text-sky-300",
  amber: "bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300",
  emerald: "bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300",
  violet: "bg-violet-50 text-violet-700 dark:bg-violet-500/10 dark:text-violet-300",
  rose: "bg-rose-50 text-rose-700 dark:bg-rose-500/10 dark:text-rose-300",
} as const;
export type Tone = keyof typeof TONES;

/** Metric widget: icon, label, value with unit, optional utilisation bar. */
export function Kpi({
  label,
  value,
  unit,
  icon: Icon,
  tone = "slate",
  hint,
  progress,
}: {
  label: string;
  value: number | string;
  unit?: string;
  icon?: LucideIcon;
  tone?: Tone;
  hint?: string;
  progress?: number; // 0..1
}) {
  return (
    <div className="card px-4 py-3.5">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-muted">{label}</span>
        {Icon && (
          <span className={`grid h-7 w-7 place-items-center rounded-lg ${TONES[tone]}`}>
            <Icon size={15} strokeWidth={2} />
          </span>
        )}
      </div>
      <div className="mt-1.5 flex items-baseline gap-1">
        <span className="text-2xl font-semibold tracking-tight text-ink tabular-nums">{value}</span>
        {unit && <span className="text-xs font-medium text-muted">{unit}</span>}
      </div>
      {progress !== undefined && (
        <div className="mt-2 h-1 overflow-hidden rounded-full bg-surface-2">
          <div className="h-full rounded-full bg-accent transition-[width] duration-500" style={{ width: `${Math.min(1, progress) * 100}%` }} />
        </div>
      )}
      {hint && <div className="mt-1 text-[11px] text-muted">{hint}</div>}
    </div>
  );
}

export function Skeleton({ className = "" }: { className?: string }) {
  return <div className={`skeleton ${className}`} aria-hidden="true" />;
}

/** Placeholder layout shown until the first snapshot arrives. */
export function DashboardSkeleton() {
  return (
    <div className="space-y-5" aria-busy="true" aria-label="Loading live data">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        {Array.from({ length: 6 }, (_, i) => (
          <div key={i} className="card space-y-2 px-4 py-3.5">
            <Skeleton className="h-3 w-20" />
            <Skeleton className="h-7 w-14" />
          </div>
        ))}
      </div>
      <div className="grid gap-5 xl:grid-cols-[1fr_360px]">
        <div className="card space-y-3 p-5">
          <Skeleton className="h-4 w-40" />
          {Array.from({ length: 5 }, (_, i) => (
            <div key={i} className="flex gap-2">
              <Skeleton className="h-16 w-20" />
              {Array.from({ length: 6 }, (_, j) => (
                <Skeleton key={j} className="h-16 flex-1" />
              ))}
            </div>
          ))}
        </div>
        <div className="card space-y-3 p-5">
          <Skeleton className="h-4 w-32" />
          <Skeleton className="h-24" />
          <Skeleton className="h-24" />
        </div>
      </div>
    </div>
  );
}
