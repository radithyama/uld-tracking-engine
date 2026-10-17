"use client";

import { useEffect, useRef, useState } from "react";
import { signIn } from "next-auth/react";
import {
  Activity,
  ArrowRight,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Circle,
  FlaskConical,
  Globe2,
  Plane,
  Scale,
  ScanLine,
  ShieldAlert,
  Users,
  X,
  type LucideIcon,
} from "lucide-react";
import { can, ROLE_INFO, ROLES, type Capability, type Role, type Tab } from "@/lib/roles";
import { sandboxChaos, sandboxScan, type ActionResult } from "@/lib/sandboxClient";
import type { ULD } from "@/lib/types";
import { BrandMark } from "./Navbar";

interface Props {
  open: boolean;
  onClose: () => void;
  role: Role;
  demoSession: boolean;
  ulds: ULD[];
  onTab: (t: Tab) => void;
  onOpenNextDeparture: () => boolean; // false if none available
}

const STEPS: { title: string; icon: LucideIcon; body: React.ReactNode }[] = [
  {
    title: "Choose your persona",
    icon: Users,
    body: (
      <>
        <p>ULDPulse enforces role-based access in the UI, the Next.js server and the Go services. Each persona sees a different slice of the operation:</p>
        <ul className="mt-3 space-y-2">
          {ROLES.map((r) => (
            <li key={r} className="rounded-lg border border-line bg-surface-2/60 px-3 py-2">
              <div className="text-sm font-medium text-ink">{ROLE_INFO[r].label}</div>
              <div className="text-xs text-muted">{ROLE_INFO[r].blurb}</div>
            </li>
          ))}
        </ul>
        <p className="mt-3">Switch persona any time from the menu (☰) or from the checklist on the right.</p>
      </>
    ),
  },
  {
    title: "Watch real-time telemetry",
    icon: Activity,
    body: (
      <>
        <p>
          Simulated RFID and barcode scanners at six airports stream continuously. Each scan is authenticated by the Go <b>ingest API</b>,
          ordered per ULD in <b>Kafka</b>, persisted to <b>PostgreSQL</b> by the stream processor, projected into <b>Redis</b>, and pushed
          to your browser over a <b>WebSocket</b> within milliseconds.
        </p>
        <p className="mt-3">
          The <b>Live</b> badge and the msg/s counter in the header show the stream. Container chips flash as they move between
          receiving, build-up, staging and the ramp. Switch hubs from the header or the menu.
        </p>
      </>
    ),
  },
  {
    title: "Try the Interactive Sandbox",
    icon: FlaskConical,
    body: (
      <>
        <p>Open the <b>Interactive Sandbox</b> bar at the bottom of the screen to drive the live system yourself:</p>
        <ul className="mt-3 list-disc space-y-1.5 pl-5">
          <li><b>Simulate scan</b>: pick a ULD, a bay and a weight, then submit it through the real ingest API.</li>
          <li><b>Trigger tamper</b>: a loaded container is re-weighed 30% heavier, which fires a ULD-INT-003 integrity alert.</li>
          <li><b>Trigger impossible travel</b>: a container is scanned in Jakarta, then Sydney 5 seconds later (ULD-GEO-001).</li>
          <li><b>Traffic</b>: switch the scanner simulator between 5 and 30 messages per second.</li>
        </ul>
        <p className="mt-3">Detections come from the backend, not the button. Security Auditors triage them in the SOC view.</p>
      </>
    ),
  },
  {
    title: "Flight & network optimization",
    icon: Plane,
    body: (
      <>
        <p>
          <b>Load Optimization</b> lists departures. Open one to see the aircraft hold: each position is drawn at its real balance arm,
          with structural limits and the centre-of-gravity envelope. <b>Optimize CG</b> proposes a plan, preview it, then apply it.
        </p>
        <p className="mt-3">
          The <b>Flight Network Map</b> shows aircraft flying the CGK hub rotations in real time, with every container on board and
          its ETA. The network clock runs time-compressed so you can watch a full rotation.
        </p>
      </>
    ),
  },
];

interface QuickAction {
  id: string;
  label: string;
  icon: LucideIcon;
  needs: Capability | null;
  run: () => Promise<ActionResult | null>;
}

const DONE_KEY = "uld.guide.done";

function loadDone(): string[] {
  try {
    return JSON.parse(localStorage.getItem(DONE_KEY) ?? "[]");
  } catch {
    return [];
  }
}

