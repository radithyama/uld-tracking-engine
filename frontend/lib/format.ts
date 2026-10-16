import { useEffect, useState } from "react";

const kgFmt = new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 });

export const kg = (n: number) => `${kgFmt.format(n)} kg`;
export const tonnes = (n: number) =>
  `${(n / 1000).toLocaleString("en-US", { maximumFractionDigits: 1 })} t`;

export function ago(iso: string | null, now: number): string {
  if (!iso || !now) return "—";
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  return `${Math.floor(s / 3600)}h ago`;
}

export function timeHM(iso: string): string {
  return new Date(iso).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
}

/** Re-renders the caller every `ms` so relative timestamps stay fresh. */
// Returns 0 until mounted: reading the clock during render would freeze it
// into the prerendered HTML.
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(0);
  useEffect(() => {
    const tick = () => setNow(Date.now());
    const first = setTimeout(tick, 0);
    const t = setInterval(tick, ms);
    return () => {
      clearTimeout(first);
      clearInterval(t);
    };
  }, [ms]);
  return now;
}
