"use client";

import { useEffect, useReducer } from "react";
import type {
  ConnectionStatus,
  FlightLoad,
  ServerMessage,
  ULD,
  WarehouseLocation,
} from "./types";

export interface StreamState {
  ulds: Record<string, ULD>;
  flights: Record<string, FlightLoad>;
  locations: WarehouseLocation[];
  status: ConnectionStatus;
  /** Total messages applied since page load (for the throughput meter). */
  messageCount: number;
  lastMessageAt: number | null;
}

type Action =
  | { type: "messages"; msgs: ServerMessage[]; at: number }
  | { type: "status"; status: ConnectionStatus };

const initialState: StreamState = {
  ulds: {},
  flights: {},
  locations: [],
  status: "connecting",
  messageCount: 0,
  lastMessageAt: null,
};

// Every server message carries complete state plus a monotonic version, so
// merging is "highest version wins". This makes duplicate or reordered
// delivery (e.g. an update that races the initial snapshot) harmless.
function upsert<T extends { version: number }>(
  into: Record<string, T>,
  key: string,
  next: T,
) {
  const cur = into[key];
  if (!cur || next.version >= cur.version) into[key] = next;
}

function reducer(state: StreamState, action: Action): StreamState {
  switch (action.type) {
    case "status":
      return { ...state, status: action.status };

    case "messages": {
      const ulds = { ...state.ulds };
      const flights = { ...state.flights };
      let locations = state.locations;
      for (const msg of action.msgs) {
        switch (msg.type) {
          case "snapshot":
            locations = msg.data.locations ?? locations;
            for (const u of msg.data.ulds ?? []) upsert(ulds, u.id, u);
            for (const f of msg.data.flights ?? []) upsert(flights, f.flight_number, f);
            break;
          case "uld_update":
            upsert(ulds, msg.data.id, msg.data);
            break;
          case "flight_update":
            upsert(flights, msg.data.flight_number, msg.data);
            break;
        }
      }
      return {
        ...state,
        ulds,
        flights,
        locations,
        messageCount: state.messageCount + action.msgs.length,
        lastMessageAt: action.at,
      };
    }
  }
}

/** Interval at which buffered socket messages are flushed into React state. */
const FLUSH_MS = 100;

/**
 * Subscribes to the realtime WebSocket. Messages are buffered and applied in
 * batches so a burst of thousands of scans costs a handful of renders, not
 * thousands. Reconnects with capped exponential backoff + jitter; the server
 * sends a fresh snapshot on every (re)connect.
 */
export function useUldStream(url: string): StreamState {
  const [state, dispatch] = useReducer(reducer, initialState);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let attempt = 0;
    let disposed = false;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let flushTimer: ReturnType<typeof setTimeout> | undefined;
    let queue: ServerMessage[] = [];

    const flush = () => {
      flushTimer = undefined;
      if (queue.length === 0) return;
      const msgs = queue;
      queue = [];
      dispatch({ type: "messages", msgs, at: Date.now() });
    };

    const connect = () => {
      dispatch({ type: "status", status: attempt === 0 ? "connecting" : "reconnecting" });
      const socket = new WebSocket(url);
      ws = socket;

      socket.onopen = () => {
        attempt = 0;
        dispatch({ type: "status", status: "open" });
      };

      socket.onmessage = (ev) => {
        try {
          queue.push(JSON.parse(ev.data as string) as ServerMessage);
        } catch {
          return; // ignore malformed frames
        }
        // setTimeout (not rAF) so the queue still drains in background tabs.
        flushTimer ??= setTimeout(flush, FLUSH_MS);
      };

      socket.onclose = () => {
        if (disposed) return;
        attempt += 1;
        dispatch({ type: "status", status: "reconnecting" });
        const base = Math.min(30_000, 500 * 2 ** Math.min(attempt, 6));
        reconnectTimer = setTimeout(connect, base / 2 + Math.random() * (base / 2));
      };

      socket.onerror = () => socket.close();
    };

    connect();

    return () => {
      disposed = true;
      clearTimeout(reconnectTimer);
      clearTimeout(flushTimer);
      ws?.close();
    };
  }, [url]);

  return state;
}
