import { useEffect, useMemo, useState } from 'react';
import L from 'leaflet';
import {
  MapContainer,
  Marker,
  Polyline,
  Popup,
  TileLayer,
  useMap,
} from 'react-leaflet';
import {
  Building2,
  Car,
  Clock,
  Compass,
  ExternalLink,
  Footprints,
  LocateFixed,
  MapPin,
  Navigation,
  Phone,
  RotateCcw,
  Sparkles,
  Trash2,
  AlertCircle,
  CheckCircle2,
} from 'lucide-react';
import type {
  CDDAllocation,
  FacilityType,
  GeoPoint,
  TrackingResult,
  UserCoordinates,
} from '../types/tracking';
import {
  buildExternalMapLinks,
  estimateTravelTimes,
  generateSimulatedNearbyPoint,
} from '../utils/geo';

interface TrackingMapProps {
  result: TrackingResult;
}

const BRAZIL_DEFAULT_CENTER: [number, number] = [-15.793889, -47.882778];

interface OsrmRouteResponse {
  code?: string;
  routes?: Array<{
    geometry?: {
      coordinates?: Array<[number, number]>;
    };
  }>;
}

async function fetchOsrmRoadGeometry(
  points: [number, number][],
  signal: AbortSignal,
): Promise<[number, number][] | null> {
  if (points.length < 2) return null;
  const coordsParam = points
    .map(([lat, lng]) => `${lng.toFixed(5)},${lat.toFixed(5)}`)
    .join(';');
  const url = `https://router.project-osrm.org/route/v1/driving/${coordsParam}?overview=full&geometries=geojson`;
  const resp = await fetch(url, { signal });
  if (!resp.ok) return null;
  const data = (await resp.json()) as OsrmRouteResponse;
  const rawCoords = data.routes?.[0]?.geometry?.coordinates;
  if (!Array.isArray(rawCoords) || rawCoords.length < 2) return null;
  return rawCoords.map(([lng, lat]) => [lat, lng]);
}

function createPinIcon(
  fill: string,
  ring: string,
  glyphSvg: string,
  size = 36,
  badgePulse = false,
): L.DivIcon {
  const pulseRing = badgePulse
    ? `<span style="position:absolute;inset:-6px;border-radius:9999px;border:2px solid ${fill};opacity:0.4;"></span>`
    : '';

  return L.divIcon({
    className: 'rs-leaflet-div-icon',
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
    popupAnchor: [0, -(size / 2 + 2)],
    html: `
      <div style="position:relative;width:${size}px;height:${size}px;display:flex;align-items:center;justify-content:center;">
        ${pulseRing}
        <div style="width:${size}px;height:${size}px;border-radius:9999px;background:${fill};border:3px solid ${ring};box-shadow:0 4px 12px rgba(35,34,31,0.28);display:flex;align-items:center;justify-content:center;color:#FCFBF8;">
          ${glyphSvg}
        </div>
      </div>
    `,
  });
}

function getMarkerIcon(type: FacilityType, isActiveCdd = false): L.DivIcon {
  switch (type) {
    case 'ORIGIN':
      return createPinIcon(
        '#65625A',
        '#FCFBF8',
        '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="M12 22V12"/></svg>',
        32,
      );
    case 'HUB':
      return createPinIcon(
        '#C47D2B',
        '#FCFBF8',
        '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><rect x="1" y="3" width="15" height="13" rx="2"/><path d="M16 8h4l3 3v5h-7V8Z"/><circle cx="5.5" cy="18.5" r="2.5"/><circle cx="18.5" cy="18.5" r="2.5"/></svg>',
        30,
      );
    case 'CDD':
      return createPinIcon(
        '#B85D3B',
        '#FCFBF8',
        '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 22V4a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v18Z"/><path d="M6 12H4a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h2"/><path d="M18 9h2a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-2"/><path d="M10 6h4"/><path d="M10 10h4"/><path d="M10 14h4"/><path d="M10 18h4"/></svg>',
        isActiveCdd ? 40 : 34,
        isActiveCdd,
      );
    case 'DESTINATION':
      return createPinIcon(
        '#2E5A44',
        '#FCFBF8',
        '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 10c0 6-8 12-8 12s-8-6-8-12a8 8 0 0 1 16 0Z"/><circle cx="12" cy="10" r="3"/></svg>',
        34,
      );
  }
}

