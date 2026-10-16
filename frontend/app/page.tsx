"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useSession } from "next-auth/react";
import { useUldStream } from "@/lib/useUldStream";
import { useSecurityStream } from "@/lib/useSecurityStream";
import { can, ROLE_INFO, type Role, type Tab } from "@/lib/roles";
import type { FlightLoad } from "@/lib/types";
import { TopBar } from "@/components/TopBar";
import { OperationsView } from "@/components/OperationsView";
import { FlightsView } from "@/components/FlightsView";
import { NetworkView } from "@/components/NetworkView";
import { SecurityView, severityStyle } from "@/components/SecurityView";
import { SandboxToolbar } from "@/components/SandboxToolbar";
import { DeckModal } from "@/components/DeckModal";

const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8081/ws";

export default function Page() {
  const { data: session, status } = useSession();
  const router = useRouter();
  const role = session?.user?.role;

  useEffect(() => {
    if (status === "unauthenticated") router.replace("/signin");
  }, [status, router]);

  if (status !== "authenticated" || !role) {
    return <div className="grid min-h-screen place-items-center text-sm text-slate-500">Loading control room…</div>;
  }
  return <ControlRoom role={role} name={session.user?.name ?? ""} />;
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

function ControlRoom({ role, name }: { role: Role; name: string }) {
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
  const chooseStation = (s: string) => {
    setStation(s);
    pref.set("uld.station", s);
  };
  const chooseTab = (t: Tab) => {
    setTab(t);
    pref.set("uld.tab", t);
  };

  const ulds = useMemo(() => Object.values(stream.ulds), [stream.ulds]);
  const flights = useMemo(() => Object.values(stream.flights), [stream.flights]);
  const stations = stream.stations.length ? stream.stations : [{ iata: "CGK", name: "Soekarno-Hatta", country: "ID", lat: -6.1, lon: 106.7 }];
  const openSecurity = security.alerts.filter((a) => a.status === "OPEN" && a.kind === "alert").length;
  const openDeck = can(role, "loadplan.view") ? setDeck : undefined;

  return (
    <>
      <TopBar
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
      />

      {security.latest && tab !== "security" && (
        <div className="mx-auto mt-4 w-full max-w-[1600px] px-4 sm:px-6">
          <div className="flex items-center gap-3 rounded-lg border border-red-500/60 bg-red-500/15 px-4 py-2.5 text-sm text-red-100" role="alert">
            <span className={`rounded px-1.5 py-0.5 text-[10px] font-bold ${severityStyle(security.latest.severity).cls}`}>
              {security.latest.rule_id}
            </span>
            <span className="flex-1">
              {security.latest.rule_name}
              {security.latest.uld_id && <span className="font-mono text-red-200/80"> · {security.latest.uld_id}</span>}
            </span>
            <button type="button" onClick={() => chooseTab("security")} className="text-xs font-medium underline">
              Triage
            </button>
            <button type="button" onClick={security.dismissLatest} aria-label="Dismiss" className="text-red-200/70 hover:text-white">
              ✕
            </button>
          </div>
        </div>
      )}

      <main className="mx-auto w-full max-w-[1600px] flex-1 px-4 pb-28 pt-5 sm:px-6">
        {tab === "operations" && (
          <OperationsView
            station={station}
            ulds={ulds}
            locations={stream.locations}
            flights={flights}
            manifest={can(role, "view.manifest")}
            onOpenFlight={openDeck}
          />
        )}
        {tab === "flights" && (
          <FlightsView station={station} flights={flights} ulds={ulds} timeScale={stream.timeScale} onOpen={(f) => setDeck(f)} />
        )}
        {tab === "network" && (
          <NetworkView stations={stations} flights={flights} ulds={ulds} station={station} timeScale={stream.timeScale} onStation={chooseStation} />
        )}
        {tab === "security" && can(role, "security.view") && <SecurityView stream={security} />}
      </main>

      <SandboxToolbar role={role} station={station} ulds={ulds} locations={stream.locations} flights={flights} />

      {deck && (
        <DeckModal
          flightId={deck.id}
          planVersion={stream.planVersions[deck.id] ?? 0}
          canEdit={can(role, "loadplan.edit")}
          onClose={() => setDeck(null)}
        />
      )}
    </>
  );
}
