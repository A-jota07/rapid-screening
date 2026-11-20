import { useCallback, useEffect, useRef, useState } from 'react';
import {
  AlertTriangle,
  Compass,
  PackageSearch,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react';
import {
  ApiError,
  fetchCarriers,
  fetchHealth,
  trackPackage,
} from './api/client';
import { ProviderStatusPanel } from './components/ProviderStatusPanel';
import { RecentSearches } from './components/RecentSearches';
import { SearchBar } from './components/SearchBar';
import { StatusStepper } from './components/StatusStepper';
import { Timeline } from './components/Timeline';
import { TrackingMap } from './components/TrackingMap';
import type {
  CarrierInfo,
  HealthResponse,
  RecentTrackingSummary,
  TrackingResult,
} from './types/tracking';
import { sanitizeTrackingInput } from './utils/carrierDetect';

const LOCAL_RECENT_STORAGE_KEY = 'rapid_screening_recent_v1';

function readLocalRecent(): RecentTrackingSummary[] {
  try {
    const raw = window.localStorage.getItem(LOCAL_RECENT_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function saveLocalRecent(item: RecentTrackingSummary): RecentTrackingSummary[] {
  const existing = readLocalRecent().filter((x) => x.trackingCode !== item.trackingCode);
  const next = [item, ...existing].slice(0, 10);
  try {
    window.localStorage.setItem(LOCAL_RECENT_STORAGE_KEY, JSON.stringify(next));
  } catch {
    // Ignore storage quota errors
  }
  return next;
}

export function App() {
  const [queryCode, setQueryCode] = useState<string>(() => {
    const params = new URLSearchParams(window.location.search);
    const initial = params.get('code');
    return initial ? sanitizeTrackingInput(initial) : '';
  });

  const [result, setResult] = useState<TrackingResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [trackError, setTrackError] = useState<string | null>(null);

  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [healthError, setHealthError] = useState<string | null>(null);
  const [carriers, setCarriers] = useState<CarrierInfo[]>([]);
  const [recentItems, setRecentItems] = useState<RecentTrackingSummary[]>(() => readLocalRecent());

  const [cooldownRemaining, setCooldownRemaining] = useState(0);
  const [cooldownMessage, setCooldownMessage] = useState<string | null>(null);

  const activeRequestRef = useRef<AbortController | null>(null);

  const loadHealthAndRecent = useCallback(async () => {
    try {
      const healthData = await fetchHealth();
      setHealth(healthData);
      setHealthError(null);
    } catch (err) {
      const msg =
        err instanceof Error
          ? err.message
          : 'Não foi possível conectar ao servidor backend.';
      setHealthError(msg);
    }
  }, []);

  // Load initial health and carriers
  useEffect(() => {
    const controller = new AbortController();

    async function bootstrap() {
      const [healthRes, carriersRes] = await Promise.allSettled([
        fetchHealth(controller.signal),
        fetchCarriers(controller.signal),
      ]);

      if (controller.signal.aborted) return;

      if (healthRes.status === 'fulfilled') {
        setHealth(healthRes.value);
        setHealthError(null);
      } else {
        setHealthError(
          healthRes.reason instanceof Error
            ? healthRes.reason.message
            : 'Backend temporariamente indisponível.',
        );
      }

      if (carriersRes.status === 'fulfilled') {
        setCarriers(carriersRes.value);
      }
    }

    void bootstrap();
    return () => controller.abort();
  }, []);

  // Cooldown countdown tick
  useEffect(() => {
    if (cooldownRemaining <= 0) {
      setCooldownMessage(null);
      return;
    }

    const timer = window.setInterval(() => {
      setCooldownRemaining((prev) => {
        if (prev <= 1) {
          setCooldownMessage(null);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);

    return () => window.clearInterval(timer);
  }, [cooldownRemaining]);

  const executeTrack = useCallback(
    async (rawCode: string, refresh = false) => {
      const code = sanitizeTrackingInput(rawCode);
      if (code.length < 4) return;

      activeRequestRef.current?.abort();
      const controller = new AbortController();
      activeRequestRef.current = controller;

      setQueryCode(code);
      setTrackError(null);
      setCooldownMessage(null);
      if (refresh) {
        setRefreshing(true);
      } else {
        setLoading(true);
      }

      try {
        const data = await trackPackage(code, {
          refresh,
          signal: controller.signal,
        });

        if (controller.signal.aborted) return;

        setResult(data);
        setRecentItems(
          saveLocalRecent({
            trackingCode: data.trackingCode,
            carrierCode: data.carrier.code,
            carrierName: data.carrier.name,
            currentStatus: data.currentStatus,
            currentStatusLabel: data.currentStatusLabel,
            providerUsed: data.providerUsed,
            lastUpdatedAt: data.lastUpdatedAt,
            cddName: data.cdd?.name,
          }),
        );
        document.title = `${data.trackingCode} (${data.currentStatusLabel}) — Rapid Screening`;

        // Sync URL ?code=... cleanly without page reload
        const url = new URL(window.location.href);
        url.searchParams.set('code', data.trackingCode);
        window.history.replaceState({}, '', url.toString());

        if (refresh && health?.cache?.cooldownSeconds) {
          setCooldownRemaining(health.cache.cooldownSeconds);
        }

        // Refresh background telemetry & recent list
        void loadHealthAndRecent();
      } catch (err) {
        if (controller.signal.aborted) return;

        if (err instanceof ApiError && err.status === 429) {
          const waitSeconds =
            err.retryAfterSeconds ?? health?.cache?.cooldownSeconds ?? 30;
          setCooldownRemaining(waitSeconds);
          setCooldownMessage(
            `${err.message} Aguarde ${waitSeconds}s antes de forçar nova consulta externa.`,
          );
        } else {
          const message =
            err instanceof Error
              ? err.message
              : 'Erro inesperado ao consultar o rastreamento.';
          setTrackError(message);
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
          setRefreshing(false);
        }
      }
    },
    [health?.cache?.cooldownSeconds, loadHealthAndRecent],
  );

  // Auto-track if URL arrived with ?code=...
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const initial = params.get('code');
    if (initial && sanitizeTrackingInput(initial).length >= 4) {
      void executeTrack(initial, false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSelectCode = (code: string) => {
    const clean = sanitizeTrackingInput(code);
    setQueryCode(clean);
    void executeTrack(clean, false);
    window.scrollTo({ top: 0, behavior: 'smooth' });
  };

  const handleRefreshNow = () => {
    if (!result || refreshing || cooldownRemaining > 0) return;
    void executeTrack(result.trackingCode, true);
  };

  const backendOnline = Boolean(health && !healthError);

  return (
    <div className="min-h-screen bg-[#F6F4EE] text-[#23221F] flex flex-col">
      <a href="#main-content" className="skip-link visually-hidden">
        Ir para o conteúdo principal
      </a>

      {/* Compact Top Header */}
      <header className="border-b border-[#DCD5C9] bg-[#FCFBF8]/95 backdrop-blur-xs">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-3.5 sm:px-6 lg:px-8">
          <div className="flex items-center gap-3">
            <div
              aria-hidden="true"
              className="flex h-10 w-10 items-center justify-center rounded-xl bg-[#2E5A44] text-[#FCFBF8] shadow-xs"
            >
              <PackageSearch className="h-5 w-5" />
            </div>
            <div>
              <span className="block text-base font-bold tracking-tight text-[#23221F]">
                Rapid Screening
              </span>
              <span className="block text-xs text-[#65625A]">
                Rastreamento Inteligente &amp; Retirada em CDD
              </span>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <span
              className={`inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-semibold ${
                backendOnline
                  ? 'border-[#BBD4C6] bg-[#E6EFEA] text-[#234735]'
                  : 'border-[#E4CFA8] bg-[#F9F0E1] text-[#835014]'
              }`}
            >
              <span
                aria-hidden="true"
                className={`h-2 w-2 rounded-full ${
                  backendOnline ? 'bg-[#2E5A44]' : 'bg-[#C47D2B]'
                }`}
              />
              <span>
                {backendOnline ? 'API Conectada' : 'Verificando API'}
              </span>
            </span>

            <a
              href="https://alexandrejunior.com.br/"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex min-h-[34px] items-center justify-center rounded-full bg-[#2E5A44] px-3.5 py-1.5 text-xs font-semibold text-[#FCFBF8] shadow-xs transition-colors hover:bg-[#234735]"
            >
              Portfólio
            </a>

            <a
              href="https://github.com/A-jota07"
              target="_blank"
              rel="noopener noreferrer"
              aria-label="Perfil no GitHub"
              title="GitHub"
              className="inline-flex h-[34px] w-[34px] items-center justify-center rounded-full border border-[#DCD5C9] bg-[#F6F4EE] text-[#23221F] shadow-xs transition-colors hover:border-[#2E5A44] hover:bg-[#E6EFEA] hover:text-[#234735]"
            >
              <svg
                aria-hidden="true"
                viewBox="0 0 24 24"
                className="h-4 w-4 fill-current"
              >
                <path d="M12 0.5C5.648 0.5 0.5 5.648 0.5 12c0 5.084 3.292 9.395 7.865 10.916.575.106.785-.25.785-.555 0-.274-.01-1-.015-1.962-3.2.695-3.875-1.542-3.875-1.542-.523-1.329-1.277-1.682-1.277-1.682-1.044-.714.079-.7.079-.7 1.154.081 1.762 1.185 1.762 1.185 1.026 1.758 2.692 1.25 3.348.956.104-.743.402-1.25.73-1.538-2.555-.291-5.242-1.277-5.242-5.684 0-1.256.449-2.283 1.185-3.088-.119-.29-.513-1.46.112-3.044 0 0 .966-.309 3.165 1.179A11.02 11.02 0 0 1 12 6.065c.978.004 1.963.132 2.883.388 2.197-1.488 3.162-1.179 3.162-1.179.627 1.584.233 2.754.114 3.044.738.805 1.184 1.832 1.184 3.088 0 4.418-2.691 5.39-5.254 5.675.414.356.782 1.059.782 2.135 0 1.542-.014 2.785-.014 3.164 0 .308.208.667.791.554C20.212 21.391 23.5 17.082 23.5 12 23.5 5.648 18.352.5 12 .5Z" />
              </svg>
            </a>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main
        id="main-content"
        className="mx-auto w-full max-w-7xl flex-1 space-y-6 px-4 py-6 sm:px-6 sm:py-8 lg:px-8"
      >
        {/* Hero Intro */}
        <div className="max-w-3xl space-y-1.5">
          <h1 tabIndex={-1} className="text-2xl font-bold tracking-tight text-[#23221F] sm:text-3xl">
            Acompanhe sua encomenda com calma e precisão
          </h1>
          <p className="text-sm text-[#65625A] sm:text-base">
            Identificação automática de transportadora, histórico traduzido em linguagem clara,
            mapa interativo da rota e guia por GPS até o CDD quando disponível para retirada.
          </p>
        </div>

        {/* Accessible Search Component */}
        <SearchBar
          value={queryCode}
          onChange={setQueryCode}
          onSubmitCode={(code) => void executeTrack(code, false)}
          loading={loading}
          supportedCarriers={carriers}
        />

        {/* Polite announcement region for tracking load completions */}
        <div aria-live="polite" className="visually-hidden">
          {result
            ? `Rastreamento ${result.trackingCode} carregado. Status atual: ${result.currentStatusLabel}.`
            : ''}
        </div>

        {/* Error Banner */}
        {trackError && (
          <div
            role="alert"
            className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-[#E3C1B2] bg-[#F7EAE4] p-4 text-sm text-[#8F4124]"
          >
            <div className="flex items-start gap-2.5">
              <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-[#B85D3B]" aria-hidden="true" />
              <div>
                <p className="font-bold">Não foi possível concluir o rastreamento</p>
                <p className="mt-0.5">{trackError}</p>
              </div>
            </div>
            {queryCode.length >= 4 && (
              <button
                type="button"
                onClick={() => void executeTrack(queryCode, false)}
                className="inline-flex min-h-[42px] items-center gap-1.5 rounded-xl border border-[#B85D3B]/40 bg-[#FCFBF8] px-3.5 py-2 text-xs font-semibold text-[#8F4124] hover:bg-[#EAE5DC]"
              >
                <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
                <span>Tentar novamente</span>
              </button>
            )}
          </div>
        )}

        {/* Loaded Tracking Result Workspace */}
        {result ? (
          <div className="space-y-6">
            <StatusStepper result={result} />

            <TrackingMap result={result} />

            <div className="grid grid-cols-1 gap-6 lg:grid-cols-12 lg:items-start">
              <div className="lg:col-span-7">
                <Timeline
                  events={result.events}
                  carrierName={result.carrier.name}
                />
              </div>

              <aside className="space-y-6 lg:col-span-5">
                <RecentSearches
                  items={recentItems}
                  activeCode={result.trackingCode}
                  onSelectCode={handleSelectCode}
                />
                <ProviderStatusPanel
                  result={result}
                  health={health}
                  healthError={healthError}
                  carriers={carriers}
                  refreshing={refreshing}
                  cooldownRemaining={cooldownRemaining}
                  cooldownMessage={cooldownMessage}
                  onRefreshNow={handleRefreshNow}
                  onSelectSampleCode={handleSelectCode}
                  onReloadHealth={() => void loadHealthAndRecent()}
                />
              </aside>
            </div>
          </div>
        ) : (
          /* Initial Workspace before first search */
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-12 lg:items-start">
            <div className="space-y-6 lg:col-span-7">
              <section
                aria-labelledby="guide-heading"
                className="rs-card rounded-2xl border border-[#DCD5C9] bg-[#FCFBF8] p-6 shadow-sm"
              >
                <h2 id="guide-heading" className="text-lg font-bold text-[#23221F]">
                  Como o Rapid Screening funciona
                </h2>
                <p className="mt-1 text-sm text-[#65625A]">
                  Selecione qualquer um dos códigos de exemplo acima ou digite o seu rastreio para
                  visualizar todos os recursos em ação.
                </p>

                <div className="mt-5 grid gap-4 sm:grid-cols-3">
                  <div className="rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-4">
                    <ShieldCheck className="h-5 w-5 text-[#2E5A44]" aria-hidden="true" />
                    <h3 className="mt-2.5 text-sm font-bold text-[#23221F]">
                      Cache &amp; Provedores
                    </h3>
                    <p className="mt-1 text-xs leading-relaxed text-[#65625A]">
                      Prioriza consultas gratuitas e cache em banco antes de acionar agregadores
                      pagos, economizando créditos.
                    </p>
                  </div>

                  <div className="rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-4">
                    <PackageSearch className="h-5 w-5 text-[#C47D2B]" aria-hidden="true" />
                    <h3 className="mt-2.5 text-sm font-bold text-[#23221F]">
                      Status Padronizados
                    </h3>
                    <p className="mt-1 text-xs leading-relaxed text-[#65625A]">
                      Traduz mensagens técnicas de cada transportadora em 4 etapas claras, mantendo
                      o texto original visível.
                    </p>
                  </div>

                  <div className="rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-4">
                    <Compass className="h-5 w-5 text-[#B85D3B]" aria-hidden="true" />
                    <h3 className="mt-2.5 text-sm font-bold text-[#23221F]">
                      CDD &amp; Retirada GPS
                    </h3>
                    <p className="mt-1 text-xs leading-relaxed text-[#65625A]">
                      Quando alocado no CDD, traça a distância até você com Leaflet, Haversine e
                      atalhos para Google Maps e Waze.
                    </p>
                  </div>
                </div>
              </section>

              <RecentSearches
                items={recentItems}
                onSelectCode={handleSelectCode}
              />
            </div>

            <aside className="lg:col-span-5">
              <ProviderStatusPanel
                result={null}
                health={health}
                healthError={healthError}
                carriers={carriers}
                refreshing={refreshing}
                cooldownRemaining={cooldownRemaining}
                cooldownMessage={cooldownMessage}
                onRefreshNow={handleRefreshNow}
                onSelectSampleCode={handleSelectCode}
                onReloadHealth={() => void loadHealthAndRecent()}
              />
            </aside>
          </div>
        )}
      </main>

      <footer className="mt-10 border-t border-[#DCD5C9] bg-[#FCFBF8]">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-4 text-xs text-[#65625A] sm:px-6 lg:px-8">
          <p>
            <strong className="font-semibold text-[#23221F]">Rapid Screening</strong> —
            Interface acolhedora para rastreamento e retirada presencial em CDD.
          </p>
          <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span>
              Todas as consultas trafegam exclusivamente pelo backend interno (<code>/api/v1</code>).
            </span>
            <span aria-hidden="true">·</span>
            <span>
              Suporte:{' '}
              <a
                href="https://www.siterastreio.com.br/"
                target="_blank"
                rel="noopener noreferrer"
                className="font-semibold text-[#2E5A44] underline decoration-[#BBD4C6] underline-offset-2 hover:text-[#234735]"
              >
                Rastreamento
              </a>
            </span>
          </p>
        </div>
      </footer>
    </div>
  );
}

export default App;
