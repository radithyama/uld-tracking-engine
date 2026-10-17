"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useSession } from "next-auth/react";
import { ShieldAlert, WifiOff, X } from "lucide-react";
import { useUldStream } from "@/lib/useUldStream";
import { useSecurityStream } from "@/lib/useSecurityStream";
import { can, ROLE_INFO, type Role, type Tab } from "@/lib/roles";
import { VIEWS } from "@/lib/nav";
import type { FlightLoad } from "@/lib/types";
import { Navbar } from "@/components/Navbar";
import { NavDrawer } from "@/components/NavDrawer";
import { GuideModal } from "@/components/GuideModal";
import { OperationsView } from "@/components/OperationsView";
import { FlightsView } from "@/components/FlightsView";
import { NetworkMap } from "@/components/NetworkMap";
import { SecurityView, severityStyle } from "@/components/SecurityView";
import { SandboxToolbar } from "@/components/SandboxToolbar";
import { DeckModal } from "@/components/DeckModal";
import { DashboardSkeleton } from "@/components/Panel";
import { TrafficGenerator } from "@/components/TrafficGenerator";

const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8081/ws";

export default function Page() {
  const { data: session, status } = useSession();
  const router = useRouter();
  const role = session?.user?.role;

  useEffect(() => {
    if (status === "unauthenticated") router.replace("/signin");
  }, [status, router]);

  if (status !== "authenticated" || !role) {
    return (
      <div className="mx-auto w-full max-w-[1600px] px-4 pt-20 sm:px-6">
        <DashboardSkeleton />
      </div>
    );
  }
  return (
    <ControlRoom
      role={role}
      name={session.user?.name ?? ""}
      demoSession={(session.user?.email ?? "").endsWith("@demo.uldpulse")}
    />
  );
}

/** Messages/second, sampled once a second from the running total. */
function useRate(total: number): number {
  const totalRef = useRef(total);
  const [rate, setRate] = useState(0);
  useEffect(() => {
    totalRef.current = total;
  }, [total]);
  useEffect(() => {
    let prev = totalRef.current;
    const t = setInterval(() => {
      setRate(totalRef.current - prev);
      prev = totalRef.current;
    }, 1000);
    return () => clearInterval(t);
  }, []);
  return rate;
}

// Per-viewer UI preferences; storage can be unavailable (private mode).
const pref = {
  get: (k: string) => {
    try {
      return localStorage.getItem(k);
    } catch {
      return null;
    }
  },
  set: (k: string, v: string) => {
    try {
      localStorage.setItem(k, v);
    } catch {
      /* ignore */
    }
  },
};

