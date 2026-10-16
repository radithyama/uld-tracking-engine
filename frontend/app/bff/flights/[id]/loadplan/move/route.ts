import { json, realtime, requireCapability } from "@/lib/bff";

export async function POST(req: Request, ctx: { params: Promise<{ id: string }> }) {
  const c = await requireCapability("loadplan.edit");
  if (c instanceof Response) return c;
  const { id } = await ctx.params;
  const body = (await req.json().catch(() => null)) as { uld_id?: unknown; position?: unknown } | null;
  if (typeof body?.uld_id !== "string" || typeof body.position !== "string") {
    return json({ error: "uld_id and position (string) required" }, 400);
  }
  return realtime(c, `/flights/${encodeURIComponent(id)}/loadplan/move`, {
    method: "POST",
    body: JSON.stringify({ uld_id: body.uld_id, position: body.position }),
  });
}
