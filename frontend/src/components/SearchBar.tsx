import { useEffect, useId, useRef, useState } from 'react';
import { Loader2, Search, Sparkles, X, CheckCircle2, AlertCircle } from 'lucide-react';
import { detectCarrier } from '../api/client';
import type { CarrierInfo } from '../types/tracking';
import {
  QUICK_SAMPLE_CHIPS,
  detectCarrierLocally,
  sanitizeTrackingInput,
} from '../utils/carrierDetect';

interface SearchBarProps {
  value: string;
  onChange: (nextValue: string) => void;
  onSubmitCode: (code: string) => void;
  loading: boolean;
  supportedCarriers: CarrierInfo[];
}

export function SearchBar({
  value,
  onChange,
  onSubmitCode,
  loading,
  supportedCarriers,
}: SearchBarProps) {
  const inputId = useId();
  const helperId = useId();
  const errorId = useId();
  const inputRef = useRef<HTMLInputElement>(null);

  const [validationError, setValidationError] = useState<string | null>(null);
  const [apiCarrier, setApiCarrier] = useState<CarrierInfo | null>(null);
  const [detectingApi, setDetectingApi] = useState(false);

  const cleanValue = sanitizeTrackingInput(value);
  const localCarrier = detectCarrierLocally(cleanValue, supportedCarriers);
  const activeDetection = apiCarrier ?? localCarrier;

  // Confirm local carrier detection against backend /api/v1/carriers/detect (debounced)
  useEffect(() => {
    if (cleanValue.length < 6) {
      setApiCarrier(null);
      setDetectingApi(false);
      return;
    }

    const controller = new AbortController();
    const timer = window.setTimeout(async () => {
      setDetectingApi(true);
      try {
        const result = await detectCarrier(cleanValue, controller.signal);
        if (!controller.signal.aborted && result?.name) {
          setApiCarrier(result);
        }
      } catch {
        // Silent fallback to instant local regex detection when offline or interrupted
      } finally {
        if (!controller.signal.aborted) {
          setDetectingApi(false);
        }
      }
    }, 240);

    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [cleanValue]);

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const next = sanitizeTrackingInput(e.target.value);
    onChange(next);
    if (validationError && next.length >= 4) {
      setValidationError(null);
    }
  };

  const handleSubmit = (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const sanitized = sanitizeTrackingInput(value);
    if (sanitized.length < 4) {
      setValidationError(
        'Informe um código de rastreio válido com pelo menos 4 caracteres alfanuméricos.',
      );
      inputRef.current?.focus();
      return;
    }
    setValidationError(null);
    onChange(sanitized);
    onSubmitCode(sanitized);
  };

  const handleChipClick = (code: string) => {
    setValidationError(null);
    onChange(code);
    onSubmitCode(code);
  };

  const handleClear = () => {
    setValidationError(null);
    setApiCarrier(null);
    onChange('');
    inputRef.current?.focus();
  };

  const describedBy = validationError ? `${helperId} ${errorId}` : helperId;

  return (
    <section
      aria-labelledby="search-heading"
      className="rs-card rounded-2xl border border-[#DCD5C9] bg-[#FCFBF8] p-5 shadow-sm sm:p-7"
    >
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2
            id="search-heading"
            className="text-lg font-bold tracking-tight text-[#23221F] sm:text-xl"
          >
            Consultar encomenda
          </h2>
          <p id={helperId} className="mt-1 text-sm text-[#65625A]">
            Digite o código de rastreio para identificar a transportadora automaticamente, visualizar
            a rota e verificar se o objeto está disponível para retirada no CDD.
          </p>
        </div>
      </div>

      <search>
        <form onSubmit={handleSubmit} noValidate className="space-y-3">
          <label
            htmlFor={inputId}
            className="block text-sm font-semibold text-[#23221F]"
          >
            Código de rastreamento
          </label>

          <div className="flex flex-col gap-3 sm:flex-row sm:items-stretch">
            <div className="relative flex-1">
              <Search
                className="pointer-events-none absolute top-1/2 left-4 h-5 w-5 -translate-y-1/2 text-[#65625A]"
                aria-hidden="true"
              />
              <input
                ref={inputRef}
                id={inputId}
                name="trackingCode"
                type="search"
                value={value}
                onChange={handleInputChange}
                placeholder="Ex.: AA123456789BR ou LGG84920194BR"
                autoComplete="off"
                autoCorrect="off"
                autoCapitalize="characters"
                spellCheck={false}
                enterKeyHint="search"
                aria-describedby={describedBy}
                aria-invalid={validationError ? 'true' : undefined}
                className={`min-h-[54px] w-full rounded-xl border bg-[#F6F4EE] py-3 pr-11 pl-12 font-mono text-base font-medium tracking-wider text-[#23221F] placeholder:font-sans placeholder:text-sm placeholder:font-normal placeholder:tracking-normal placeholder:text-[#65625A]/80 transition-colors ${
                  validationError
                    ? 'border-[#B85D3B] bg-[#F7EAE4]/40'
                    : 'border-[#DCD5C9] hover:border-[#65625A]/60 focus:border-[#2E5A44] focus:bg-[#FCFBF8]'
                }`}
              />
              {value.length > 0 && (
                <button
                  type="button"
                  onClick={handleClear}
                  aria-label="Limpar código digitado"
                  className="absolute top-1/2 right-2 flex h-9 w-9 -translate-y-1/2 items-center justify-center rounded-lg text-[#65625A] transition-colors hover:bg-[#EAE5DC] hover:text-[#23221F]"
                >
                  <X className="h-4 w-4" aria-hidden="true" />
                </button>
              )}
            </div>

            <button
              type="submit"
              disabled={loading}
              className="inline-flex min-h-[54px] shrink-0 items-center justify-center gap-2 rounded-xl bg-[#2E5A44] px-6 py-3 text-base font-semibold text-[#FCFBF8] shadow-sm transition-colors hover:bg-[#234735] disabled:cursor-not-allowed disabled:opacity-70"
            >
              {loading ? (
                <>
                  <Loader2 className="h-5 w-5 animate-spin" aria-hidden="true" />
                  <span>Consultando...</span>
                </>
              ) : (
                <>
                  <Search className="h-5 w-5" aria-hidden="true" />
                  <span>Rastrear</span>
                </>
              )}
            </button>
          </div>

          {validationError && (
            <div
              id={errorId}
              role="alert"
              className="flex items-center gap-2 rounded-xl border border-[#E3C1B2] bg-[#F7EAE4] px-3.5 py-2.5 text-sm font-medium text-[#8F4124]"
            >
              <AlertCircle className="h-4 w-4 shrink-0" aria-hidden="true" />
              <span>{validationError}</span>
            </div>
          )}

          {/* Live carrier detection badge */}
          <div
            aria-live="polite"
            className="min-h-[38px] flex flex-wrap items-center gap-2 pt-0.5"
          >
            {activeDetection &&
            activeDetection.code !== 'auto' &&
            activeDetection.confidence >= 0.7 ? (
              <div className="rs-badge inline-flex flex-wrap items-center gap-2 rounded-xl border border-[#BBD4C6] bg-[#E6EFEA] px-3 py-1.5 text-xs sm:text-sm text-[#234735]">
                <CheckCircle2 className="h-4 w-4 shrink-0 text-[#2E5A44]" aria-hidden="true" />
                <span className="font-semibold">
                  Transportadora detectada: {activeDetection.name}
                </span>
                <span className="rounded-md bg-[#FCFBF8]/80 px-2 py-0.5 font-mono text-xs text-[#2E5A44]">
                  {Math.round((activeDetection.confidence || 0.9) * 100)}% ·{' '}
                  {apiCarrier && apiCarrier.code !== 'auto' ? 'API + Regex' : 'Regex local'}
                </span>
                {activeDetection.patternDescription && (
                  <span className="text-xs text-[#2E5A44]/90">
                    ({activeDetection.patternDescription})
                  </span>
                )}
                {detectingApi && (
                  <Loader2
                    className="h-3.5 w-3.5 animate-spin text-[#2E5A44]"
                    aria-hidden="true"
                  />
                )}
              </div>
            ) : activeDetection ? (
              <div className="rs-badge inline-flex flex-wrap items-center gap-2 rounded-xl border border-[#E4CFA8] bg-[#F9F0E1] px-3 py-1.5 text-xs text-[#835014]">
                <Sparkles className="h-3.5 w-3.5 shrink-0 text-[#C47D2B]" aria-hidden="true" />
                <span className="font-semibold">{activeDetection.name}</span>
                {activeDetection.patternDescription && (
                  <span>({activeDetection.patternDescription})</span>
                )}
              </div>
            ) : cleanValue.length >= 4 ? (
              <div className="inline-flex items-center gap-2 rounded-xl border border-[#DCD5C9] bg-[#EAE5DC]/60 px-3 py-1.5 text-xs text-[#65625A]">
                <Sparkles className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                <span>
                  {detectingApi
                    ? 'Verificando padrão na API...'
                    : 'Padrão genérico — ao rastrear, o sistema consultará os provedores em cadeia.'}
                </span>
              </div>
            ) : (
              <span className="text-xs text-[#65625A]">
                Detecção automática ativa: Correios, Loggi, Azul Cargo, Mandaê, Magalog, Jadlog,
                Total Express e mais.
              </span>
            )}
          </div>
        </form>
      </search>

      {/* 1-click quick test chips */}
      <div className="mt-5 border-t border-[#EAE5DC] pt-4">
        <p className="mb-2.5 text-xs font-semibold tracking-wide text-[#65625A] uppercase">
          Exemplos rápidos com 1 clique
        </p>
        <div className="flex flex-wrap gap-2">
          {QUICK_SAMPLE_CHIPS.map((chip) => {
            const isSelected = cleanValue === chip.code;
            return (
              <button
                key={chip.code}
                type="button"
                onClick={() => handleChipClick(chip.code)}
                className={`group flex min-h-[44px] flex-col items-start rounded-xl border px-3 py-2 text-left transition-colors ${
                  isSelected
                    ? 'border-[#2E5A44] bg-[#E6EFEA] text-[#234735]'
                    : 'border-[#DCD5C9] bg-[#F6F4EE] text-[#23221F] hover:border-[#2E5A44]/60 hover:bg-[#EAE5DC]/70'
                }`}
              >
                <span className="font-mono text-xs font-semibold tracking-wide">
                  {chip.code}
                </span>
                <span className="text-[11px] text-[#65625A] group-hover:text-[#23221F]">
                  {chip.carrier} — {chip.hint}
                </span>
              </button>
            );
          })}
        </div>
      </div>
    </section>
  );
}
