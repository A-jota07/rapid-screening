import type { UserCoordinates } from '../types/tracking';

const EARTH_RADIUS_KM = 6371;

function toRadians(degrees: number): number {
  return (degrees * Math.PI) / 180;
}

/**
 * Computes great-circle distance in kilometers between two geographic coordinates.
 */
export function haversineDistanceKm(
  lat1: number,
  lng1: number,
  lat2: number,
  lng2: number,
): number {
  const dLat = toRadians(lat2 - lat1);
  const dLng = toRadians(lng2 - lng1);

  const a =
    Math.sin(dLat / 2) * Math.sin(dLat / 2) +
    Math.cos(toRadians(lat1)) *
      Math.cos(toRadians(lat2)) *
      Math.sin(dLng / 2) *
      Math.sin(dLng / 2);

  const c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a));
  return EARTH_RADIUS_KM * c;
}

export function formatDistanceKm(distanceKm: number): string {
  if (distanceKm < 1) {
    const meters = Math.max(50, Math.round(distanceKm * 1000));
    return `${meters} m`;
  }
  return `${distanceKm.toLocaleString('pt-BR', {
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  })} km`;
}

export function formatDurationMinutes(totalMinutes: number): string {
  const rounded = Math.max(1, Math.round(totalMinutes));
  if (rounded < 60) {
    return `${rounded} min`;
  }
  const hours = Math.floor(rounded / 60);
  const mins = rounded % 60;
  return mins > 0 ? `${hours}h ${mins}min` : `${hours}h`;
}

export interface TravelEstimate {
  distanceKm: number;
  distanceLabel: string;
  carLabel: string;
  walkLabel: string;
}

export function estimateTravelTimes(
  fromLat: number,
  fromLng: number,
  toLat: number,
  toLng: number,
): TravelEstimate {
  const distanceKm = haversineDistanceKm(fromLat, fromLng, toLat, toLng);
  // Urban routing factor ~1.3x straight line + realistic average speeds
  const roadDistanceKm = distanceKm * 1.28;
  const avgCarSpeedKmH = roadDistanceKm <= 15 ? 26 : 46;
  const carMinutes = (roadDistanceKm / avgCarSpeedKmH) * 60 + 2;
  const walkMinutes = (roadDistanceKm / 4.6) * 60;

  return {
    distanceKm,
    distanceLabel: formatDistanceKm(distanceKm),
    carLabel: formatDurationMinutes(carMinutes),
    walkLabel: formatDurationMinutes(walkMinutes),
  };
}

export interface ExternalMapLinks {
  googleMapsUrl: string;
  wazeUrl: string;
  openStreetMapUrl: string;
}

export function buildExternalMapLinks(lat: number, lng: number): ExternalMapLinks {
  const latStr = lat.toFixed(6);
  const lngStr = lng.toFixed(6);

  return {
    googleMapsUrl: `https://www.google.com/maps/dir/?api=1&destination=${latStr},${lngStr}`,
    wazeUrl: `https://waze.com/ul?ll=${latStr},${lngStr}&navigate=yes`,
    openStreetMapUrl: `https://www.openstreetmap.org/?mlat=${latStr}&mlon=${lngStr}#map=16/${latStr}/${lngStr}`,
  };
}

/**
 * Creates a realistic nearby starting point (~2.5 km away) for testing GPS pickup
 * routing when browser geolocation is blocked or unavailable.
 */
export function generateSimulatedNearbyPoint(
  targetLat: number,
  targetLng: number,
): UserCoordinates {
  return {
    lat: Number((targetLat - 0.0175).toFixed(6)),
    lng: Number((targetLng + 0.0145).toFixed(6)),
    simulated: true,
    label: 'Ponto de partida simulado (~2,5 km do CDD)',
  };
}
