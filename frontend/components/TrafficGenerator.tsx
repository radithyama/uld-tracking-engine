"use client";

import { useCallback, useEffect, useState } from "react";
import { Activity } from "lucide-react";
import { getGenerator, setGenerator, type GeneratorState } from "@/lib/sandboxClient";

const POLL_MS = 10_000;

const mmss = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;

/**
 * On/off switch for the simulator's traffic generator: extra live warehouse
 * activity for exploring the demo. The state is shared by every viewer (it's
 * one simulator), so it's polled; it switches itself off after a time limit.
 */
export function TrafficGenerator() {
  const [state, setState] = useState<GeneratorState | null>(null);
  const [endsAt, setEndsAt] = useState<number | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const apply = useCallback((s: GeneratorState | null) => {
    setState(s);
    setEndsAt(s?.enabled && s.remaining_seconds != null ? Date.now() + s.remaining_seconds * 1000 : null);
  }, []);

  useEffect(() => {
    let alive = true;
    const load = () => getGenerator().then((s) => alive && apply(s));
    void load();
    const t = setInterval(load, POLL_MS);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [apply]);

  // Local countdown between polls.
  useEffect(() => {
    if (!endsAt) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [endsAt]);

  const remaining = endsAt ? Math.max(0, Math.round((endsAt - now) / 1000)) : 0;
  const on = !!state?.enabled && remaining > 0;
  const unavailable = state === null;

  const toggle = async () => {
    setBusy(true);
    setError("");
    const r = await setGenerator(!on);
    if (r.ok) apply(r.state);
    else setError(r.error);
    setBusy(false);
  };

  return (
    <div className="flex items-center gap-3 rounded-xl border border-line bg-surface px-3 py-2 shadow-xs">
      <span
        className={`grid h-8 w-8 shrink-0 place-items-center rounded-lg ${
          on ? "bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-300" : "bg-surface-2 text-muted"
        }`}
      >
        <Activity size={16} className={on ? "animate-pulse" : ""} />
      </span>
      <div className="min-w-0 leading-tight">
        <div className="text-sm font-semibold text-ink">Traffic generator</div>
        <div className="text-xs text-muted" aria-live="polite">
          {error
            ? <span className="text-rose-600 dark:text-rose-300">{error}</span>
            : unavailable
              ? "Simulator unavailable"
              : on
                ? `${state?.rate_per_sec ?? ""} scans/s · auto-off in ${mmss(remaining)}`
                : "Off · simulates live warehouse activity"}
        </div>
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label="Traffic generator"
        disabled={busy || unavailable}
        onClick={toggle}
        className={`relative ml-1 inline-flex h-6 w-11 shrink-0 items-center rounded-full transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50 ${
          on ? "bg-emerald-500" : "bg-line-strong"
        }`}
      >
        <span className={`inline-block h-5 w-5 rounded-full bg-white shadow transition-transform ${on ? "translate-x-5.5" : "translate-x-0.5"}`} />
      </button>
    </div>
  );
}
