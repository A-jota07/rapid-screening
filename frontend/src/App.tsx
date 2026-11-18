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
  fetchRecentTrackings,
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
  const [recentItems, setRecentItems] = useState<RecentTrackingSummary[]>([]);

  const [cooldownRemaining, setCooldownRemaining] = useState(0);
  const [cooldownMessage, setCooldownMessage] = useState<string | null>(null);

  const activeRequestRef = useRef<AbortController | null>(null);

  const loadHealthAndRecent = useCallback(async () => {
    try {
      const [healthData, recentData] = await Promise.all([
        fetchHealth(),
        fetchRecentTrackings(),
      ]);
      setHealth(healthData);
      setHealthError(null);
      setRecentItems(recentData.items);
    } catch (err) {
      const msg =
        err instanceof Error
          ? err.message
          : 'Não foi possível conectar ao servidor backend.';
      setHealthError(msg);
    }
  }, []);

  // Load initial health, carriers, and recent trackings
  useEffect(() => {
    const controller = new AbortController();

    async function bootstrap() {
      const [healthRes, carriersRes, recentRes] = await Promise.allSettled([
        fetchHealth(controller.signal),
        fetchCarriers(controller.signal),
        fetchRecentTrackings(controller.signal),
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

      if (recentRes.status === 'fulfilled') {
        setRecentItems(recentRes.value.items);
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

          <div className="flex items-center gap-2.5">
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
              Suporte de rastreamento nacional:{' '}
              <a
                href="https://www.siterastreio.com.br"
                target="_blank"
                rel="noopener noreferrer"
                className="font-semibold text-[#2E5A44] underline decoration-[#BBD4C6] underline-offset-2 hover:text-[#234735]"
              >
                Site Rastreio
              </a>
            </span>
          </p>
        </div>
      </footer>
    </div>
  );
}

export default App;