function ControlRoom({ role, name, demoSession }: { role: Role; name: string; demoSession: boolean }) {
  const stream = useUldStream(WS_URL);
  const security = useSecurityStream(can(role, "security.view"));
  const rate = useRate(stream.messageCount);
  const tabs = ROLE_INFO[role].tabs;

  // ControlRoom only renders client-side (after the session resolves), so
  // reading saved preferences in the initializers can't cause a hydration
  // mismatch.
  const [station, setStation] = useState(() => pref.get("uld.station") ?? "CGK");
  const [tab, setTab] = useState<Tab>(() => {
    const t = pref.get("uld.tab") as Tab | null;
    return t && tabs.includes(t) ? t : "operations";
  });
  const [deck, setDeck] = useState<FlightLoad | null>(null);
  const [drawer, setDrawer] = useState(false);
  // The guide opens once automatically for first-time visitors.
  const [guide, setGuide] = useState(() => !pref.get("uld.guide.seen"));

  const chooseStation = (s: string) => {
    setStation(s);
    pref.set("uld.station", s);
  };
  const chooseTab = (t: Tab) => {
    setTab(t);
    pref.set("uld.tab", t);
  };
  const closeGuide = useCallback(() => {
    setGuide(false);
    pref.set("uld.guide.seen", "1");
  }, []);
  const closeDrawer = useCallback(() => setDrawer(false), []);

  const ulds = useMemo(() => Object.values(stream.ulds), [stream.ulds]);
  const flights = useMemo(() => Object.values(stream.flights), [stream.flights]);
  const stations = stream.stations.length ? stream.stations : [{ iata: "CGK", name: "Soekarno-Hatta", country: "ID", lat: -6.1, lon: 106.7 }];
  const openSecurity = security.alerts.filter((a) => a.status === "OPEN" && a.kind === "alert").length;
  const openDeck = can(role, "loadplan.view") ? setDeck : undefined;
  const hasData = stream.locations.length > 0;

  const openNextDeparture = () => {
    const next = flights
      .filter((f) => f.status === "SCHEDULED" && f.uld_count > 0)
      .sort((a, b) => Number(b.origin === station) - Number(a.origin === station) || a.departure.localeCompare(b.departure))[0];
    if (!next) return false;
    if (tabs.includes("flights")) chooseTab("flights");
    setDeck(next);
    return true;
  };

  const View = VIEWS[tab];

  return (
    <>
      <Navbar
        stations={stations}
        station={station}
        onStation={chooseStation}
        tabs={tabs}
        tab={tab}
        onTab={chooseTab}
        role={role}
        name={name}
        status={stream.status}
        rate={rate}
        openSecurity={openSecurity}
        onMenu={() => setDrawer(true)}
        onGuide={() => setGuide(true)}
      />

      <main className="mx-auto w-full max-w-[1600px] flex-1 px-4 pb-32 pt-5 sm:px-6">
        <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
          <div>
            <div className="text-xs font-medium text-muted">
              {stations.find((s) => s.iata === station)?.name ?? station} · {station}
            </div>
            <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight text-ink">
              <View.icon size={20} className="text-accent" />
              {View.label}
            </h1>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-3">
            <p className="hidden max-w-xs text-right text-xs text-muted xl:block">{View.blurb}</p>
            {can(role, "sandbox.generator") && <TrafficGenerator />}
          </div>
        </div>

        {stream.status === "reconnecting" && hasData && (
          <div className="mb-4 flex items-center gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
            <WifiOff size={15} /> Reconnecting to the live stream… figures below may be momentarily stale.
          </div>
        )}

        {security.latest && tab !== "security" && (
          <div className="fade-in mb-4 flex items-center gap-3 rounded-xl border border-rose-200 bg-rose-50 px-4 py-2.5 text-sm text-rose-800 shadow-xs dark:border-rose-500/40 dark:bg-rose-500/10 dark:text-rose-200" role="alert">
            <ShieldAlert size={17} className="shrink-0 text-rose-600 dark:text-rose-300" />
            <span className={`rounded px-1.5 py-0.5 text-[10px] font-bold ${severityStyle(security.latest.severity).cls}`}>{security.latest.rule_id}</span>
            <span className="min-w-0 flex-1 truncate">
              {security.latest.rule_name}
              {security.latest.uld_id && <span className="font-mono opacity-80"> · {security.latest.uld_id}</span>}
            </span>
            <button type="button" onClick={() => chooseTab("security")} className="text-xs font-semibold underline-offset-2 hover:underline">
              Triage
            </button>
            <button type="button" onClick={security.dismissLatest} aria-label="Dismiss" className="opacity-60 hover:opacity-100">
              <X size={15} />
            </button>
          </div>
        )}

        {!hasData ? (
          <DashboardSkeleton />
        ) : (
          // Re-keyed on view + hub so switching fades the new content in.
          <div key={`${tab}:${station}`} className="fade-in">
            {tab === "operations" && (
              <OperationsView station={station} ulds={ulds} locations={stream.locations} flights={flights} manifest={can(role, "view.manifest")} onOpenFlight={openDeck} />
            )}
            {tab === "flights" && <FlightsView station={station} flights={flights} ulds={ulds} timeScale={stream.timeScale} onOpen={(f) => setDeck(f)} />}
            {tab === "network" && (
              <NetworkMap stations={stations} flights={flights} ulds={ulds} station={station} timeScale={stream.timeScale} onStation={chooseStation} />
            )}
            {tab === "security" && can(role, "security.view") && <SecurityView stream={security} />}
          </div>
        )}
      </main>

      <SandboxToolbar role={role} station={station} ulds={ulds} locations={stream.locations} flights={flights} />

      <NavDrawer
        open={drawer}
        onClose={closeDrawer}
        stations={stations}
        station={station}
        onStation={chooseStation}
        ulds={ulds}
        tabs={tabs}
        tab={tab}
        onTab={chooseTab}
        role={role}
        name={name}
        demoSession={demoSession}
        onGuide={() => setGuide(true)}
      />

      <GuideModal
        open={guide}
        onClose={closeGuide}
        role={role}
        demoSession={demoSession}
        ulds={ulds}
        onTab={chooseTab}
        onOpenNextDeparture={openNextDeparture}
      />

      {deck && (
        <DeckModal flightId={deck.id} planVersion={stream.planVersions[deck.id] ?? 0} canEdit={can(role, "loadplan.edit")} onClose={() => setDeck(null)} />
      )}
    </>
  );
}
