"use client";

import { useCallback, useSyncExternalStore } from "react";

export type Theme = "light" | "dark";
const KEY = "uld.theme";

/**
 * Runs inline in <head> before first paint so the saved theme applies
 * without a light/dark flash. Light is the default; an explicit choice is
 * remembered per browser.
 */
export const themeBootstrapScript = `(function(){try{var t=localStorage.getItem("${KEY}");document.documentElement.dataset.theme=t==="dark"?"dark":"light"}catch(e){document.documentElement.dataset.theme="light"}})();`;

const listeners = new Set<() => void>();

function current(): Theme {
  return document.documentElement.dataset.theme === "dark" ? "dark" : "light";
}

export function useTheme(): [Theme, (t: Theme) => void] {
  const theme = useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    current,
    () => "light" as Theme,
  );
  const set = useCallback((t: Theme) => {
    const root = document.documentElement;
    root.classList.add("theme-transition");
    root.dataset.theme = t;
    try {
      localStorage.setItem(KEY, t);
    } catch {
      /* storage unavailable: theme still applies for this page view */
    }
    listeners.forEach((l) => l());
    setTimeout(() => root.classList.remove("theme-transition"), 250);
  }, []);
  return [theme, set];
}
