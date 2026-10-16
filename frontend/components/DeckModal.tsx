"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import type { LoadPlanView, Plan, PlanULD, Position } from "@/lib/types";
import { kg, timeHM } from "@/lib/format";
import { AircraftDeck, CGGauge } from "./AircraftDeck";
import { StatusBadge } from "./StatusBadge";

interface Props {
  flightId: number;
  planVersion: number; // bumps on loadplan_update
  canEdit: boolean;
  onClose: () => void;
}

export function DeckModal({ flightId, planVersion, canEdit, onClose }: Props) {
  const [view, setView] = useState<LoadPlanView | null>(null);
  const [proposal, setProposal] = useState<{ before: Plan; proposed: Plan } | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const load = useCallback(async () => {
    const res = await fetch(`/bff/flights/${flightId}/loadplan`, { cache: "no-store" });
    const body = await res.json();
    if (!res.ok) return setMsg({ kind: "err", text: body.error ?? `HTTP ${res.status}` });
    setView(body);
  }, [flightId]);

  useEffect(() => {
    // Fetch on open and whenever another user changes this flight's plan.
    let cancelled = false;
    fetch(`/bff/flights/${flightId}/loadplan`, { cache: "no-store" })
      .then(async (res) => ({ res, body: await res.json() }))
      .then(({ res, body }) => {
        if (cancelled) return;
        if (res.ok) setView(body);
        else setMsg({ kind: "err", text: body.error ?? `HTTP ${res.status}` });
      })
      .catch(() => !cancelled && setMsg({ kind: "err", text: "network error" }));
    return () => {
      cancelled = true;
    };
  }, [flightId, planVersion]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const ulds = useMemo(() => Object.fromEntries((view?.flight.ulds ?? []).map((u) => [u.id, u])), [view]);
  const saved = useMemo(() => Object.fromEntries((view?.plan.assignments ?? []).map((a) => [a.position, a.uld_id])), [view]);
  const shown = useMemo(
    () => (proposal ? Object.fromEntries(proposal.proposed.assignments.map((a) => [a.position, a.uld_id])) : saved),
    [proposal, saved],
  );
  const changed = useMemo(() => {
    const s = new Set<string>();
    if (!proposal) return s;
    for (const code of new Set([...Object.keys(saved), ...Object.keys(shown)])) if (saved[code] !== shown[code]) s.add(code);
    return s;
  }, [proposal, saved, shown]);
  const positionOf = useMemo(() => Object.fromEntries(Object.entries(shown).map(([p, u]) => [u, p])), [shown]);

  if (!view) {
    return (
      <Overlay onClose={onClose}>
        <div className="p-10 text-center text-sm text-slate-400">{msg?.text ?? "Loading load plan…"}</div>
      </Overlay>
    );
  }

  const { flight, plannable } = view;
  const ac = flight.aircraft;
  const editable = canEdit && plannable;
  const balance = view.plan.balance;
  const pctToArm = (pct: number) => ac.lemac_m + (pct / 100) * ac.mac_m;
  const shownBalance = proposal?.proposed.balance ?? balance;

  const act = async (fn: () => Promise<Response>, ok: string) => {
    setBusy(true);
    setMsg(null);
    try {
      const res = await fn();
      const body = await res.json();
      if (!res.ok) {
        setMsg({ kind: "err", text: body.error ?? `HTTP ${res.status}` });
        return null;
      }
      setMsg({ kind: "ok", text: ok });
      return body;
    } finally {
      setBusy(false);
    }
  };

  const optimize = async (apply: boolean) => {
    const body = await act(
      () => fetch(`/bff/flights/${flightId}/optimize`, { method: "POST", body: JSON.stringify({ apply }) }),
      apply ? "Optimized plan applied" : "Proposed plan: review the highlighted positions, then apply",
    );
    if (!body) return;
    if (apply) {
      setProposal(null);
      await load();
    } else setProposal({ before: body.before, proposed: body.proposed });
  };

  const move = async (uldId: string, position: string) => {
    const body = await act(
      () => fetch(`/bff/flights/${flightId}/loadplan/move`, { method: "POST", body: JSON.stringify({ uld_id: uldId, position }) }),
      position ? `${uldId} → ${position}` : `${uldId} removed from plan`,
    );
    if (body) {
      setSelected(null);
      await load();
    }
  };

  const onSlot = (p: Position) => {
    const occupant = shown[p.code];
    if (proposal) return;
    if (selected && editable && occupant !== selected) {
      if (occupant) return setMsg({ kind: "err", text: `${p.code} is occupied by ${occupant}; unplan it first` });
      void move(selected, p.code);
      return;
    }
    setSelected(occupant && occupant !== selected ? occupant : null);
  };

  const sel: PlanULD | undefined = selected ? ulds[selected] : undefined;

  return (
    <Overlay onClose={onClose}>
      <header className="flex flex-wrap items-start justify-between gap-3 border-b border-slate-800 px-5 py-4">
        <div>
          <div className="flex items-baseline gap-2">
            <h2 className="font-mono text-lg font-semibold text-slate-100">{flight.flight_number}</h2>
            <span className="text-sm text-slate-400">
              {flight.origin} → {flight.destination} · STD {timeHM(flight.departure)}
            </span>
            <span className="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-slate-300">{flight.status}</span>
          </div>
          <div className="text-xs text-slate-500">
            {ac.type} · {ac.registration} · max payload {kg(ac.max_payload_kg)}
            {!plannable && " · plan frozen (departed)"}
            {plannable && !canEdit && " · read-only"}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {editable && !proposal && (
            <button type="button" disabled={busy} onClick={() => optimize(false)} className="btn-primary">
              ⚖ Optimize CG
            </button>
          )}
          {proposal && (
            <>
              <button type="button" disabled={busy} onClick={() => optimize(true)} className="btn-primary">
                Apply plan
              </button>
              <button type="button" disabled={busy} onClick={() => setProposal(null)} className="btn-ghost">
                Discard
              </button>
            </>
          )}
          <button type="button" onClick={onClose} className="btn-ghost" aria-label="Close">
            ✕
          </button>
        </div>
      </header>

      <div className="grid gap-5 p-5 lg:grid-cols-[1fr_300px]">
        <div className="min-w-0 space-y-4">
          <AircraftDeck
            positions={flight.positions}
            ulds={ulds}
            assignment={shown}
            changed={changed}
            selected={selected}
            cgArm={shownBalance.cg_arm_m}
            targetArm={pctToArm(ac.cg_target_pct)}
            envelope={[pctToArm(ac.cg_fwd_pct), pctToArm(ac.cg_aft_pct)]}
            onSlot={onSlot}
          />
          <div className="grid gap-4 sm:grid-cols-[1fr_auto]">
            <div>
              <div className="mb-1 flex items-baseline justify-between text-xs">
                <span className="font-medium text-slate-300">Zero-fuel CG</span>
                <span className="tabular-nums text-slate-400">
                  {proposal ? (
                    <>
                      {proposal.before.balance.cg_pct_mac.toFixed(1)}% →{" "}
                      <span className="font-semibold text-sky-300">{proposal.proposed.balance.cg_pct_mac.toFixed(1)}% MAC</span>
                    </>
                  ) : (
                    <span className="font-semibold text-slate-100">{balance.cg_pct_mac.toFixed(1)}% MAC</span>
                  )}
                </span>
              </div>
              <CGGauge
                fwd={ac.cg_fwd_pct}
                aft={ac.cg_aft_pct}
                target={ac.cg_target_pct}
                current={(proposal?.before ?? view.plan).balance.cg_pct_mac}
                proposed={proposal?.proposed.balance.cg_pct_mac}
              />
            </div>
            <dl className="grid grid-cols-3 gap-3 text-xs sm:w-72">
              {["MAIN", "FWD", "AFT"].map((h) => (
                <div key={h} className="rounded-md bg-slate-800/60 px-2 py-1.5">
                  <dt className="text-[10px] uppercase tracking-wider text-slate-500">{h}</dt>
                  <dd className="tabular-nums text-slate-200">{((shownBalance.hold_kg?.[h] ?? 0) / 1000).toFixed(1)} t</dd>
                </div>
              ))}
              <div className="col-span-3 flex justify-between rounded-md bg-slate-800/60 px-2 py-1.5">
                <span className="text-slate-500">Lateral imbalance</span>
                <span className="tabular-nums text-slate-200">{Math.round(Math.abs(shownBalance.lateral_imbalance_kg))} kg</span>
              </div>
            </dl>
          </div>
          {msg && (
            <div className={`rounded-md px-3 py-2 text-sm ${msg.kind === "ok" ? "bg-sky-500/10 text-sky-200" : "bg-red-500/10 text-red-200"}`}>
              {msg.text}
            </div>
          )}
          {[...(proposal?.proposed.issues ?? view.plan.issues), ...(proposal?.proposed.unassigned ?? [])].map((i, n) => (
            <div key={n} className="rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-xs text-amber-200">
              <span className="font-mono">{i.code}</span> {i.uld_id && <span className="font-mono">· {i.uld_id}</span>} — {i.message}
            </div>
          ))}
          <p className="text-[10px] text-slate-600">
            Illustrative balance model (OEW + cargo, no fuel/index). Not for operational weight-and-balance.
          </p>
        </div>

        <aside className="space-y-3">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-500">Booked ULDs ({flight.ulds.length})</h3>
          {editable && !proposal && (
            <p className="text-[11px] text-slate-500">Select a ULD, then click an empty slot to move it.</p>
          )}
          <ul className="max-h-[420px] space-y-1.5 overflow-auto pr-1">
            {flight.ulds.map((u) => {
              const plannable = u.status === "STAGED" || u.status === "LOADED";
              return (
                <li key={u.id}>
                  <button
                    type="button"
                    onClick={() => setSelected(selected === u.id ? null : u.id)}
                    className={`flex w-full items-center justify-between gap-2 rounded-md border px-2 py-1.5 text-left text-xs transition ${
                      selected === u.id ? "border-sky-400 bg-sky-500/10" : "border-slate-800 hover:border-slate-600"
                    }`}
                  >
                    <span>
                      <span className="font-mono text-slate-100">{u.id}</span>
                      <span className="ml-1 text-slate-500">{u.uld_type}</span>
                      <div className="mt-0.5">
                        <StatusBadge status={u.status} />
                      </div>
                    </span>
                    <span className="text-right tabular-nums">
                      <div className="text-slate-200">{kg(u.weight_kg)}</div>
                      <div className={positionOf[u.id] ? "text-sky-300" : plannable ? "text-amber-300" : "text-slate-600"}>
                        {positionOf[u.id] ?? (plannable ? "unplanned" : "not ready")}
                        {u.locked && " 🔒"}
                      </div>
                    </span>
                  </button>
                </li>
              );
            })}
            {flight.ulds.length === 0 && <li className="text-xs text-slate-500">No ULDs booked on this flight yet.</li>}
          </ul>
          {sel && editable && !proposal && positionOf[sel.id] && !sel.locked && (
            <button type="button" disabled={busy} onClick={() => move(sel.id, "")} className="btn-ghost w-full">
              Remove {sel.id} from plan
            </button>
          )}
        </aside>
      </div>
    </Overlay>
  );
}

function Overlay({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-auto bg-slate-950/80 p-4 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        className="my-6 w-full max-w-6xl rounded-xl border border-slate-700 bg-slate-950 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {children}
      </div>
    </div>
  );
}
