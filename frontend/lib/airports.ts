import type { Station } from "@/lib/types";

/**
 * Real-world airport positions for the map, [latitude, longitude] (airport
 * reference points). The backend's station records drive everything else;
 * these only decide where each station is drawn.
 */
export const AIRPORT_COORDS: Record<string, [lat: number, lon: number]> = {
  CGK: [-6.1256, 106.6558], // Jakarta Soekarno-Hatta
  SIN: [1.3644, 103.9915], // Singapore Changi
  MEL: [-37.669, 144.841], // Melbourne Tullamarine
  SYD: [-33.9461, 151.1772], // Sydney Kingsford Smith
  NRT: [35.7719, 140.3928], // Tokyo Narita
  DPS: [-8.7482, 115.1678], // Bali Ngurah Rai
};

/** [longitude, latitude], the order d3-geo and react-simple-maps expect. */
export function lonLat(s: Pick<Station, "iata" | "lat" | "lon">): [number, number] {
  const c = AIRPORT_COORDS[s.iata];
  return c ? [c[1], c[0]] : [s.lon, s.lat];
}
