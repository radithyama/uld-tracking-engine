import { json, realtime, requireCapability } from "@/lib/bff";

export async function POST(req: Request, ctx: { params: Promise<{ id: string; action: string }> }) {
  const c = await requireCapability("security.view");
  if (c instanceof Response) return c;
  const { id, action } = await ctx.params;
  if (action !== "ack" && action !== "resolve") return json({ error: "unknown action" }, 404);
  const { note } = (await req.json().catch(() => ({}))) as { note?: unknown };
  return realtime(c, `/security/alerts/${encodeURIComponent(id)}/${action}`, {
    method: "POST",
    body: JSON.stringify({ note: typeof note === "string" ? note.slice(0, 500) : "" }),
  });
}
