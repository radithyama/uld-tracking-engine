"use client";

import { useEffect, useState } from "react";
import { getProviders, signIn } from "next-auth/react";
import { ROLE_INFO, ROLES, type Role } from "@/lib/roles";

const ROLE_ICON: Record<Role, string> = { ground_handler: "⛟", loadmaster: "⚖", security_auditor: "⛨" };
const ROLE_ACCENT: Record<Role, string> = {
  ground_handler: "hover:border-sky-500/60 focus-visible:border-sky-500",
  loadmaster: "hover:border-amber-500/60 focus-visible:border-amber-500",
  security_auditor: "hover:border-red-500/60 focus-visible:border-red-500",
};

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
    <main className="grid min-h-screen place-items-center px-4 py-10">
      <div className="w-full max-w-3xl">
        <div className="mb-8 text-center">
          <div className="text-xs font-semibold uppercase tracking-[0.3em] text-sky-400">ULDPulse</div>
          <h1 className="mt-2 text-2xl font-semibold text-slate-100">ULD Operations Control</h1>
          <p className="mt-2 text-sm text-slate-400">
            Real-time air cargo tracking, load planning and SOC telemetry. Choose a demo role to explore.
          </p>
        </div>

        <div className="grid gap-3 sm:grid-cols-3">
          {ROLES.map((role) => (
            <button
              key={role}
              type="button"
              onClick={() => demo(role)}
              disabled={busy !== null}
              className={`group rounded-xl border border-slate-800 bg-slate-900/60 p-5 text-left transition focus:outline-none disabled:opacity-60 ${ROLE_ACCENT[role]}`}
            >
              <div className="text-2xl">{ROLE_ICON[role]}</div>
              <div className="mt-3 font-semibold text-slate-100">{ROLE_INFO[role].label}</div>
              <p className="mt-1 text-xs leading-relaxed text-slate-400">{ROLE_INFO[role].blurb}</p>
              <div className="mt-4 text-xs font-medium text-sky-400 group-hover:underline">
                {busy === role ? "Signing in…" : "Continue as demo →"}
              </div>
            </button>
          ))}
        </div>

        {google && (
          <div className="mt-6 text-center">
            <button
              type="button"
              onClick={() => {
                setBusy("google");
                void signIn("google", { redirectTo: "/" });
              }}
              disabled={busy !== null}
              className="rounded-lg border border-slate-700 bg-slate-900 px-4 py-2 text-sm text-slate-200 hover:border-slate-500 disabled:opacity-60"
            >
              Sign in with Google
            </button>
          </div>
        )}

        <p className="mt-8 text-center text-[11px] text-slate-600">
          Demo environment. Balance figures are illustrative, not certified weight-and-balance data.
        </p>
      </div>
    </main>
  );
}
