/** "12m 05s" / "3s" countdown; negative values read as "due". */
export function countdown(targetIso: string | null, now: number): string {
  if (!targetIso || !now) return "—";
  const s = Math.round((Date.parse(targetIso) - now) / 1000);
  if (s <= 0) return "due";
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  return h ? `${h}h ${String(m).padStart(2, "0")}m` : m ? `${m}m ${String(sec).padStart(2, "0")}s` : `${sec}s`;
}

/** Fraction of the way from start to end (0..1). */
export function progress(startIso: string | null, endIso: string | null, now: number): number {
  if (!startIso || !endIso || !now) return 0;
  const a = Date.parse(startIso), b = Date.parse(endIso);
  return b > a ? Math.min(1, Math.max(0, (now - a) / (b - a))) : 1;
}
