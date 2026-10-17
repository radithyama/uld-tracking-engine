"use client";

import type { PlanULD, Position } from "@/lib/types";

interface Props {
  positions: Position[];
  ulds: Record<string, PlanULD>;
  assignment: Record<string, string>; // position code -> uld id
  changed: Set<string>; // positions whose ULD differs from the saved plan
  selected: string | null; // uld id
  cgArm: number;
  targetArm: number;
  envelope: [number, number]; // arms
  onSlot: (p: Position) => void;
}

const W = 900;
const PAD = 46;
const BAND_H = 92;

// Semi-transparent fills read well on both light and dark surfaces.
function fillColor(pct: number) {
  if (pct > 100) return { fill: "rgb(244 63 94 / 0.18)", stroke: "rgb(225 29 72)" }; // rose
  if (pct >= 90) return { fill: "rgb(245 158 11 / 0.2)", stroke: "rgb(217 119 6)" }; // amber
  if (pct >= 60) return { fill: "rgb(16 185 129 / 0.16)", stroke: "rgb(5 150 105)" }; // emerald
  return { fill: "rgb(14 165 233 / 0.14)", stroke: "rgb(2 132 199)" }; // sky
}

/**
 * Top-down cargo deck: x is the balance arm (nose left), y is lateral side.
 * Positions are drawn at their true arms, so the picture *is* the moment
 * diagram the optimizer balances.
 */
export function AircraftDeck({ positions, ulds, assignment, changed, selected, cgArm, targetArm, envelope, onSlot }: Props) {
  const arms = positions.map((p) => p.arm_m).concat(envelope);
  const lo = Math.min(...arms) - 2.5;
  const hi = Math.max(...arms) + 2.5;
  const x = (arm: number) => PAD + ((arm - lo) / (hi - lo)) * (W - 2 * PAD);

  const decks = (["MAIN", "LOWER"] as const).filter((d) => positions.some((p) => p.deck === d));
  const H = decks.length * (BAND_H + 34) + 34;

  // Slot width from the tightest spacing between neighbouring positions.
  const slotW = (deck: string) => {
    const xs = [...new Set(positions.filter((p) => p.deck === deck).map((p) => p.arm_m))].sort((a, b) => a - b);
    let gap = Infinity;
    for (let i = 1; i < xs.length; i++) gap = Math.min(gap, x(xs[i]) - x(xs[i - 1]));
    return Math.max(22, Math.min(64, (Number.isFinite(gap) ? gap : 60) * 0.88));
  };

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label="Aircraft cargo deck load plan">
      {decks.map((deck, di) => {
        const top = 22 + di * (BAND_H + 34);
        const cy = top + BAND_H / 2;
        const w = slotW(deck);
        const ps = positions.filter((p) => p.deck === deck);
        return (
          <g key={deck}>
            <text x={PAD} y={top - 8} className="fill-muted text-[11px] font-semibold tracking-widest">
              {deck === "MAIN" ? "MAIN DECK" : "LOWER DECK"}
            </text>
            {/* fuselage */}
            <path
              d={`M ${PAD - 30} ${cy} Q ${PAD - 24} ${top} ${PAD + 10} ${top} L ${W - PAD + 6} ${top} Q ${W - PAD + 26} ${top + 6} ${W - PAD + 30} ${cy} Q ${W - PAD + 26} ${top + BAND_H - 6} ${W - PAD + 6} ${top + BAND_H} L ${PAD + 10} ${top + BAND_H} Q ${PAD - 24} ${top + BAND_H} ${PAD - 30} ${cy} Z`}
              className="fill-surface-2 stroke-line-strong"
              strokeWidth={1.5}
            />
            {/* CG envelope, target and current CG */}
            <rect x={x(envelope[0])} y={top + 2} width={x(envelope[1]) - x(envelope[0])} height={BAND_H - 4} className="fill-accent/5" />
            <line x1={x(targetArm)} x2={x(targetArm)} y1={top} y2={top + BAND_H} className="stroke-accent/60" strokeDasharray="4 4" />
            {ps.map((p) => {
              const uldId = assignment[p.code];
              const u = uldId ? ulds[uldId] : undefined;
              const bulk = p.uld_types.length === 0;
              const h = p.lateral === 0 ? BAND_H - 18 : (BAND_H - 22) / 2;
              const y = p.lateral === 1 ? cy + 2 : p.lateral === -1 ? cy - h - 2 : cy - h / 2;
              const pct = u ? (u.weight_kg / p.max_weight_kg) * 100 : 0;
              const c = u ? fillColor(pct) : null;
              const isSel = !!u && u.id === selected;
              return (
                <g
                  key={p.code}
                  onClick={() => !bulk && onSlot(p)}
                  className={bulk ? "" : "cursor-pointer"}
                  role={bulk ? undefined : "button"}
                  aria-label={`${p.code}${u ? `: ${u.id} ${Math.round(u.weight_kg)} kg` : " empty"}`}
                >
                  <title>
                    {`${p.code} · ${p.hold} · arm ${p.arm_m} m · limit ${p.max_weight_kg} kg${bulk ? " · bulk (no ULDs)" : ` · ${p.uld_types.join("/")}`}${u ? `\n${u.id} ${u.uld_type} ${Math.round(u.weight_kg)} kg (${pct.toFixed(0)}%)` : ""}`}
                  </title>
                  <rect
                    x={x(p.arm_m) - w / 2}
                    y={y}
                    width={w}
                    height={h}
                    rx={4}
                    fill={c ? c.fill : "transparent"}
                    stroke={isSel ? "var(--accent)" : c ? c.stroke : "rgb(148 163 184)"}
                    strokeWidth={isSel ? 2.5 : changed.has(p.code) ? 2 : 1}
                    strokeDasharray={u ? undefined : bulk ? "2 3" : "4 3"}
                  />
                  {changed.has(p.code) && (
                    <circle cx={x(p.arm_m) + w / 2 - 4} cy={y + 4} r={3} className="fill-accent" />
                  )}
                  <text x={x(p.arm_m)} y={y + 11} textAnchor="middle" className="pointer-events-none fill-muted text-[9px]">
                    {bulk ? "BULK" : p.code}
                  </text>
                  {u && h > 26 && (
                    <>
                      <text x={x(p.arm_m)} y={y + h / 2 + 4} textAnchor="middle" className="pointer-events-none fill-ink text-[9px] font-mono font-semibold">
                        {u.id.slice(3, 8)}
                      </text>
                      <text x={x(p.arm_m)} y={y + h - 5} textAnchor="middle" className="pointer-events-none fill-ink-2 text-[9px]">
                        {(u.weight_kg / 1000).toFixed(1)}t
                      </text>
                    </>
                  )}
                  {u && h <= 26 && (
                    <text x={x(p.arm_m)} y={y + h - 4} textAnchor="middle" className="pointer-events-none fill-ink text-[9px]">
                      {(u.weight_kg / 1000).toFixed(1)}t
                    </text>
                  )}
                </g>
              );
            })}
            <line x1={x(cgArm)} x2={x(cgArm)} y1={top - 3} y2={top + BAND_H + 3} className="stroke-ink" strokeWidth={2} />
          </g>
        );
      })}
      {/* arm axis */}
      <g>
        {Array.from({ length: Math.floor(hi / 5) - Math.ceil(lo / 5) + 1 }, (_, i) => (Math.ceil(lo / 5) + i) * 5).map((m) => (
          <g key={m}>
            <line x1={x(m)} x2={x(m)} y1={H - 26} y2={H - 21} className="stroke-line-strong" />
            <text x={x(m)} y={H - 10} textAnchor="middle" className="fill-muted text-[9px]">
              {m} m
            </text>
          </g>
        ))}
        <text x={PAD - 30} y={H - 10} className="fill-muted text-[9px] font-semibold">
          NOSE
        </text>
      </g>
    </svg>
  );
}