const USER_GPS_ICON = createPinIcon(
  '#2E5A44',
  '#F6F4EE',
  '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v3"/><path d="M12 19v3"/><path d="M2 12h3"/><path d="M19 12h3"/></svg>',
  36,
  true,
);

function FitBoundsController({
  points,
  focusKey,
}: {
  points: [number, number][];
  focusKey: string;
}) {
  const map = useMap();

  useEffect(() => {
    if (points.length === 0) return;

    if (points.length === 1) {
      map.setView(points[0], 13, { animate: true });
      return;
    }

    const bounds = L.latLngBounds(points);
    if (bounds.isValid()) {
      map.fitBounds(bounds, {
        padding: [44, 44],
        maxZoom: 15,
        animate: true,
      });
    }
  }, [map, points, focusKey]);

  return null;
}

function facilityLabel(type: FacilityType): string {
  switch (type) {
    case 'ORIGIN':
      return 'Origem';
    case 'HUB':
      return 'Hub de Triagem';
    case 'CDD':
      return 'CDD Alocado';
    case 'DESTINATION':
      return 'Destino Final';
  }
}

function CddPopupContent({ cdd }: { cdd: CDDAllocation }) {
  return (
    <div className="min-w-[210px] max-w-[260px] space-y-1.5 text-xs text-[#23221F]">
      <div className="flex items-center justify-between gap-2">
        <span className="rounded bg-[#F7EAE4] px-2 py-0.5 text-[11px] font-bold text-[#8F4124]">
          CDD ALOCADO
        </span>
        <span className="font-semibold text-[#65625A]">{cdd.carrierName}</span>
      </div>
      <p className="text-sm font-bold text-[#23221F]">{cdd.name}</p>
      <p className="text-[#65625A]">
        {cdd.address} — {cdd.city}/{cdd.state}
      </p>
      {cdd.zipCode && (
        <p className="font-mono text-[11px] text-[#23221F]">CEP: {cdd.zipCode}</p>
      )}
      {cdd.openingHours && (
        <p className="text-[#23221F]">
          <strong>Horário:</strong> {cdd.openingHours}
        </p>
      )}
      {cdd.phone && (
        <p className="text-[#23221F]">
          <strong>Telefone:</strong> {cdd.phone}
        </p>
      )}
      {cdd.instructions && (
        <p className="rounded-md bg-[#F6F4EE] p-2 text-[11px] text-[#23221F]">
          {cdd.instructions}
        </p>
      )}
    </div>
  );
}

