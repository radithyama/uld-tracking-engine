"use client";

import { useEffect, useMemo, useState } from "react";
import { ClipboardList, ShieldAlert } from "lucide-react";
import type { AlertStatus, AuditEntry, SecurityAlert } from "@/lib/types";
import type { SecurityStream } from "@/lib/useSecurityStream";
import { ago, useNow } from "@/lib/format";
import { Panel } from "./Panel";

const FAMILIES = [
  { key: "GEO", label: "Impossible travel", match: (r: string) => r.startsWith("ULD-GEO"), tone: "violet" },
  { key: "INT", label: "Weight tampering", match: (r: string) => r.startsWith("ULD-INT"), tone: "red" },
  { key: "AUTH", label: "Auth / API keys", match: (r: string) => r.startsWith("ULD-AUTH"), tone: "amber" },
  { key: "DLQ", label: "Dead-letter", match: (r: string) => r.startsWith("ULD-DLQ") || r.startsWith("ULD-INPUT"), tone: "sky" },
  { key: "AVAIL", label: "Availability", match: (r: string) => r.startsWith("ULD-AVAIL"), tone: "slate" },
] as const;
type Family = (typeof FAMILIES)[number]["key"];

const TONE: Record<string, string> = {
  violet: "border-violet-200 dark:border-violet-500/40 text-violet-800 dark:text-violet-200 bg-violet-50 dark:bg-violet-500/10",
  red: "border-rose-200 dark:border-rose-500/40 text-rose-700 dark:text-rose-200 bg-rose-50 dark:bg-rose-500/10",
  amber: "border-amber-200 dark:border-amber-500/40 text-amber-800 dark:text-amber-200 bg-amber-50 dark:bg-amber-500/10",
  sky: "border-sky-200 dark:border-sky-500/40 text-sky-800 dark:text-sky-200 bg-sky-50 dark:bg-sky-500/10",
  slate: "border-line text-ink-2 bg-surface",
};

export function severityStyle(sev: number) {
  if (sev >= 99) return { label: "CRITICAL", cls: "bg-rose-600 text-white" };
  if (sev >= 73) return { label: "HIGH", cls: "bg-rose-100 dark:bg-rose-500/20 text-rose-600 dark:text-rose-300 ring-1 ring-inset ring-rose-200 dark:ring-rose-500/40" };
  if (sev >= 47) return { label: "MEDIUM", cls: "bg-amber-50 dark:bg-amber-500/15 text-amber-700 dark:text-amber-300 ring-1 ring-inset ring-amber-200 dark:ring-amber-500/30" };
  return { label: "LOW", cls: "bg-surface-2 text-ink-2 ring-1 ring-inset ring-line" };
}

function entity(a: SecurityAlert): string {
  const parts = [a.uld_id && `ULD ${a.uld_id}`, a.principal && `key ${a.principal}`, a.source_ip && `src ${a.source_ip}`].filter(Boolean);
  if (a.rule_id === "ULD-GEO-001") {
    const g = a.details?.geo as { previous?: { station?: string }; current?: { station?: string }; implied_speed_kmh?: number } | undefined;
    if (g) parts.push(`${g.previous?.station}→${g.current?.station} @ ${Math.round(g.implied_speed_kmh ?? 0).toLocaleString()} km/h`);
  }
  if (a.rule_id.startsWith("ULD-INT")) {
    const w = a.details?.weight as { reported_kg?: number; previous_kg?: number; limit_kg?: number } | undefined;
    if (w) parts.push(`${w.previous_kg ?? "?"} → ${w.reported_kg} kg${w.limit_kg ? ` (limit ${w.limit_kg})` : ""}`);
  }
  return parts.join(" · ") || a.service;
}

