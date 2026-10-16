"use client";

import { useEffect, useMemo, useState } from "react";
import type { FlightLoad, ULD, WarehouseLocation } from "@/lib/types";
import { can, type Role } from "@/lib/roles";

interface Props {
  role: Role;
  station: string;
  ulds: ULD[];
  locations: WarehouseLocation[];
  flights: FlightLoad[];
}

interface Result {
  id: number;
  ok: boolean;
  title: string;
  detail: string;
}

let seq = 0;

/** Collapsible bottom toolbar so reviewers can drive the system without a terminal. */
export function SandboxToolbar({ role, station, ulds, locations, flights }: Props) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<Result[]>([]);
  const [traffic, setTraffic] = useState<{ rate_per_sec?: number; paused?: boolean } | null>(null);

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

  const push = (r: Omit<Result, "id">) => setResults((prev) => [{ id: ++seq, ...r }, ...prev].slice(0, 4));

  const call = async (url: string, body: unknown, describe: (b: Record<string, unknown>) => Omit<Result, "id">) => {
    setBusy(true);
    try {
      const res = await fetch(url, { method: "POST", body: JSON.stringify(body) });
      const b = await res.json().catch(() => ({}));
      if (!res.ok && res.status !== 202) {
        const err = b.ingest?.details ? JSON.stringify(b.ingest.details) : b.error ?? `HTTP ${res.status}`;
        push({ ok: false, title: "Rejected", detail: String(err) });
      } else push(describe(b));
      return b;
    } catch {
      push({ ok: false, title: "Network error", detail: url });
    } finally {
      setBusy(false);
    }
  };

  const chaos = (kind: "tamper" | "impossible_travel" | "unauthorized") =>
    call("/bff/sandbox/chaos", { kind }, (b) => ({
      ok: true,
      title: `${kind.replace("_", " ")} → expect ${b.expected_rule}`,
      detail: String(b.detail ?? ""),
    }));

  const setRate = async (body: object) => {
    const b = await call("/bff/sandbox/traffic", body, (b) => ({
      ok: true,
      title: "Telemetry simulator",
      detail: b.paused ? "paused" : `${b.rate_per_sec} scans/s`,
    }));
    if (b) setTraffic(b);
  };

  const field = "h-8 rounded-md border border-slate-700 bg-slate-900 px-2 text-xs text-slate-200 focus:border-sky-500 focus:outline-none";

  return (
    <div className="fixed inset-x-0 bottom-0 z-40 flex justify-center px-3 pb-3">
      <div className="w-full max-w-5xl rounded-xl border border-slate-700 bg-slate-950/95 shadow-2xl backdrop-blur">
        <button
          type="button"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          className="flex w-full items-center justify-between px-4 py-2.5 text-sm font-medium text-slate-200"
        >
          <span>🧪 Interactive Sandbox <span className="ml-2 text-xs font-normal text-slate-500">drive the live system as {role.replace("_", " ")}</span></span>
          <span className="text-slate-500">{open ? "▾" : "▴"}</span>
        </button>

        {open && (
          <div className="grid gap-4 border-t border-slate-800 p-4 md:grid-cols-[1.4fr_1fr]">
            <div className="space-y-4">
              {can(role, "sandbox.scan") && (
                <form
                  className="space-y-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void call(
                      "/bff/sandbox/scan",
                      { uld_id: uldValue, location_id: locValue, weight_kg: Number(weight), flight_number: flight },
                      (b) => ({ ok: true, title: `Scan accepted (${(b.ingest as { durability?: string })?.durability ?? "kafka"})`, detail: `${uldValue} @ ${locValue} · ${weight} kg` }),
                    );
                  }}
                >
                  <div className="text-xs font-semibold uppercase tracking-wider text-slate-500">Simulate scan · {station}</div>
                  <div className="flex flex-wrap gap-2">
                    <input
                      list="sandbox-ulds"
                      value={uldValue}
                      onChange={(e) => setUld(e.target.value.toUpperCase())}
                      className={`${field} w-32 font-mono`}
                      aria-label="ULD id"
                    />
                    <datalist id="sandbox-ulds">
                      {stationULDs.map((u) => (
                        <option key={u.id} value={u.id} />
                      ))}
                    </datalist>
                    <select value={locValue} onChange={(e) => setLoc(e.target.value)} className={field} aria-label="Location">
                      {stationLocs.map((l) => (
                        <option key={l.id} value={l.id}>
                          {l.id}
                        </option>
                      ))}
                    </select>
                    <input type="number" min={0} max={20000} step={10} value={weight} onChange={(e) => setWeight(e.target.value)} className={`${field} w-24`} aria-label="Weight kg" />
                    <select value={flight} onChange={(e) => setFlight(e.target.value)} className={field} aria-label="Flight">
                      <option value="">no flight change</option>
                      {departures.map((f) => (
                        <option key={f.id} value={f.flight_number}>
                          {f.flight_number} → {f.destination}
                        </option>
                      ))}
                    </select>
                    <button type="submit" disabled={busy || !uldValue || !locValue} className="btn-primary !text-xs">
                      Submit scan
                    </button>
                  </div>
                </form>
              )}

              {can(role, "sandbox.chaos") && (
                <div className="space-y-2">
                  <div className="text-xs font-semibold uppercase tracking-wider text-slate-500">Security drills</div>
                  <div className="flex flex-wrap gap-2">
                    <button type="button" disabled={busy} onClick={() => chaos("tamper")} className="btn-danger !text-xs" title="Load a ULD, then re-scan it 30% heavier">
                      ⚖ Trigger tamper (+30%)
                    </button>
                    <button type="button" disabled={busy} onClick={() => chaos("impossible_travel")} className="btn-danger !text-xs" title="CGK then SYD, 5 seconds apart">
                      ✈ Trigger impossible travel
                    </button>
                    <button type="button" disabled={busy} onClick={() => chaos("unauthorized")} className="btn-ghost !text-xs" title="Scan signed with a forged API key">
                      🔑 Unauthorized scan
                    </button>
                  </div>
                </div>
              )}

              {canTraffic && (
                <div className="space-y-2">
                  <div className="text-xs font-semibold uppercase tracking-wider text-slate-500">
                    Telemetry traffic{" "}
                    <span className="font-normal normal-case text-slate-400">
                      {traffic ? (traffic.paused ? "· paused" : `· ${traffic.rate_per_sec} scans/s`) : ""}
                    </span>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <button type="button" disabled={busy} onClick={() => setRate({ action: traffic?.paused ? "resume" : "pause" })} className="btn-ghost !text-xs">
                      {traffic?.paused ? "▶ Resume" : "⏸ Pause"}
                    </button>
                    {[5, 30].map((r) => (
                      <button
                        key={r}
                        type="button"
                        disabled={busy}
                        onClick={() => setRate({ action: "rate", rate: r })}
                        className={`btn-ghost !text-xs ${traffic?.rate_per_sec === r && !traffic.paused ? "!border-sky-500 !text-sky-200" : ""}`}
                      >
                        {r} msg/s
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>

            <div>
              <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500">Results</div>
              <ul className="space-y-2" aria-live="polite">
                {results.map((r) => (
                  <li key={r.id} className={`rounded-md border px-3 py-2 text-xs ${r.ok ? "border-sky-500/30 bg-sky-500/5" : "border-red-500/40 bg-red-500/10"}`}>
                    <div className={r.ok ? "font-medium text-sky-200" : "font-medium text-red-200"}>{r.title}</div>
                    <div className="mt-0.5 break-words text-slate-400">{r.detail}</div>
                  </li>
                ))}
                {results.length === 0 && (
                  <li className="text-xs text-slate-500">
                    Actions go through the real ingest API, Kafka and processor. Security drills raise genuine detections
                    {can(role, "security.view") ? " in the Security tab." : "; sign in as Security Auditor to triage them."}
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
