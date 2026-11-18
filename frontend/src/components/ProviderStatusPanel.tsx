import { useEffect, useState } from 'react';
import {
  Activity,
  CheckCircle2,
  ChevronRight,
  Database,
  RefreshCw,
  ShieldAlert,
  Sparkles,
  Server,
  Clock,
} from 'lucide-react';
import type {
  CarrierInfo,
  HealthResponse,
  TrackingResult,
} from '../types/tracking';
import {
  formatDateTimePtBR,
  formatProviderName,
  formatRelativePtBR,
} from '../utils/format';

interface ProviderStatusPanelProps {
  result: TrackingResult | null;
  health: HealthResponse | null;
  healthError: string | null;
  carriers: CarrierInfo[];
  refreshing: boolean;
  cooldownRemaining: number;
  cooldownMessage: string | null;
  onRefreshNow: () => void;
  onSelectSampleCode: (code: string) => void;
  onReloadHealth: () => void;
}

export function ProviderStatusPanel({
  result,
  health,
  healthError,
  carriers,
  refreshing,
  cooldownRemaining,
  cooldownMessage,
  onRefreshNow,
  onSelectSampleCode,
  onReloadHealth,
}: ProviderStatusPanelProps) {
  // Subtle tick every 30s to keep relative timestamps fresh while visible
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => setTick((t) => t + 1), 30_000);
    return () => window.clearInterval(id);
  }, []);

  return (
    <section
      aria-labelledby="providers-heading"
      className="rs-card space-y-5 rounded-2xl border border-[#DCD5C9] bg-[#FCFBF8] p-5 shadow-sm sm:p-6"
    >
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[#EAE5DC] pb-4">
        <div>
          <h2 id="providers-heading" className="text-lg font-bold text-[#23221F]">
            Provedores &amp; Economia de Cache
          </h2>
          <p className="text-xs text-[#65625A]">
            Roteamento em cadeia com cache inteligente e proteção de créditos.
          </p>
        </div>

        <button
          type="button"
          onClick={onReloadHealth}
          className="inline-flex min-h-[38px] items-center gap-1.5 rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] px-3 py-1.5 text-xs font-semibold text-[#23221F] transition-colors hover:bg-[#EAE5DC]"
        >
          <Activity className="h-3.5 w-3.5 text-[#2E5A44]" aria-hidden="true" />
          <span>Atualizar saúde</span>
        </button>
      </div>

      {/* Current Tracking Source & Refresh Control */}
      {result && (
        <div className="rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-4 space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="inline-flex items-center gap-1.5 rounded-full border border-[#BBD4C6] bg-[#E6EFEA] px-3 py-1 text-xs font-semibold text-[#234735]">
              {result.cached || result.providerUsed === 'cache' ? (
                <Database className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              ) : (
                <Sparkles className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              )}
              <span>{formatProviderName(result.providerUsed, result.cached)}</span>
            </span>

            <button
              type="button"
              onClick={onRefreshNow}
              disabled={refreshing || cooldownRemaining > 0}
              className="inline-flex min-h-[40px] items-center gap-1.5 rounded-xl bg-[#2E5A44] px-3.5 py-2 text-xs font-semibold text-[#FCFBF8] transition-colors hover:bg-[#234735] disabled:cursor-not-allowed disabled:opacity-65"
            >
              <RefreshCw
                className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`}
                aria-hidden="true"
              />
              <span>
                {refreshing
                  ? 'Atualizando...'
                  : cooldownRemaining > 0
                    ? `Aguarde ${cooldownRemaining}s`
                    : 'Atualizar agora'}
              </span>
            </button>
          </div>

          <div className="grid grid-cols-1 gap-2 text-xs text-[#65625A] sm:grid-cols-2">
            <div className="flex items-center gap-1.5">
              <Clock className="h-3.5 w-3.5 shrink-0 text-[#2E5A44]" aria-hidden="true" />
              <span>
                Atualizado: <strong className="text-[#23221F]">{formatDateTimePtBR(result.lastUpdatedAt)}</strong>{' '}
                ({formatRelativePtBR(result.lastUpdatedAt)})
              </span>
            </div>
            {result.cacheExpiresAt && (
              <div className="flex items-center gap-1.5">
                <Database className="h-3.5 w-3.5 shrink-0 text-[#C47D2B]" aria-hidden="true" />
                <span>
                  Cache válido até:{' '}
                  <strong className="text-[#23221F]">
                    {formatDateTimePtBR(result.cacheExpiresAt)}
                  </strong>
                </span>
              </div>
            )}
          </div>

          {cooldownMessage && (
            <p
              role="status"
              className="rounded-lg border border-[#E4CFA8] bg-[#F9F0E1] px-3 py-2 text-xs font-medium text-[#835014]"
            >
              {cooldownMessage}
            </p>
          )}
        </div>
      )}

      {/* Backend Health & Cache KPIs */}
      {healthError ? (
        <div
          role="status"
          className="rounded-xl border border-[#E3C1B2] bg-[#F7EAE4] p-3.5 text-xs text-[#8F4124]"
        >
          <p className="font-semibold">Backend /api/v1/health indisponível no momento</p>
          <p className="mt-0.5">{healthError}</p>
        </div>
      ) : health ? (
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
            <div className="rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-3">
              <span className="block text-[11px] font-semibold text-[#65625A] uppercase">
                Cache Hits
              </span>
              <strong className="mt-1 block font-mono text-lg font-bold text-[#2E5A44]">
                {health.cache.hits}
              </strong>
            </div>
            <div className="rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-3">
              <span className="block text-[11px] font-semibold text-[#65625A] uppercase">
                Créditos Salvos
              </span>
              <strong className="mt-1 block font-mono text-lg font-bold text-[#2E5A44]">
                {health.cache.creditsSaved}
              </strong>
            </div>
            <div className="rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-3">
              <span className="block text-[11px] font-semibold text-[#65625A] uppercase">
                Salvos em Banco
              </span>
              <strong className="mt-1 block font-mono text-lg font-bold text-[#23221F]">
                {health.cache.storedCount}
              </strong>
            </div>
            <div className="rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-3">
              <span className="block text-[11px] font-semibold text-[#65625A] uppercase">
                Cooldown
              </span>
              <strong className="mt-1 block font-mono text-lg font-bold text-[#23221F]">
                {health.cache.cooldownSeconds}s
              </strong>
            </div>
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between text-xs text-[#65625A]">
              <span className="font-semibold uppercase tracking-wide">
                Cadeia de Provedores
              </span>
              <span className="font-mono text-[11px]">
                Storage: {health.cache.backendMode}
              </span>
            </div>

            <ul className="space-y-2">
              {health.providers.map((provider) => {
                const isHealthy =
                  provider.enabled && provider.available && !provider.circuitOpen;
                const statusBadge = provider.circuitOpen
                  ? 'bg-[#F7EAE4] text-[#8F4124] border-[#E3C1B2]'
                  : isHealthy
                    ? 'bg-[#E6EFEA] text-[#234735] border-[#BBD4C6]'
                    : 'bg-[#EAE5DC] text-[#65625A] border-[#DCD5C9]';

                return (
                  <li
                    key={provider.name}
                    className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] px-3.5 py-2.5 text-xs"
                  >
                    <div className="flex items-center gap-2.5 min-w-0">
                      <Server className="h-4 w-4 shrink-0 text-[#65625A]" aria-hidden="true" />
                      <div className="min-w-0">
                        <p className="font-semibold text-[#23221F] truncate">
                          {provider.name}
                        </p>
                        <p className="text-[11px] text-[#65625A]">
                          Modo: <span className="font-mono">{provider.mode}</span> · Uso:{' '}
                          {provider.requestsUsed} · Falhas: {provider.failures}
                          {provider.lastLatencyMs > 0
                            ? ` · ${provider.lastLatencyMs} ms`
                            : ''}
                        </p>
                      </div>
                    </div>

                    <div className="flex items-center gap-2">
                      <span className="font-mono text-[11px] text-[#65625A]">
                        {provider.creditsRemaining < 0
                          ? 'Ilimitado'
                          : `${provider.creditsRemaining} créd.`}
                      </span>
                      <span
                        className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-semibold ${statusBadge}`}
                      >
                        {provider.circuitOpen ? (
                          <>
                            <ShieldAlert className="h-3 w-3" aria-hidden="true" />
                            Circuito aberto
                          </>
                        ) : isHealthy ? (
                          <>
                            <CheckCircle2 className="h-3 w-3" aria-hidden="true" />
                            Ativo
                          </>
                        ) : (
                          'Standby'
                        )}
                      </span>
                    </div>
                  </li>
                );
              })}
            </ul>
          </div>
        </div>
      ) : (
        <div className="rounded-xl border border-[#EAE5DC] bg-[#F6F4EE] p-4 text-xs text-[#65625A]">
          Carregando telemetria de provedores...
        </div>
      )}

      {/* Collapsible Supported Carriers & Regex Patterns via native <details> */}
      <details className="rs-disclosure rounded-xl border border-[#DCD5C9] bg-[#F6F4EE]">
        <summary className="flex min-h-[44px] items-center justify-between gap-2 px-4 py-3 text-sm font-semibold text-[#23221F] hover:bg-[#EAE5DC]/60 rounded-xl">
          <span className="flex items-center gap-2">
            <ChevronRight
              className="rs-chevron h-4 w-4 shrink-0 text-[#2E5A44]"
              aria-hidden="true"
            />
            <span>Transportadoras Suportadas &amp; Padrões Regex</span>
          </span>
          <span className="rounded-full bg-[#FCFBF8] border border-[#DCD5C9] px-2.5 py-0.5 font-mono text-xs text-[#65625A]">
            {carriers.length}
          </span>
        </summary>

        <div className="border-t border-[#DCD5C9] px-4 py-3">
          {carriers.length === 0 ? (
            <p className="text-xs text-[#65625A]">
              Nenhuma lista de transportadoras carregada da API no momento.
            </p>
          ) : (
            <ul className="space-y-2.5">
              {carriers.map((carrier) => (
                <li
                  key={carrier.code}
                  className="rounded-xl border border-[#DCD5C9] bg-[#FCFBF8] p-3 text-xs"
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="font-bold text-[#23221F]">{carrier.name}</span>
                    <span className="font-mono text-[11px] text-[#65625A]">
                      {carrier.code}
                    </span>
                  </div>
                  {carrier.patternDescription && (
                    <p className="mt-1 text-[#65625A]">{carrier.patternDescription}</p>
                  )}
                  {carrier.regexPattern && (
                    <p className="mt-1.5 overflow-x-auto rounded-md bg-[#F6F4EE] px-2 py-1 font-mono text-[11px] text-[#2E5A44]">
                      {carrier.regexPattern}
                    </p>
                  )}
                  {carrier.sampleCodes && carrier.sampleCodes.length > 0 && (
                    <div className="mt-2 flex flex-wrap items-center gap-1.5">
                      <span className="text-[11px] text-[#65625A]">Testar:</span>
                      {carrier.sampleCodes.map((sample) => (
                        <button
                          key={sample}
                          type="button"
                          onClick={() => onSelectSampleCode(sample)}
                          className="rounded-md border border-[#DCD5C9] bg-[#F6F4EE] px-2 py-1 font-mono text-[11px] font-semibold text-[#2E5A44] transition-colors hover:border-[#2E5A44] hover:bg-[#E6EFEA]"
                        >
                          {sample}
                        </button>
                      ))}
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      </details>
    </section>
  );
}
