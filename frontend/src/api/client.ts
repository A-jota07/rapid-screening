import type {
  CarrierInfo,
  CarriersResponse,
  HealthResponse,
  RecentTrackingsResponse,
  TrackingResult,
} from '../types/tracking';

export class ApiError extends Error {
  status: number;
  retryAfterSeconds?: number;

  constructor(message: string, status: number, retryAfterSeconds?: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

interface RawErrorPayload {
  error?: string;
  message?: string;
  retryAfterSeconds?: number;
  cooldownRemaining?: number;
  retry_after_seconds?: number;
}

async function requestJson<T>(url: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(url, {
    method: 'GET',
    headers: {
      Accept: 'application/json',
    },
    signal,
  });

  if (!response.ok) {
    let message = `Erro ao consultar servidor (${response.status}).`;
    let retryAfterSeconds: number | undefined;

    const retryHeader = response.headers.get('Retry-After');
    if (retryHeader) {
      const parsed = Number.parseInt(retryHeader, 10);
      if (!Number.isNaN(parsed) && parsed > 0) {
        retryAfterSeconds = parsed;
      }
    }

    try {
      const payload = (await response.json()) as RawErrorPayload;
      if (payload.message) {
        message = payload.message;
      } else if (payload.error) {
        message = payload.error;
      }
      const payloadRetry =
        payload.retryAfterSeconds ??
        payload.cooldownRemaining ??
        payload.retry_after_seconds;
      if (typeof payloadRetry === 'number' && payloadRetry > 0) {
        retryAfterSeconds = payloadRetry;
      }
    } catch {
      // Ignore non-JSON error body
    }

    throw new ApiError(message, response.status, retryAfterSeconds);
  }

  return (await response.json()) as T;
}

export async function fetchHealth(signal?: AbortSignal): Promise<HealthResponse> {
  return requestJson<HealthResponse>('/api/v1/health', signal);
}

export async function fetchCarriers(signal?: AbortSignal): Promise<CarrierInfo[]> {
  const data = await requestJson<CarriersResponse>('/api/v1/carriers', signal);
  return Array.isArray(data.carriers) ? data.carriers : [];
}

export async function detectCarrier(
  code: string,
  signal?: AbortSignal,
): Promise<CarrierInfo> {
  const encoded = encodeURIComponent(code.trim().toUpperCase());
  const raw = await requestJson<CarrierInfo | { carrier: CarrierInfo }>(
    `/api/v1/carriers/detect?code=${encoded}`,
    signal,
  );
  if ('carrier' in raw && raw.carrier) {
    return raw.carrier;
  }
  return raw as CarrierInfo;
}

export async function trackPackage(
  code: string,
  options?: { refresh?: boolean; signal?: AbortSignal },
): Promise<TrackingResult> {
  const cleanCode = encodeURIComponent(code.trim().toUpperCase());
  const refresh = options?.refresh ? 'true' : 'false';
  return requestJson<TrackingResult>(
    `/api/v1/track/${cleanCode}?refresh=${refresh}`,
    options?.signal,
  );
}

export async function fetchRecentTrackings(
  signal?: AbortSignal,
): Promise<RecentTrackingsResponse> {
  const data = await requestJson<RecentTrackingsResponse>(
    '/api/v1/trackings/recent',
    signal,
  );
  return {
    items: Array.isArray(data.items) ? data.items : [],
  };
}
