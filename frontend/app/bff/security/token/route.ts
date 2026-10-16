import { json, mintToken, requireCapability } from "@/lib/bff";

// Short-lived token for the browser's /ws/security connection (WebSockets
// can't carry an Authorization header, so it goes in the query string). The
// client fetches a fresh one on every (re)connect.
export async function GET() {
  const c = await requireCapability("security.view");
  if (c instanceof Response) return c;
  return json({ token: await mintToken(c, 60) });
}
