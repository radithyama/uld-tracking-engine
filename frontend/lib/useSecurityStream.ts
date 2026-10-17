"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { SecurityAlert } from "./types";
import { fetchTicket, withTicket } from "./wsTicket";

const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8081/ws";

type Msg =
  | { type: "snapshot"; data: { alerts: SecurityAlert[] } }
  | { type: "security_event"; data: SecurityAlert }
  | { type: "security_alert_update"; data: SecurityAlert };

export interface SecurityStream {
  alerts: SecurityAlert[]; // newest first
  status: "off" | "connecting" | "open" | "reconnecting";
  /** Most recent alert-kind event received live (not from the snapshot). */
  latest: SecurityAlert | null;
  upsert: (a: SecurityAlert) => void;
  dismissLatest: () => void;
}

const MAX = 300;

/**
 * Live security events for auditors. A fresh single-use ticket is fetched
 * from the BFF before every (re)connect, since the browser can't send an
 * Authorization header on a WebSocket.
 */
export function useSecurityStream(enabled: boolean): SecurityStream {
  const [alerts, setAlerts] = useState<SecurityAlert[]>([]);
  const [status, setStatus] = useState<SecurityStream["status"]>("off");
  const [latest, setLatest] = useState<SecurityAlert | null>(null);
  const seen = useRef(new Set<string>());

  const upsert = useCallback((a: SecurityAlert) => {
    setAlerts((prev) => {
      const i = prev.findIndex((x) => x.id === a.id);
      if (i >= 0) {
        const next = prev.slice();
        next[i] = a;
        return next;
      }
      return [a, ...prev].slice(0, MAX);
    });
  }, []);

  useEffect(() => {
    if (!enabled) return;
    let ws: WebSocket | null = null;
    let disposed = false;
    let attempt = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const connect = async () => {
      setStatus(attempt === 0 ? "connecting" : "reconnecting");
      const ticket = await fetchTicket("security");
      if (disposed) return;
      if (!ticket) return retry();
      const socket = new WebSocket(withTicket(`${WS_URL}/security`, ticket));
      ws = socket;
      socket.onopen = () => {
        attempt = 0;
        setStatus("open");
      };
      socket.onmessage = (ev) => {
        let msg: Msg;
        try {
          msg = JSON.parse(ev.data as string);
        } catch {
          return;
        }
        if (msg.type === "snapshot") {
          for (const a of msg.data.alerts ?? []) seen.current.add(a.id);
          setAlerts(msg.data.alerts ?? []);
        } else if (msg.type === "security_event") {
          const fresh = !seen.current.has(msg.data.id);
          seen.current.add(msg.data.id);
          upsert(msg.data);
          if (fresh && msg.data.kind === "alert") setLatest(msg.data);
        } else if (msg.type === "security_alert_update") {
          upsert(msg.data);
        }
      };
      socket.onclose = () => !disposed && retry();
      socket.onerror = () => socket.close();
    };
    const retry = () => {
      attempt += 1;
      setStatus("reconnecting");
      const base = Math.min(30_000, 1000 * 2 ** Math.min(attempt, 5));
      timer = setTimeout(connect, base / 2 + Math.random() * (base / 2));
    };

    void connect();
    return () => {
      disposed = true;
      clearTimeout(timer);
      ws?.close();
      setStatus("off");
    };
  }, [enabled, upsert]);

  return { alerts, status, latest, upsert, dismissLatest: () => setLatest(null) };
}
