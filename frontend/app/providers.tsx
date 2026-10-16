"use client";

import { SessionProvider } from "next-auth/react";

// Session state is resolved client-side (the app uses Cache Components, so
// pages stay static); every privileged call is re-checked on the server.
export function Providers({ children }: { children: React.ReactNode }) {
  return <SessionProvider basePath="/auth">{children}</SessionProvider>;
}