export function GuideModal({ open, onClose, role, demoSession, ulds, onTab, onOpenNextDeparture }: Props) {
  const [step, setStep] = useState(0);
  const [done, setDone] = useState<string[]>(loadDone);
  const [busy, setBusy] = useState<string | null>(null);
  const [result, setResult] = useState<(ActionResult & { id: string }) | null>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    dialogRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowRight") setStep((s) => Math.min(STEPS.length - 1, s + 1));
      if (e.key === "ArrowLeft") setStep((s) => Math.max(0, s - 1));
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const markDone = (id: string) => {
    setDone((prev) => {
      const next = prev.includes(id) ? prev : [...prev, id];
      try {
        localStorage.setItem(DONE_KEY, JSON.stringify(next));
      } catch {
        /* progress just isn't remembered */
      }
      return next;
    });
  };

  const scanTarget = ulds.find((u) => u.station === "CGK" && u.status !== "IN_FLIGHT" && u.status !== "LOADED") ?? ulds.find((u) => u.station === "CGK");

  const actions: QuickAction[] = [
    {
      id: "scan",
      label: "Simulate a scan at CGK",
      icon: ScanLine,
      needs: "sandbox.scan",
      run: () => sandboxScan({ uld_id: scanTarget?.id ?? "AKE10013GA", location_id: "STA-01", weight_kg: 850 }),
    },
    { id: "tamper", label: "Trigger a +30% weight tamper alert", icon: Scale, needs: "sandbox.chaos", run: () => sandboxChaos("tamper") },
    {
      id: "travel",
      label: "Trigger an impossible-travel alert (CGK → SYD in 5 s)",
      icon: Plane,
      needs: "sandbox.chaos",
      run: () => sandboxChaos("impossible_travel"),
    },
    {
      id: "optimize",
      label: "Open the next departure and optimize its CG",
      icon: Scale,
      needs: "loadplan.edit",
      run: async () => {
        if (onOpenNextDeparture()) {
          onClose();
          return null;
        }
        return { ok: false, title: "No departure to plan", detail: "No scheduled departures from this hub right now. Try CGK." };
      },
    },
    {
      id: "network",
      label: "Watch flights on the network map",
      icon: Globe2,
      needs: null,
      run: async () => {
        onTab("network");
        onClose();
        return null;
      },
    },
    {
      id: "soc",
      label: "Triage alerts in the SOC console",
      icon: ShieldAlert,
      needs: "security.view",
      run: async () => {
        onTab("security");
        onClose();
        return null;
      },
    },
  ];

  const runAction = async (a: QuickAction) => {
    setBusy(a.id);
    setResult(null);
    try {
      const r = await a.run();
      if (r) setResult({ ...r, id: a.id });
      if (!r || r.ok) markDone(a.id);
    } finally {
      setBusy(null);
    }
  };

  const S = STEPS[step];
  const completed = actions.filter((a) => done.includes(a.id)).length;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-3 sm:p-6" role="dialog" aria-modal="true" aria-labelledby="guide-title">
      <div className="fade-in absolute inset-0 bg-slate-900/30 backdrop-blur-[2px] dark:bg-black/50" onClick={onClose} />
      <div
        ref={dialogRef}
        tabIndex={-1}
        className="fade-in relative flex max-h-[92vh] w-full max-w-4xl flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-2xl outline-none"
      >
        <header className="flex items-center justify-between border-b border-line px-5 py-3.5">
          <div className="flex items-center gap-3">
            <BrandMark size={36} />
            <div>
              <h2 id="guide-title" className="text-base font-semibold text-ink">
                How ULDPulse works
              </h2>
              <p className="text-xs text-muted">A 2-minute tour of the live demo. Everything you trigger runs through the real backend.</p>
            </div>
          </div>
          <button type="button" onClick={onClose} className="grid h-9 w-9 place-items-center rounded-lg text-muted hover:bg-surface-2 hover:text-ink" aria-label="Close guide">
            <X size={18} />
          </button>
        </header>

        <div className="grid min-h-0 flex-1 overflow-y-auto md:grid-cols-[1fr_320px]">
          {/* Steps */}
          <section className="flex flex-col p-5">
            <ol className="mb-5 flex gap-1.5" aria-label="Tour steps">
              {STEPS.map((s, i) => (
                <li key={s.title} className="flex-1">
                  <button
                    type="button"
                    onClick={() => setStep(i)}
                    aria-current={i === step ? "step" : undefined}
                    className="group w-full text-left"
                  >
                    <span className={`block h-1 rounded-full transition ${i <= step ? "bg-accent" : "bg-surface-2"}`} />
                    <span className={`mt-1.5 block text-[11px] font-medium ${i === step ? "text-accent-ink" : "text-muted group-hover:text-ink-2"}`}>
                      Step {i + 1}
                    </span>
                  </button>
                </li>
              ))}
            </ol>
            <div key={step} className="fade-in flex-1">
              <div className="mb-3 flex items-center gap-3">
                <span className="grid h-10 w-10 place-items-center rounded-xl bg-accent-soft text-accent-ink">
                  <S.icon size={20} />
                </span>
                <h3 className="text-lg font-semibold tracking-tight text-ink">{S.title}</h3>
              </div>
              <div className="space-y-0 text-sm leading-relaxed text-ink-2">{S.body}</div>
            </div>
            <div className="mt-5 flex items-center justify-between">
              <button type="button" onClick={() => setStep(step - 1)} disabled={step === 0} className="btn-ghost">
                <ChevronLeft size={15} /> Back
              </button>
              <span className="text-xs text-muted">
                {step + 1} / {STEPS.length}
              </span>
              {step < STEPS.length - 1 ? (
                <button type="button" onClick={() => setStep(step + 1)} className="btn-primary">
                  Next <ChevronRight size={15} />
                </button>
              ) : (
                <button type="button" onClick={onClose} className="btn-primary">
                  Start exploring <ArrowRight size={15} />
                </button>
              )}
            </div>
          </section>

          {/* Quick start checklist */}
          <aside className="border-t border-line bg-surface-2/50 p-5 md:border-l md:border-t-0">
            <div className="flex items-baseline justify-between">
              <h3 className="text-sm font-semibold text-ink">Quick start</h3>
              <span className="text-xs tabular-nums text-muted">
                {completed}/{actions.length} done
              </span>
            </div>
            <div className="mt-2 h-1 overflow-hidden rounded-full bg-surface-2 ring-1 ring-line">
              <div className="h-full rounded-full bg-emerald-500 transition-[width] duration-500" style={{ width: `${(completed / actions.length) * 100}%` }} />
            </div>
            <ul className="mt-4 space-y-2">
              {actions.map((a) => {
                const allowed = a.needs === null || can(role, a.needs);
                const switchTo = !allowed ? ROLES.find((r) => a.needs && can(r, a.needs)) : undefined;
                const isDone = done.includes(a.id);
                return (
                  <li key={a.id} className="rounded-lg border border-line bg-surface p-2.5 shadow-xs">
                    <div className="flex items-start gap-2.5">
                      {isDone ? (
                        <CheckCircle2 size={17} className="mt-0.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
                      ) : (
                        <Circle size={17} className="mt-0.5 shrink-0 text-line-strong" />
                      )}
                      <div className="min-w-0 flex-1">
                        <div className="text-[13px] font-medium leading-snug text-ink">{a.label}</div>
                        {allowed ? (
                          <button
                            type="button"
                            disabled={busy !== null}
                            onClick={() => runAction(a)}
                            className="mt-1 inline-flex items-center gap-1 text-xs font-medium text-accent hover:underline disabled:opacity-50"
                          >
                            <a.icon size={12} /> {busy === a.id ? "Running…" : "Run it"}
                          </button>
                        ) : switchTo && demoSession ? (
                          <button
                            type="button"
                            onClick={() => signIn("demo", { role: switchTo, redirectTo: "/" })}
                            className="mt-1 text-xs font-medium text-accent hover:underline"
                          >
                            Switch to {ROLE_INFO[switchTo].label} →
                          </button>
                        ) : (
                          <div className="mt-1 text-xs text-muted">Requires {switchTo ? ROLE_INFO[switchTo].label : "another role"}</div>
                        )}
                        {result?.id === a.id && (
                          <div
                            className={`mt-2 rounded-md px-2 py-1.5 text-xs ${
                              result.ok
                                ? "bg-emerald-50 text-emerald-800 dark:bg-emerald-500/10 dark:text-emerald-200"
                                : "bg-rose-50 text-rose-800 dark:bg-rose-500/10 dark:text-rose-200"
                            }`}
                          >
                            <div className="font-medium">{result.title}</div>
                            <div className="opacity-80">{result.detail}</div>
                          </div>
                        )}
                      </div>
                    </div>
                  </li>
                );
              })}
            </ul>
          </aside>
        </div>
      </div>
    </div>
  );
}
