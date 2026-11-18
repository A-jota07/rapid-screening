export type StandardStatus =
  | 'POSTED'
  | 'IN_TRANSIT'
  | 'AT_CDD'
  | 'OUT_FOR_DELIVERY'
  | 'DELIVERED'
  | 'EXCEPTION';

export type FacilityType = 'ORIGIN' | 'HUB' | 'CDD' | 'DESTINATION';

export interface CarrierInfo {
  code: string;
  name: string;
  track17CarrierId?: number;
  trackingMoreCode?: string;
  detectedBy: string; // "regex" | "aggregator" | "heuristic"
  confidence: number;
  patternDescription: string;
  regexPattern?: string;
  sampleCodes?: string[];
}

export interface GeoPoint {
  lat: number;
  lng: number;
  city: string;
  state: string;
  address: string;
  zipCode: string;
  facilityType: FacilityType;
  facilityName: string;
}

export interface CDDAllocation {
  name: string;
  carrierName: string;
  address: string;
  city: string;
  state: string;
  zipCode: string;
  phone: string;
  openingHours: string;
  lat: number;
  lng: number;
  availableForPickup: boolean;
  instructions: string;
}

export interface TrackingEvent {
  timestamp: string;
  rawStatus: string;
  standardStatus: StandardStatus;
  standardLabel: string;
  description: string;
  location: string;
  coordinates?: GeoPoint;
}

export interface TrackingResult {
  trackingCode: string;
  carrier: CarrierInfo;
  currentStatus: StandardStatus;
  currentStatusLabel: string;
  progressStep: number; // 1..4
  estimatedDelivery: string;
  origin?: GeoPoint;
  destination?: GeoPoint;
  currentLocation?: GeoPoint;
  cdd?: CDDAllocation;
  routePoints: GeoPoint[];
  events: TrackingEvent[];
  providerUsed: string;
  cached: boolean;
  cacheExpiresAt: string;
  lastUpdatedAt: string;
}

export interface ProviderHealth {
  name: string;
  enabled: boolean;
  configured: boolean;
  available: boolean;
  circuitOpen: boolean;
  circuitUntil?: string;
  creditsRemaining: number; // -1 for unlimited/free
  requestsUsed: number;
  failures: number;
  lastError?: string;
  lastLatencyMs: number;
  mode: string; // "free" | "api_key" | "local_fallback"
}

export interface CacheStats {
  hits: number;
  misses: number;
  storedCount: number;
  creditsSaved: number;
  backendMode: string;
  cooldownSeconds: number;
}

export interface HealthResponse {
  status: string;
  service: string;
  timestamp: string;
  cache: CacheStats;
  providers: ProviderHealth[];
}

export interface CarriersResponse {
  carriers: CarrierInfo[];
}

export interface RecentTrackingSummary {
  trackingCode: string;
  carrierCode: string;
  carrierName: string;
  currentStatus: StandardStatus;
  currentStatusLabel: string;
  providerUsed: string;
  lastUpdatedAt: string;
  cddName?: string;
}

export interface RecentTrackingsResponse {
  items: RecentTrackingSummary[];
}

export interface UserCoordinates {
  lat: number;
  lng: number;
  accuracyMeters?: number;
  simulated: boolean;
  label: string;
}
