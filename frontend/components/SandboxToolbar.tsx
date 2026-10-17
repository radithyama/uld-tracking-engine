"use client";

import { useEffect, useMemo, useState } from "react";
import { ChevronDown, ChevronUp, FlaskConical, KeyRound, Pause, Plane, Play, Scale, ScanLine } from "lucide-react";
import type { FlightLoad, ULD, WarehouseLocation } from "@/lib/types";
import { can, type Role } from "@/lib/roles";
import { sandboxChaos, sandboxScan, sandboxTraffic, type ActionResult, type ChaosKind, type TrafficState } from "@/lib/sandboxClient";

interface Props {
  role: Role;
  station: string;
  ulds: ULD[];
  locations: WarehouseLocation[];
  flights: FlightLoad[];
}

let seq = 0;

/** Collapsible bottom toolbar so reviewers can drive the system without a terminal. */
export function SandboxToolbar({ role, station, ulds, locations, flights }: Props) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<(ActionResult & { id: number })[]>([]);
  const [traffic, setTraffic] = useState<TrafficState | null>(null);

  const stationLocs = useMemo(() => locations.filter((l) => l.station === station), [locations, station]);
  const stationULDs = useMemo(
    () => ulds.filter((u) => u.station === station && u.status !== "IN_FLIGHT").sort((a, b) => a.id.localeCompare(b.id)),
    [ulds, station],
  );
  const departures = useMemo(() => flights.filter((f) => f.origin === station && f.status === "SCHEDULED"), [flights, station]);

  const [uld, setUld] = useState("");
  const [loc, setLoc] = useState("");
  const [weight, setWeight] = useState("850");
  const [flight, setFlight] = useState("");
  const uldValue = uld || stationULDs[0]?.id || "";
  const locValue = stationLocs.some((l) => l.id === loc) ? loc : stationLocs[0]?.id ?? "";

  const canTraffic = can(role, "sandbox.traffic");
  useEffect(() => {
    if (!open || !canTraffic) return;
    fetch("/bff/sandbox/traffic", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : null))
      .then(setTraffic)
      .catch(() => {});
  }, [open, canTraffic]);

  const run = async (fn: () => Promise<ActionResult>) => {
    setBusy(true);
    try {
      const r = await fn();
      setResults((prev) => [{ id: ++seq, ...r }, ...prev].slice(0, 4));
    } finally {
      setBusy(false);
    }
  };

  const chaos = (kind: ChaosKind) => run(() => sandboxChaos(kind));

  const setRate = (body: Parameters<typeof sandboxTraffic>[0]) =>
    run(async () => {
      const r = await sandboxTraffic(body);
      if (!r.ok) return { ok: false, title: "Simulator", detail: r.error };
      setTraffic(r.state);
      return { ok: true, title: "Telemetry simulator", detail: r.state.paused ? "paused" : `${r.state.rate_per_sec} scans/s` };
    });

  const label = "text-[11px] font-semibold uppercase tracking-wider text-muted";

  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-0 z-40 flex justify-center px-3 pb-3">
      <div className="pointer-events-auto w-full max-w-5xl overflow-hidden rounded-2xl border border-line bg-surface/95 shadow-xl backdrop-blur">
        <button
          type="button"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          className="flex w-full items-center justify-between gap-3 px-4 py-2.5 text-left transition hover:bg-surface-2/60"
        >
          <span className="flex items-center gap-2.5">
            <span className="grid h-7 w-7 place-items-center rounded-lg bg-accent-soft text-accent-ink">
              <FlaskConical size={15} />
            </span>
            <span>
              <span className="block text-sm font-semibold text-ink">Interactive Sandbox</span>
              <span className="block text-xs text-muted">Drive the live system as {role.replace("_", " ")}</span>
            </span>
          </span>
          {open ? <ChevronDown size={18} className="text-muted" /> : <ChevronUp size={18} className="text-muted" />}
        </button>

        {open && (
          <div className="grid max-h-[55vh] gap-5 overflow-y-auto border-t border-line p-4 md:grid-cols-[1.4fr_1fr]">
            <div className="space-y-5">
              {can(role, "sandbox.scan") && (
                <form
                  className="space-y-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void run(() => sandboxScan({ uld_id: uldValue, location_id: locValue, weight_kg: Number(weight), flight_number: flight }));
                  }}
                >
                  <div className={label}>Simulate scan · {station}</div>
                  <div className="flex flex-wrap gap-2">
                    <input list="sandbox-ulds" value={uldValue} onChange={(e) => setUld(e.target.value.toUpperCase())} className="field w-32 font-mono" aria-label="ULD id" />
                    <datalist id="sandbox-ulds">
                      {stationULDs.map((u) => (
                        <option key={u.id} value={u.id} />
                      ))}
                    </datalist>
                    <select value={locValue} onChange={(e) => setLoc(e.target.value)} className="field" aria-label="Bay">
                      {stationLocs.map((l) => (
                        <option key={l.id} value={l.id}>
                          {l.id}
                        </option>
                      ))}
                    </select>
                    <div className="relative">
                      <input type="number" min={0} max={20000} step={10} value={weight} onChange={(e) => setWeight(e.target.value)} className="field w-28 pr-8" aria-label="Weight in kilograms" />
                      <span className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-xs text-muted">kg</span>
                    </div>
                    <select value={flight} onChange={(e) => setFlight(e.target.value)} className="field" aria-label="Flight">
                      <option value="">Keep flight</option>
                      {departures.map((f) => (
                        <option key={f.id} value={f.flight_number}>
                          {f.flight_number} → {f.destination}
                        </option>
                      ))}
                    </select>
                    <button type="submit" disabled={busy || !uldValue || !locValue} className="btn-primary">
                      <ScanLine size={15} /> Submit scan
                    </button>
                  </div>
                </form>
              )}

              {can(role, "sandbox.chaos") && (
                <div className="space-y-2">
                  <div className={label}>Security drills</div>
                  <div className="flex flex-wrap gap-2">
                    <button type="button" disabled={busy} onClick={() => chaos("tamper")} className="btn-danger" title="Load a ULD, then re-scan it 30% heavier">
                      <Scale size={15} /> Tamper +30%
                    </button>
                    <button type="button" disabled={busy} onClick={() => chaos("impossible_travel")} className="btn-danger" title="CGK then SYD, 5 seconds apart">
                      <Plane size={15} /> Impossible travel
                    </button>
                    <button type="button" disabled={busy} onClick={() => chaos("unauthorized")} className="btn-ghost" title="Scan signed with a forged API key">
                      <KeyRound size={15} /> Unauthorized scan
                    </button>
                  </div>
                </div>
              )}

              {canTraffic && (
                <div className="space-y-2">
                  <div className={label}>
                    Telemetry traffic{" "}
                    <span className="font-normal normal-case tracking-normal text-ink-2">
                      {traffic ? (traffic.paused ? "· paused" : `· ${traffic.rate_per_sec} scans/s`) : ""}
                    </span>
                  </div>
                  <div className="inline-flex rounded-lg border border-line bg-surface-2 p-0.5">
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => setRate({ action: traffic?.paused ? "resume" : "pause" })}
                      className="inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm text-ink-2 transition hover:text-ink"
                    >
                      {traffic?.paused ? <Play size={14} /> : <Pause size={14} />}
                      {traffic?.paused ? "Resume" : "Pause"}
                    </button>
                    {[5, 30].map((r) => {
                      const active = traffic?.rate_per_sec === r && !traffic.paused;
                      return (
                        <button
                          key={r}
                          type="button"
                          disabled={busy}
                          onClick={() => setRate({ action: "rate", rate: r })}
                          className={`rounded-md px-3 py-1.5 text-sm tabular-nums transition ${active ? "bg-surface font-medium text-accent-ink shadow-xs ring-1 ring-line" : "text-ink-2 hover:text-ink"}`}
                        >
                          {r} msg/s
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}
            </div>

            <div>
              <div className={`${label} mb-2`}>Results</div>
              <ul className="space-y-2" aria-live="polite">
                {results.map((r) => (
                  <li
                    key={r.id}
                    className={`fade-in rounded-lg border px-3 py-2 text-xs ${
                      r.ok
                        ? "border-emerald-200 bg-emerald-50 dark:border-emerald-500/30 dark:bg-emerald-500/10"
                        : "border-rose-200 bg-rose-50 dark:border-rose-500/40 dark:bg-rose-500/10"
                    }`}
                  >
                    <div className={`font-medium ${r.ok ? "text-emerald-800 dark:text-emerald-200" : "text-rose-800 dark:text-rose-200"}`}>{r.title}</div>
                    <div className="mt-0.5 break-words text-ink-2">{r.detail}</div>
                  </li>
                ))}
                {results.length === 0 && (
                  <li className="rounded-lg border border-dashed border-line px-3 py-3 text-xs text-muted">
                    Actions go through the real ingest API, Kafka and processor. Security drills raise genuine detections
                    {can(role, "security.view") ? " in the SOC view." : "; switch to Security Auditor to triage them."}
                  </li>
                )}
              </ul>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
