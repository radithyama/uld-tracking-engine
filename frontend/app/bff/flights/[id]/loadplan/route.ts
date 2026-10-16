import { realtime, requireCapability } from "@/lib/bff";

export async function GET(_req: Request, ctx: { params: Promise<{ id: string }> }) {
  const c = await requireCapability("loadplan.view");
  if (c instanceof Response) return c;
  const { id } = await ctx.params;
  return realtime(c, `/flights/${encodeURIComponent(id)}/loadplan`);
}
