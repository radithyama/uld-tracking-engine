import type { Metadata, Viewport } from "next";
import { headers } from "next/headers";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";
import { Providers } from "./providers";
import { themeBootstrapScript } from "@/lib/theme";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "ULDPulse · ULD Operations Control",
  description: "Real-time air cargo ULD tracking, load planning and SOC security telemetry",
  applicationName: "ULDPulse",
  appleWebApp: { title: "ULDPulse" },
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f8fafc" },
    { media: "(prefers-color-scheme: dark)", color: "#020617" },
  ],
};

export default async function RootLayout({ children }: LayoutProps<"/">) {
  // Per-request CSP nonce from proxy.ts; the inline theme script needs it.
  const nonce = (await headers()).get("x-nonce") ?? undefined;
  return (
    // data-theme is set by the bootstrap script before hydration.
    <html lang="en" data-theme="light" suppressHydrationWarning className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}>
      <head>
        {/* A fixed, build-time constant (no user input); runs before paint to avoid a theme flash. */}
        {/* eslint-disable-next-line react/no-danger */}
        <script nonce={nonce} dangerouslySetInnerHTML={{ __html: themeBootstrapScript }} />
      </head>
      <body className="flex min-h-full flex-col">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