/** Horizontal %MAC gauge: envelope band, target tick, current (and proposed) CG. */
export function CGGauge({ fwd, aft, target, current, proposed }: { fwd: number; aft: number; target: number; current: number; proposed?: number }) {
  const lo = Math.min(fwd, current, proposed ?? current) - 5;
  const hi = Math.max(aft, current, proposed ?? current) + 5;
  const pos = (v: number) => `${((v - lo) / (hi - lo)) * 100}%`;
  const inEnv = (v: number) => v >= fwd && v <= aft;
  return (
    <div>
      <div className="relative h-7 rounded-md bg-surface-2 ring-1 ring-inset ring-line">
        <div className="absolute inset-y-0 rounded-md bg-emerald-500/15 ring-1 ring-inset ring-emerald-500/40" style={{ left: pos(fwd), width: `calc(${pos(aft)} - ${pos(fwd)})` }} />
        <div className="absolute inset-y-0 w-px bg-accent" style={{ left: pos(target) }} title={`target ${target}% MAC`} />
        <div
          className={`absolute top-0 h-7 w-1 -translate-x-1/2 rounded ${inEnv(current) ? "bg-ink" : "bg-rose-500"} ${proposed !== undefined ? "opacity-40" : ""}`}
          style={{ left: pos(current) }}
          title={`current ${current.toFixed(1)}% MAC`}
        />
        {proposed !== undefined && (
          <div className={`absolute top-0 h-7 w-1 -translate-x-1/2 rounded ${inEnv(proposed) ? "bg-accent" : "bg-rose-500"}`} style={{ left: pos(proposed) }} />
        )}
      </div>
      <div className="mt-1 flex justify-between text-[10px] tabular-nums text-muted">
        <span>FWD {fwd}%</span>
        <span className="text-accent">target {target}%</span>
        <span>AFT {aft}%</span>
      </div>
    </div>
  );
}
