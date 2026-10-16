import NextAuth from "next-auth";
import Credentials from "next-auth/providers/credentials";
import Google from "next-auth/providers/google";
import type { Provider } from "next-auth/providers";
import { isRole, ROLE_INFO, type Role } from "@/lib/roles";

// Google sign-in is enabled only when credentials are configured
// (AUTH_GOOGLE_ID / AUTH_GOOGLE_SECRET). Google users get a role from
// GOOGLE_ROLE_MAP ("alice@x.com:loadmaster,bob@y.com:security_auditor"),
// falling back to GOOGLE_DEFAULT_ROLE (ground_handler).
export const googleEnabled = !!(process.env.AUTH_GOOGLE_ID && process.env.AUTH_GOOGLE_SECRET);

function googleRole(email: string | null | undefined): Role {
  const map = Object.fromEntries(
    (process.env.GOOGLE_ROLE_MAP ?? "")
      .split(",")
      .map((pair) => pair.trim().split(":"))
      .filter(([e, r]) => e && isRole(r))
      .map(([e, r]) => [e.toLowerCase(), r as Role]),
  );
  const fallback = process.env.GOOGLE_DEFAULT_ROLE;
  return map[(email ?? "").toLowerCase()] ?? (isRole(fallback) ? fallback : "ground_handler");
}

const providers: Provider[] = [
  // One-click demo accounts so reviewers can explore every role without
  // signing up. Disable with DEMO_LOGIN=off.
  Credentials({
    id: "demo",
    name: "Demo account",
    credentials: { role: { label: "Role", type: "text" } },
    authorize: async (creds) => {
      if (process.env.DEMO_LOGIN === "off" || !isRole(creds?.role)) return null;
      const role = creds.role;
      return { id: `demo-${role}`, name: `Demo ${ROLE_INFO[role].label}`, email: `${role}@demo.uldpulse`, role };
    },
  }),
];
if (googleEnabled) providers.push(Google);

export const { handlers, auth, signIn, signOut } = NextAuth({
  // nginx routes /api/* to the Go ingest service, so Auth.js lives at /auth.
  basePath: "/auth",
  trustHost: true,
  session: { strategy: "jwt", maxAge: 8 * 60 * 60 },
  pages: { signIn: "/signin" },
  providers,
  callbacks: {
    jwt({ token, user, account }) {
      if (user) {
        token.role = account?.provider === "google" ? googleRole(user.email) : (user as { role?: Role }).role;
      }
      return token;
    },
    session({ session, token }) {
      if (isRole(token.role)) session.user.role = token.role;
      return session;
    },
  },
});
