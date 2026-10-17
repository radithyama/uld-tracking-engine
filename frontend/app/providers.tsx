"use client";

import { SessionProvider } from "next-auth/react";

// Session state is resolved client-side; signed-out page views are already
// redirected by proxy.ts, and every privileged call is re-checked on the server.
export function Providers({ children }: { children: React.ReactNode }) {
  return <SessionProvider basePath="/auth">{children}</SessionProvider>;
}
