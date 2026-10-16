import { realtime, requireCapability } from "@/lib/bff";

export async function GET() {
  const c = await requireCapability("security.view");
  if (c instanceof Response) return c;
  return realtime(c, "/audit?limit=100");
}
