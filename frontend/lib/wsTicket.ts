// Fetches a single-use WebSocket ticket from the dashboard server. Returns
// null when the user isn't allowed or the server is unreachable; callers
// retry with backoff.
export async function fetchTicket(stream: "ops" | "security"): Promise<string | null> {
  try {
    const res = await fetch(`/bff/ws/ticket?stream=${stream}`, { cache: "no-store" });
    if (!res.ok) return null;
    const { ticket } = (await res.json()) as { ticket?: string };
    return ticket ?? null;
  } catch {
    return null;
  }
}

export const withTicket = (url: string, ticket: string) => `${url}${url.includes("?") ? "&" : "?"}ticket=${encodeURIComponent(ticket)}`;
