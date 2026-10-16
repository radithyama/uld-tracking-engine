import { realtime, requireCapability } from "@/lib/bff";

export async function GET(req: Request) {
  const c = await requireCapability("security.view");
  if (c instanceof Response) return c;
  const q = new URL(req.url).searchParams;
  const params = new URLSearchParams({ status: q.get("status") ?? "", limit: q.get("limit") ?? "150" });
  return realtime(c, `/security/alerts?${params}`);
}
