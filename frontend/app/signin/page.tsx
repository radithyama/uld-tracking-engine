"use client";

import { useEffect, useState } from "react";
import { getProviders, signIn } from "next-auth/react";
import { ArrowRight, Scale, ShieldCheck, Truck, type LucideIcon } from "lucide-react";
import { ROLE_INFO, ROLES, type Role } from "@/lib/roles";
import { ThemeToggle } from "@/components/Navbar";

const PERSONA: Record<Role, { icon: LucideIcon; tone: string; ring: string; points: string[] }> = {
  ground_handler: {
    icon: Truck,
    tone: "bg-sky-50 text-sky-700 dark:bg-sky-500/10 dark:text-sky-300",
    ring: "hover:border-sky-300 focus-visible:border-sky-400 dark:hover:border-sky-500/50",
    points: ["Live warehouse floor & network map", "Manual scanner tool"],
  },
  loadmaster: {
    icon: Scale,
    tone: "bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300",
    ring: "hover:border-amber-300 focus-visible:border-amber-400 dark:hover:border-amber-500/50",
    points: ["Manifests & aircraft holds", "CG load optimizer", "Security drills & traffic control"],
  },
  security_auditor: {
    icon: ShieldCheck,
    tone: "bg-rose-50 text-rose-700 dark:bg-rose-500/10 dark:text-rose-300",
    ring: "hover:border-rose-300 focus-visible:border-rose-400 dark:hover:border-rose-500/50",
    points: ["Live SOC alert stream & triage", "Audit trail", "Read-only load plans"],
  },
};

const STACK = ["Go", "Kafka", "PostgreSQL", "Redis", "WebSockets", "Next.js", "Auth.js", "Docker"];

export default function SignInPage() {
  const [google, setGoogle] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    getProviders()
      .then((p) => setGoogle(!!p?.google))
      .catch(() => setGoogle(false));
  }, []);

  const demo = (role: Role) => {
    setBusy(role);
    void signIn("demo", { role, redirectTo: "/" });
  };

  return (
    <main className="relative min-h-screen bg-canvas">
      <div className="pointer-events-none absolute inset-x-0 top-0 h-80 bg-gradient-to-b from-blue-50 to-transparent dark:from-blue-500/10" />
      <div className="absolute right-5 top-5 z-10">
        <ThemeToggle />
      </div>

      {/* Both columns start on the same line: the "Live demo" pill sits level
          with "Choose a demo persona". */}
      <div className="relative mx-auto grid min-h-screen max-w-6xl content-center items-start gap-10 px-5 py-16 lg:grid-cols-[1fr_1.15fr]">
        <section className="fade-in">
          <span className="inline-flex items-center gap-2 rounded-full border border-line bg-surface px-3 py-1 text-xs font-medium text-ink-2 shadow-xs">
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" /> Live demo · streaming telemetry
          </span>
          <h1 className="mt-5 text-4xl font-semibold tracking-tight text-ink sm:text-5xl">
            Real-time air cargo,
            <br />
            <span className="text-accent">balanced and secured.</span>
          </h1>
          <p className="mt-4 max-w-lg text-base leading-relaxed text-ink-2">
            ULDPulse tracks every Unit Load Device across a multi-airport network, optimizes aircraft centre of gravity, and streams
            SIEM-ready security telemetry from scanner to screen in milliseconds.
          </p>
          <ul className="mt-6 flex max-w-lg flex-wrap gap-1.5">
            {STACK.map((t) => (
              <li key={t} className="rounded-md border border-line bg-surface px-2 py-0.5 font-mono text-[11px] text-muted shadow-xs">
                {t}
              </li>
            ))}
          </ul>
        </section>

        <section className="fade-in">
          <h2 className="text-sm font-semibold text-ink">Choose a demo persona</h2>
          <p className="mt-1 text-sm text-muted">No sign-up. Each persona sees a different, role-gated slice of the operation.</p>
          <div className="mt-4 space-y-3">
            {ROLES.map((role) => {
              const P = PERSONA[role];
              return (
                <button
                  key={role}
                  type="button"
                  onClick={() => demo(role)}
                  disabled={busy !== null}
                  className={`group card flex w-full items-center gap-4 p-4 text-left transition hover:shadow-md focus:outline-none disabled:opacity-60 ${P.ring}`}
                >
                  <span className={`grid h-11 w-11 shrink-0 place-items-center rounded-xl ${P.tone}`}>
                    <P.icon size={21} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block font-semibold text-ink">{ROLE_INFO[role].label}</span>
                    <span className="mt-0.5 block text-xs text-muted">{P.points.join(" · ")}</span>
                  </span>
                  <span className="flex items-center gap-1 text-sm font-medium text-accent">
                    {busy === role ? "Signing in…" : "Enter"}
                    <ArrowRight size={15} className="transition group-hover:translate-x-0.5" />
                  </span>
                </button>
              );
            })}
          </div>

          {google && (
            <button
              type="button"
              onClick={() => {
                setBusy("google");
                void signIn("google", { redirectTo: "/" });
              }}
              disabled={busy !== null}
              className="btn-ghost mt-4 w-full"
            >
              Sign in with Google
            </button>
          )}

          <p className="mt-6 text-[11px] text-muted">
            Demo environment. Aircraft balance figures are illustrative, not certified weight-and-balance data.
          </p>
        </section>
      </div>
    </main>
  );
}
