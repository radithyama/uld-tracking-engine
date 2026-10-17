import { json, mintTicket, requireCapability, requireSession } from "@/lib/bff";

// Single-use ticket for one WebSocket connection, fetched by the browser
// before every (re)connect: ?stream=ops for the live operations feed (any
// signed-in role) or ?stream=security for the SOC stream (auditors only).
export async function GET(req: Request) {
  const stream = new URL(req.url).searchParams.get("stream");
  if (stream !== "ops" && stream !== "security") return json({ error: "unknown stream" }, 400);
  const c = stream === "security" ? await requireCapability("security.view") : await requireSession();
  if (c instanceof Response) return c;
  return json({ ticket: await mintTicket(c) });
}