export function SecurityView({ stream }: { stream: SecurityStream }) {
  const now = useNow();
  const [family, setFamily] = useState<Family | null>(null);
  const [status, setStatus] = useState<AlertStatus | "">("OPEN");
  const [open, setOpen] = useState<string | null>(null);
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [audit, setAudit] = useState<AuditEntry[]>([]);

  const counts = useMemo(() => {
    const c = Object.fromEntries(FAMILIES.map((f) => [f.key, { open: 0, total: 0 }])) as Record<Family, { open: number; total: number }>;
    for (const a of stream.alerts) {
      const f = FAMILIES.find((x) => x.match(a.rule_id));
      if (!f) continue;
      c[f.key].total++;
      if (a.status === "OPEN") c[f.key].open++;
    }
    return c;
  }, [stream.alerts]);

  const rows = useMemo(() => {
    const fam = FAMILIES.find((f) => f.key === family);
    return stream.alerts.filter((a) => (!fam || fam.match(a.rule_id)) && (!status || a.status === status));
  }, [stream.alerts, family, status]);

  // Refresh the audit log whenever an alert changes state.
  const stateKey = stream.alerts.map((a) => a.status[0]).join("");
  useEffect(() => {
    let cancelled = false;
    const t = setTimeout(() => {
      fetch("/bff/audit", { cache: "no-store" })
        .then((r) => (r.ok ? r.json() : { entries: [] }))
        .then((b) => !cancelled && setAudit(b.entries ?? []))
        .catch(() => {});
    }, 400);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [stateKey]);

  const transition = async (a: SecurityAlert, action: "ack" | "resolve") => {
    setBusy(a.id);
    setError(null);
    try {
      const res = await fetch(`/bff/security/alerts/${a.id}/${action}`, {
        method: "POST",
        body: JSON.stringify({ note: notes[a.id] ?? "" }),
      });
      const body = await res.json();
      if (!res.ok) setError(body.error ?? `HTTP ${res.status}`);
      else stream.upsert(body);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
        {FAMILIES.map((f) => (
          <button
            key={f.key}
            type="button"
            onClick={() => setFamily(family === f.key ? null : f.key)}
            className={`rounded-lg border px-3 py-2.5 text-left transition ${TONE[f.tone]} ${family === f.key ? "ring-2 ring-accent/40" : ""}`}
          >
            <div className="text-[10px] font-semibold uppercase tracking-wider opacity-80">{f.label}</div>
            <div className="mt-1 flex items-baseline gap-2">
              <span className="text-2xl font-semibold tabular-nums">{counts[f.key].open}</span>
              <span className="text-[11px] opacity-70">open · {counts[f.key].total} total</span>
            </div>
          </button>
        ))}
      </div>

      <div className="grid gap-5 xl:grid-cols-[1fr_380px]">
        <Panel
          icon={ShieldAlert}
          title="Operations & security log"
          subtitle={`Live uld.security stream · ${stream.status === "open" ? "connected" : stream.status}`}
          action={
            <div className="flex gap-1 text-xs">
              {(["OPEN", "ACKNOWLEDGED", "RESOLVED", ""] as const).map((s) => (
                <button
                  key={s || "ALL"}
                  type="button"
                  onClick={() => setStatus(s)}
                  className={`rounded px-2 py-1 ${status === s ? "bg-surface-2 text-ink" : "text-muted hover:text-ink"}`}
                >
                  {s ? s.toLowerCase() : "all"}
                </button>
              ))}
            </div>
          }
        >
          {error && <div className="mb-2 rounded bg-rose-50 dark:bg-rose-500/10 px-3 py-2 text-sm text-rose-700 dark:text-rose-200">{error}</div>}
          <ul className="max-h-[640px] divide-y divide-line overflow-auto">
            {rows.map((a) => {
              const sev = severityStyle(a.severity);
              const expanded = open === a.id;
              return (
                <li key={a.id} className={`py-2.5 ${a.status === "OPEN" && a.kind === "alert" ? "" : "opacity-80"}`}>
                  <div className="flex flex-wrap items-start gap-2">
                    <span className={`rounded px-1.5 py-0.5 text-[10px] font-bold ${sev.cls}`}>{sev.label}</span>
                    <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-2">{a.rule_id}</span>
                    <button type="button" onClick={() => setOpen(expanded ? null : a.id)} className="min-w-0 flex-1 text-left">
                      <div className="text-sm text-ink">{a.rule_name}</div>
                      <div className="truncate text-xs text-muted">{entity(a)}</div>
                    </button>
                    <div className="text-right text-[11px] text-muted">
                      <div>{ago(a.occurred_at, now)}</div>
                      <div className="font-mono">{a.service}</div>
                    </div>
                  </div>
                  <div className="mt-2 flex flex-wrap items-center gap-2 pl-1">
                    <span
                      className={`text-[11px] ${a.status === "OPEN" ? "text-rose-600 dark:text-rose-300" : a.status === "ACKNOWLEDGED" ? "text-amber-700 dark:text-amber-300" : "text-emerald-700 dark:text-emerald-300"}`}
                    >
                      ● {a.status.toLowerCase()}
                      {a.acked_by && a.status !== "OPEN" && ` by ${a.resolved_by ?? a.acked_by}`}
                    </span>
                    {a.status !== "RESOLVED" && (
                      <>
                        <input
                          value={notes[a.id] ?? ""}
                          onChange={(e) => setNotes({ ...notes, [a.id]: e.target.value })}
                          placeholder="note (optional)"
                          maxLength={500}
                          className="h-7 w-40 rounded border border-line bg-surface px-2 text-xs text-ink focus:border-accent focus:outline-none"
                        />
                        {a.status === "OPEN" && (
                          <button type="button" disabled={busy === a.id} onClick={() => transition(a, "ack")} className="btn-ghost !py-0.5 !text-xs">
                            Acknowledge
                          </button>
                        )}
                        <button type="button" disabled={busy === a.id} onClick={() => transition(a, "resolve")} className="btn-primary !py-0.5 !text-xs">
                          Resolve
                        </button>
                      </>
                    )}
                    {a.note && <span className="text-[11px] italic text-muted">“{a.note}”</span>}
                  </div>
                  {expanded && (
                    <pre className="mt-2 max-h-64 overflow-auto rounded-md bg-surface p-3 text-[11px] leading-relaxed text-muted">
                      {JSON.stringify({ reason: a.reason, action: a.action, kind: a.kind, ...a.details }, null, 2)}
                    </pre>
                  )}
                </li>
              );
            })}
            {rows.length === 0 && (
              <li className="py-10 text-center text-sm text-muted">
                No {status ? status.toLowerCase() : ""} events{family ? ` in ${family}` : ""}. Trigger one from the Interactive Sandbox ↓
              </li>
            )}
          </ul>
        </Panel>

        <Panel icon={ClipboardList} title="Audit log" subtitle="Privileged actions taken through the dashboard">
          <ul className="max-h-[700px] space-y-2 overflow-auto text-xs">
            {audit.map((e) => (
              <li key={e.id} className="rounded-md border border-line bg-surface px-3 py-2">
                <div className="flex justify-between gap-2">
                  <span className="font-mono text-sky-700 dark:text-sky-300">{e.action}</span>
                  <span className="text-muted">{ago(e.at, now)}</span>
                </div>
                <div className="mt-0.5 text-ink-2">{e.target}</div>
                <div className="text-muted">
                  {e.actor.replace(/ <.*>$/, "")} · {e.role.replace("_", " ")}
                </div>
              </li>
            ))}
            {audit.length === 0 && <li className="text-muted">No audited actions yet.</li>}
          </ul>
        </Panel>
      </div>
    </div>
  );
}
