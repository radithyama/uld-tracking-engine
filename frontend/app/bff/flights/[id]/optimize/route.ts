import { realtime, requireCapability } from "@/lib/bff";

export async function POST(req: Request, ctx: { params: Promise<{ id: string }> }) {
  const c = await requireCapability("loadplan.edit");
  if (c instanceof Response) return c;
  const { id } = await ctx.params;
  const { apply } = (await req.json().catch(() => ({}))) as { apply?: boolean };
  return realtime(c, `/flights/${encodeURIComponent(id)}/optimize`, {
    method: "POST",
    body: JSON.stringify({ apply: apply === true }),
  });
}