export function TrackingMap({ result }: TrackingMapProps) {
  const [userLocation, setUserLocation] = useState<UserCoordinates | null>(null);
  const [gpsLoading, setGpsLoading] = useState(false);
  const [gpsError, setGpsError] = useState<string | null>(null);
  const [recenterCounter, setRecenterCounter] = useState(0);

  // Reset GPS error/route cleanly when switching to a different tracking code
  useEffect(() => {
    setGpsError(null);
    setRecenterCounter((c) => c + 1);
  }, [result.trackingCode]);

  const validRoutePoints = useMemo<GeoPoint[]>(() => {
    let raw: GeoPoint[] = [];
    if (Array.isArray(result.routePoints) && result.routePoints.length > 0) {
      raw = result.routePoints.filter(
        (p) =>
          typeof p.lat === 'number' &&
          typeof p.lng === 'number' &&
          (p.lat !== 0 || p.lng !== 0),
      );
    } else {
      if (result.origin) raw.push(result.origin);
      if (result.currentLocation) raw.push(result.currentLocation);
      if (result.destination) raw.push(result.destination);
    }

    // Deduplica pontos na mesma cidade/coordenada para evitar zigue-zague ou pinos sobrepostos
    const deduped: GeoPoint[] = [];
    for (const pt of raw) {
      const prev = deduped[deduped.length - 1];
      if (
        prev &&
        Math.abs(prev.lat - pt.lat) < 0.002 &&
        Math.abs(prev.lng - pt.lng) < 0.002
      ) {
        if (pt.facilityType === 'CDD' || pt.facilityType === 'DESTINATION') {
          deduped[deduped.length - 1] = pt;
        }
        continue;
      }
      deduped.push(pt);
    }
    return deduped;
  }, [result]);

  const routePositions = useMemo<[number, number][]>(
    () => validRoutePoints.map((p) => [p.lat, p.lng]),
    [validRoutePoints],
  );

  const [roadRoutePositions, setRoadRoutePositions] = useState<[number, number][] | null>(null);
  const [gpsRoadPositions, setGpsRoadPositions] = useState<[number, number][] | null>(null);

  const cdd = result.cdd;
  const hasCddCoords =
    Boolean(cdd) &&
    typeof cdd?.lat === 'number' &&
    typeof cdd?.lng === 'number' &&
    (cdd.lat !== 0 || cdd.lng !== 0);

  // Busca o traçado rodoviário real (BRs/estradas via OSRM) entre os pontos da encomenda
  useEffect(() => {
    setRoadRoutePositions(null);
    if (routePositions.length < 2) return;
    const controller = new AbortController();
    fetchOsrmRoadGeometry(routePositions, controller.signal)
      .then((geom) => {
        if (!controller.signal.aborted && geom && geom.length >= 2) {
          setRoadRoutePositions(geom);
        }
      })
      .catch(() => {
        // Fallback automático para polilinha direta se OSRM estiver indisponível
      });
    return () => controller.abort();
  }, [routePositions]);

  // Busca o traçado viário real de ruas entre o GPS do usuário e o CDD
  useEffect(() => {
    setGpsRoadPositions(null);
    if (!userLocation || !cdd || !hasCddCoords) return;
    const controller = new AbortController();
    fetchOsrmRoadGeometry(
      [
        [userLocation.lat, userLocation.lng],
        [cdd.lat, cdd.lng],
      ],
      controller.signal,
    )
      .then((geom) => {
        if (!controller.signal.aborted && geom && geom.length >= 2) {
          setGpsRoadPositions(geom);
        }
      })
      .catch(() => {
        // Fallback automático para linha tracejada direta
      });
    return () => controller.abort();
  }, [userLocation, cdd, hasCddCoords]);

  // When GPS route is active, focus bounds specifically between User and CDD (plus nearby context)
  const fitBoundsPoints = useMemo<[number, number][]>(() => {
    if (userLocation && cdd && hasCddCoords) {
      return [
        [userLocation.lat, userLocation.lng],
        [cdd.lat, cdd.lng],
      ];
    }
    const pts = [...routePositions];
    if (cdd && hasCddCoords) {
      const exists = pts.some(
        ([lat, lng]) =>
          Math.abs(lat - cdd.lat) < 0.0005 && Math.abs(lng - cdd.lng) < 0.0005,
      );
      if (!exists) {
        pts.push([cdd.lat, cdd.lng]);
      }
    }
    return pts;
  }, [userLocation, cdd, hasCddCoords, routePositions]);

  const focusKey = useMemo(
    () =>
      `${result.trackingCode}:${userLocation ? `${userLocation.lat},${userLocation.lng}` : 'no-gps'}:${recenterCounter}`,
    [result.trackingCode, userLocation, recenterCounter],
  );

  const travelEstimate = useMemo(() => {
    if (!userLocation || !cdd || !hasCddCoords) return null;
    return estimateTravelTimes(userLocation.lat, userLocation.lng, cdd.lat, cdd.lng);
  }, [userLocation, cdd, hasCddCoords]);

  const externalLinks = useMemo(() => {
    if (cdd && hasCddCoords) {
      return buildExternalMapLinks(cdd.lat, cdd.lng);
    }
    if (result.currentLocation) {
      return buildExternalMapLinks(
        result.currentLocation.lat,
        result.currentLocation.lng,
      );
    }
    return null;
  }, [cdd, hasCddCoords, result.currentLocation]);

  const handleActivateGps = () => {
    setGpsError(null);
    if (typeof navigator === 'undefined' || !('geolocation' in navigator)) {
      setGpsError(
        'Seu navegador não oferece suporte à API de Geolocalização. Use o botão de simulação abaixo para testar a rota até o CDD.',
      );
      return;
    }

    setGpsLoading(true);
    navigator.geolocation.getCurrentPosition(
      (position) => {
        setGpsLoading(false);
        setUserLocation({
          lat: position.coords.latitude,
          lng: position.coords.longitude,
          accuracyMeters: Math.round(position.coords.accuracy),
          simulated: false,
          label: 'Sua localização via GPS',
        });
      },
      (err) => {
        setGpsLoading(false);
        if (err.code === err.PERMISSION_DENIED) {
          setGpsError(
            'Permissão de GPS recusada no navegador. Você pode liberar nas configurações ou simular um ponto de partida próximo para testar a rota.',
          );
        } else if (err.code === err.POSITION_UNAVAILABLE) {
          setGpsError(
            'Sinal de GPS indisponível neste dispositivo/ambiente. Experimente simular um ponto de partida próximo.',
          );
        } else {
          setGpsError(
            'Tempo esgotado ao obter sua localização GPS. Você pode tentar novamente ou usar a simulação próxima.',
          );
        }
      },
      {
        enableHighAccuracy: true,
        timeout: 9000,
        maximumAge: 60_000,
      },
    );
  };

  const handleSimulateNearby = () => {
    if (!cdd || !hasCddCoords) return;
    setGpsError(null);
    setUserLocation(generateSimulatedNearbyPoint(cdd.lat, cdd.lng));
  };

  const handleClearGps = () => {
    setUserLocation(null);
    setGpsError(null);
    setRecenterCounter((c) => c + 1);
  };

  const initialCenter: [number, number] =
    routePositions[0] ||
    (cdd && hasCddCoords ? [cdd.lat, cdd.lng] : BRAZIL_DEFAULT_CENTER);

  return (
    <section
      aria-labelledby="map-section-heading"
      className="rs-card rounded-2xl border border-[#DCD5C9] bg-[#FCFBF8] p-5 shadow-sm sm:p-6"
    >
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-[#EAE5DC] pb-4">
        <div>
          <h2 id="map-section-heading" className="text-lg font-bold text-[#23221F]">
            Mapa da Rota, CDD &amp; Navegação GPS
          </h2>
          <p className="text-sm text-[#65625A]">
            Visualização geográfica da origem ao destino com destaque para a unidade CDD alocada.
          </p>
        </div>

        <button
          type="button"
          onClick={() => setRecenterCounter((c) => c + 1)}
          className="inline-flex min-h-[42px] items-center gap-1.5 rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] px-3.5 py-2 text-xs font-semibold text-[#23221F] transition-colors hover:bg-[#EAE5DC]"
        >
          <RotateCcw className="h-3.5 w-3.5 text-[#2E5A44]" aria-hidden="true" />
          <span>Ajustar enquadramento</span>
        </button>
      </div>

      <div className="grid grid-cols-1 gap-5 xl:grid-cols-12">
        {/* Interactive Leaflet Map */}
        <div className="xl:col-span-7 flex flex-col gap-3">
          <div className="rs-map-wrapper relative overflow-hidden rounded-2xl border border-[#DCD5C9] bg-[#EAE5DC]">
            <MapContainer
              center={initialCenter}
              zoom={5}
              scrollWheelZoom
              className="h-[360px] w-full sm:h-[430px]"
            >
              <TileLayer
                attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
                url="https://tile.openstreetmap.org/{z}/{x}/{y}.png"
              />

              <FitBoundsController points={fitBoundsPoints} focusKey={focusKey} />

              {/* Solid road route line: Origin -> Hubs -> CDD -> Destination */}
              {routePositions.length >= 2 && (
                <Polyline
                  positions={roadRoutePositions ?? routePositions}
                  pathOptions={{
                    color: '#2E5A44',
                    weight: 4,
                    opacity: 0.88,
                  }}
                />
              )}

              {/* Dashed terracotta GPS street route from user to CDD */}
              {userLocation && cdd && hasCddCoords && (
                <Polyline
                  positions={
                    gpsRoadPositions ?? [
                      [userLocation.lat, userLocation.lng],
                      [cdd.lat, cdd.lng],
                    ]
                  }
                  pathOptions={{
                    color: '#B85D3B',
                    weight: 4,
                    opacity: 0.92,
                    dashArray: '8, 8',
                  }}
                />
              )}

              {/* Route markers */}
              {validRoutePoints.map((point, index) => {
                const isCddMarker = point.facilityType === 'CDD';
                return (
                  <Marker
                    key={`${point.facilityType}-${point.lat}-${point.lng}-${index}`}
                    position={[point.lat, point.lng]}
                    icon={getMarkerIcon(
                      point.facilityType,
                      isCddMarker && Boolean(cdd?.availableForPickup),
                    )}
                  >
                    <Popup>
                      {isCddMarker && cdd ? (
                        <CddPopupContent cdd={cdd} />
                      ) : (
                        <div className="min-w-[180px] space-y-1 text-xs text-[#23221F]">
                          <span className="inline-block rounded bg-[#EAE5DC] px-2 py-0.5 text-[10px] font-bold uppercase text-[#23221F]">
                            {facilityLabel(point.facilityType)}
                          </span>
                          <p className="text-sm font-bold text-[#23221F]">
                            {point.facilityName || `${point.city}/${point.state}`}
                          </p>
                          {point.address && (
                            <p className="text-[#65625A]">{point.address}</p>
                          )}
                          <p className="font-medium text-[#23221F]">
                            {point.city}
                            {point.state ? ` — ${point.state}` : ''}
                          </p>
                          {point.zipCode && (
                            <p className="font-mono text-[11px] text-[#65625A]">
                              CEP: {point.zipCode}
                            </p>
                          )}
                        </div>
                      )}
                    </Popup>
                  </Marker>
                );
              })}

              {/* Dedicated CDD Marker if not already present in routePoints */}
              {cdd &&
                hasCddCoords &&
                !validRoutePoints.some(
                  (p) =>
                    p.facilityType === 'CDD' &&
                    Math.abs(p.lat - cdd.lat) < 0.0005 &&
                    Math.abs(p.lng - cdd.lng) < 0.0005,
                ) && (
                  <Marker
                    position={[cdd.lat, cdd.lng]}
                    icon={getMarkerIcon('CDD', cdd.availableForPickup)}
                  >
                    <Popup>
                      <CddPopupContent cdd={cdd} />
                    </Popup>
                  </Marker>
                )}

              {/* User GPS position marker */}
              {userLocation && (
                <Marker
                  position={[userLocation.lat, userLocation.lng]}
                  icon={USER_GPS_ICON}
                >
                  <Popup>
                    <div className="space-y-1 text-xs text-[#23221F]">
                      <span className="inline-block rounded bg-[#E6EFEA] px-2 py-0.5 text-[10px] font-bold text-[#234735]">
                        VOCÊ ESTÁ AQUI
                      </span>
                      <p className="font-semibold">{userLocation.label}</p>
                      <p className="font-mono text-[11px] text-[#65625A]">
                        {userLocation.lat.toFixed(5)}, {userLocation.lng.toFixed(5)}
                      </p>
                      {travelEstimate && (
                        <p className="font-medium text-[#8F4124]">
                          Distância até o CDD: {travelEstimate.distanceLabel}
                        </p>
                      )}
                    </div>
                  </Popup>
                </Marker>
              )}
            </MapContainer>
          </div>

          {/* Non-color-only map legend */}
          <ul
            aria-label="Legenda do mapa"
            className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-[#EAE5DC] bg-[#F6F4EE] px-3.5 py-2.5 text-xs text-[#65625A]"
          >
            <li className="inline-flex items-center gap-1.5">
              <span className="h-3 w-3 rounded-full border-2 border-[#FCFBF8] bg-[#65625A] shadow-xs" aria-hidden="true" />
              <span>Origem</span>
            </li>
            <li className="inline-flex items-center gap-1.5">
              <span className="h-3 w-3 rounded-full border-2 border-[#FCFBF8] bg-[#C47D2B] shadow-xs" aria-hidden="true" />
              <span>Hub de Triagem</span>
            </li>
            <li className="inline-flex items-center gap-1.5 font-semibold text-[#8F4124]">
              <span className="h-3.5 w-3.5 rounded-full border-2 border-[#FCFBF8] bg-[#B85D3B] shadow-xs" aria-hidden="true" />
              <span>CDD Alocado</span>
            </li>
            <li className="inline-flex items-center gap-1.5">
              <span className="h-3 w-3 rounded-full border-2 border-[#FCFBF8] bg-[#2E5A44] shadow-xs" aria-hidden="true" />
              <span>Destino / Sua Posição GPS</span>
            </li>
            <li className="inline-flex items-center gap-1.5">
              <span className="inline-block w-5 border-t-2 border-dashed border-[#B85D3B]" aria-hidden="true" />
              <span>Rota GPS ao CDD</span>
            </li>
          </ul>
        </div>

        {/* Side CDD Pickup & GPS Navigation Panel */}
        <div className="xl:col-span-5 flex flex-col justify-between gap-4">
          {cdd && hasCddCoords ? (
            <div className="flex h-full flex-col justify-between gap-4 rounded-2xl border border-[#E3C1B2] bg-[#F6F4EE] p-4 sm:p-5">
              <div className="space-y-3.5">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div className="inline-flex items-center gap-2 rounded-full border border-[#E3C1B2] bg-[#F7EAE4] px-3 py-1 text-xs font-bold text-[#8F4124]">
                    <Building2 className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                    <span>Unidade CDD Identificada</span>
                  </div>

                  {cdd.availableForPickup ? (
                    <span className="inline-flex items-center gap-1 rounded-full border border-[#BBD4C6] bg-[#E6EFEA] px-2.5 py-1 text-xs font-semibold text-[#234735]">
                      <CheckCircle2 className="h-3.5 w-3.5" aria-hidden="true" />
                      Pronto p/ retirada
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 rounded-full border border-[#E4CFA8] bg-[#F9F0E1] px-2.5 py-1 text-xs font-semibold text-[#835014]">
                      Em triagem / rota local
                    </span>
                  )}
                </div>

                <div>
                  <h3 className="text-base font-bold text-[#23221F] sm:text-lg">
                    {cdd.name}
                  </h3>
                  <p className="text-xs font-semibold text-[#65625A]">
                    Operado por {cdd.carrierName}
                  </p>
                </div>

                <dl className="space-y-2 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] p-3.5 text-xs text-[#23221F]">
                  <div className="flex items-start gap-2">
                    <MapPin className="mt-0.5 h-4 w-4 shrink-0 text-[#B85D3B]" aria-hidden="true" />
                    <div>
                      <dt className="sr-only">Endereço</dt>
                      <dd className="font-medium">
                        {cdd.address}
                        <br />
                        {cdd.city}/{cdd.state}
                        {cdd.zipCode ? ` · CEP ${cdd.zipCode}` : ''}
                      </dd>
                    </div>
                  </div>

                  {cdd.openingHours && (
                    <div className="flex items-start gap-2">
                      <Clock className="mt-0.5 h-4 w-4 shrink-0 text-[#2E5A44]" aria-hidden="true" />
                      <div>
                        <dt className="sr-only">Horário de funcionamento</dt>
                        <dd>{cdd.openingHours}</dd>
                      </div>
                    </div>
                  )}

                  {cdd.phone && (
                    <div className="flex items-center gap-2">
                      <Phone className="h-4 w-4 shrink-0 text-[#2E5A44]" aria-hidden="true" />
                      <div>
                        <dt className="sr-only">Telefone</dt>
                        <dd className="font-mono">{cdd.phone}</dd>
                      </div>
                    </div>
                  )}
                </dl>

                {cdd.instructions && (
                  <p className="rounded-xl border border-[#DCD5C9] bg-[#EAE5DC]/60 px-3.5 py-2.5 text-xs leading-relaxed text-[#23221F]">
                    <strong className="font-semibold text-[#8F4124]">
                      Orientações para retirada:{' '}
                    </strong>
                    {cdd.instructions}
                  </p>
                )}

                {/* GPS Controls & Distance Telemetry */}
                <div className="space-y-2.5 border-t border-[#DCD5C9] pt-3">
                  <p className="text-xs font-bold tracking-wide text-[#23221F] uppercase">
                    Buscar no local com GPS
                  </p>

                  <div className="flex flex-col gap-2">
                    <button
                      type="button"
                      onClick={handleActivateGps}
                      disabled={gpsLoading}
                      className="inline-flex min-h-[48px] w-full items-center justify-center gap-2 rounded-xl bg-[#2E5A44] px-4 py-2.5 text-sm font-semibold text-[#FCFBF8] shadow-xs transition-colors hover:bg-[#234735] disabled:opacity-70"
                    >
                      <LocateFixed
                        className={`h-4 w-4 shrink-0 ${gpsLoading ? 'animate-pulse' : ''}`}
                        aria-hidden="true"
                      />
                      <span>
                        {gpsLoading
                          ? 'Obtendo posição GPS...'
                          : 'Ativar Meu GPS e Traçar Rota até o CDD'}
                      </span>
                    </button>

                    <div className="flex flex-wrap gap-2">
                      <button
                        type="button"
                        onClick={handleSimulateNearby}
                        className="inline-flex min-h-[42px] flex-1 items-center justify-center gap-1.5 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] px-3 py-2 text-xs font-medium text-[#23221F] transition-colors hover:bg-[#EAE5DC]"
                      >
                        <Sparkles className="h-3.5 w-3.5 shrink-0 text-[#C47D2B]" aria-hidden="true" />
                        <span>Simular ponto de partida próximo (para teste)</span>
                      </button>

                      {userLocation && (
                        <button
                          type="button"
                          onClick={handleClearGps}
                          aria-label="Limpar localização GPS ativa"
                          className="inline-flex min-h-[42px] items-center justify-center gap-1 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] px-3 py-2 text-xs font-medium text-[#65625A] transition-colors hover:bg-[#F7EAE4] hover:text-[#8F4124]"
                        >
                          <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                          <span>Limpar</span>
                        </button>
                      )}
                    </div>
                  </div>

                  {gpsError && (
                    <div
                      role="alert"
                      className="flex items-start gap-2 rounded-xl border border-[#E3C1B2] bg-[#F7EAE4] p-3 text-xs text-[#8F4124]"
                    >
                      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                      <span>{gpsError}</span>
                    </div>
                  )}

                  {userLocation && travelEstimate && (
                    <div
                      aria-live="polite"
                      className="rounded-xl border border-[#BBD4C6] bg-[#E6EFEA] p-3.5 text-xs text-[#234735]"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-bold">
                          {userLocation.simulated
                            ? 'Rota simulada até o CDD'
                            : 'Rota GPS ativa até o CDD'}
                        </span>
                        {userLocation.accuracyMeters !== undefined && (
                          <span className="font-mono text-[11px] opacity-80">
                            ±{userLocation.accuracyMeters} m
                          </span>
                        )}
                      </div>

                      <div className="mt-2.5 grid grid-cols-3 gap-2">
                        <div className="rounded-lg bg-[#FCFBF8] p-2 text-center">
                          <span className="block text-[10px] font-semibold text-[#65625A] uppercase">
                            Distância
                          </span>
                          <strong className="mt-0.5 block font-mono text-sm text-[#23221F]">
                            {travelEstimate.distanceLabel}
                          </strong>
                        </div>
                        <div className="rounded-lg bg-[#FCFBF8] p-2 text-center">
                          <span className="inline-flex items-center justify-center gap-1 text-[10px] font-semibold text-[#65625A] uppercase">
                            <Car className="h-3 w-3" aria-hidden="true" />
                            Carro
                          </span>
                          <strong className="mt-0.5 block font-mono text-sm text-[#23221F]">
                            ~{travelEstimate.carLabel}
                          </strong>
                        </div>
                        <div className="rounded-lg bg-[#FCFBF8] p-2 text-center">
                          <span className="inline-flex items-center justify-center gap-1 text-[10px] font-semibold text-[#65625A] uppercase">
                            <Footprints className="h-3 w-3" aria-hidden="true" />
                            A pé
                          </span>
                          <strong className="mt-0.5 block font-mono text-sm text-[#23221F]">
                            ~{travelEstimate.walkLabel}
                          </strong>
                        </div>
                      </div>
                    </div>
                  )}
                </div>
              </div>

              {/* External Map Navigation Apps */}
              {externalLinks && (
                <div className="border-t border-[#DCD5C9] pt-3">
                  <p className="mb-2 text-[11px] font-semibold tracking-wide text-[#65625A] uppercase">
                    Abrir rota externa ({cdd.lat.toFixed(4)}, {cdd.lng.toFixed(4)})
                  </p>
                  <div className="grid grid-cols-1 gap-2 sm:grid-cols-3 xl:grid-cols-1 2xl:grid-cols-3">
                    <a
                      href={externalLinks.googleMapsUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex min-h-[42px] items-center justify-center gap-1.5 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] px-3 py-2 text-xs font-semibold text-[#23221F] transition-colors hover:border-[#2E5A44] hover:bg-[#E6EFEA]"
                    >
                      <Navigation className="h-3.5 w-3.5 text-[#2E5A44]" aria-hidden="true" />
                      <span>Google Maps</span>
                      <ExternalLink className="h-3 w-3 text-[#65625A]" aria-hidden="true" />
                    </a>
                    <a
                      href={externalLinks.wazeUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex min-h-[42px] items-center justify-center gap-1.5 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] px-3 py-2 text-xs font-semibold text-[#23221F] transition-colors hover:border-[#B85D3B] hover:bg-[#F7EAE4]"
                    >
                      <Compass className="h-3.5 w-3.5 text-[#B85D3B]" aria-hidden="true" />
                      <span>Waze</span>
                      <ExternalLink className="h-3 w-3 text-[#65625A]" aria-hidden="true" />
                    </a>
                    <a
                      href={externalLinks.openStreetMapUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex min-h-[42px] items-center justify-center gap-1.5 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] px-3 py-2 text-xs font-semibold text-[#23221F] transition-colors hover:border-[#2E5A44] hover:bg-[#EAE5DC]"
                    >
                      <MapPin className="h-3.5 w-3.5 text-[#65625A]" aria-hidden="true" />
                      <span>OpenStreetMap</span>
                      <ExternalLink className="h-3 w-3 text-[#65625A]" aria-hidden="true" />
                    </a>
                  </div>
                </div>
              )}
            </div>
          ) : (
            <div className="flex h-full flex-col justify-between gap-4 rounded-2xl border border-[#DCD5C9] bg-[#F6F4EE] p-5">
              <div className="space-y-3">
                <div className="inline-flex items-center gap-2 rounded-full border border-[#DCD5C9] bg-[#EAE5DC] px-3 py-1 text-xs font-semibold text-[#23221F]">
                  <Building2 className="h-3.5 w-3.5 text-[#65625A]" aria-hidden="true" />
                  <span>Alocação de CDD</span>
                </div>

                <h3 className="text-base font-bold text-[#23221F]">
                  {result.currentStatus === 'DELIVERED'
                    ? 'Objeto já entregue no destino final'
                    : 'Pacote ainda em deslocamento entre unidades'}
                </h3>
                <p className="text-sm leading-relaxed text-[#65625A]">
                  {result.currentStatus === 'DELIVERED'
                    ? 'A entrega foi concluída com sucesso. Você pode conferir todo o trajeto percorrido no mapa ao lado.'
                    : 'Assim que a encomenda chegar ao Centro de Distribuição Domiciliária (CDD) local, exibiremos aqui o endereço completo, horário de funcionamento e rota GPS para retirada presencial.'}
                </p>

                {(result.currentLocation || result.events?.[0]?.location) && (
                  <div className="rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] p-3.5 text-xs">
                    <span className="block font-semibold text-[#65625A] uppercase">
                      Última unidade registrada
                    </span>
                    <p className="mt-1 text-sm font-bold text-[#23221F]">
                      {result.currentLocation?.facilityName ||
                        result.currentLocation?.city ||
                        result.events?.[0]?.location}
                    </p>
                    {result.currentLocation?.address && (
                      <p className="mt-0.5 text-[#65625A]">{result.currentLocation.address}</p>
                    )}
                    {result.currentLocation?.city && (
                      <p className="mt-0.5 text-[#65625A]">
                        {result.currentLocation.city}
                        {result.currentLocation.state
                          ? `/${result.currentLocation.state}`
                          : ''}
                        {result.currentLocation.zipCode
                          ? ` · CEP ${result.currentLocation.zipCode}`
                          : ''}
                      </p>
                    )}
                  </div>
                )}
                {result.destination && (result.destination.city || result.destination.facilityName) && (
                  <div className="rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] p-3.5 text-xs">
                    <span className="block font-semibold text-[#2E5A44] uppercase">
                      Unidade / Local de Destino
                    </span>
                    <p className="mt-1 text-sm font-bold text-[#23221F]">
                      {result.destination.facilityName || result.destination.city}
                    </p>
                    {result.destination.city && (
                      <p className="mt-0.5 text-[#65625A]">
                        {result.destination.city}
                        {result.destination.state ? `/${result.destination.state}` : ''}
                      </p>
                    )}
                  </div>
                )}
              </div>

              {externalLinks && (
                <div className="border-t border-[#DCD5C9] pt-3">
                  <p className="mb-2 text-[11px] font-semibold tracking-wide text-[#65625A] uppercase">
                    Ver localização atual no mapa externo
                  </p>
                  <div className="flex flex-wrap gap-2">
                    <a
                      href={externalLinks.googleMapsUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex min-h-[40px] items-center gap-1.5 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] px-3 py-2 text-xs font-semibold text-[#23221F] hover:bg-[#EAE5DC]"
                    >
                      <span>Google Maps</span>
                      <ExternalLink className="h-3 w-3" aria-hidden="true" />
                    </a>
                    <a
                      href={externalLinks.openStreetMapUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex min-h-[40px] items-center gap-1.5 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] px-3 py-2 text-xs font-semibold text-[#23221F] hover:bg-[#EAE5DC]"
                    >
                      <span>OpenStreetMap</span>
                      <ExternalLink className="h-3 w-3" aria-hidden="true" />
                    </a>
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
