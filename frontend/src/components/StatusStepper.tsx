import { useState } from 'react';
import {
  AlertTriangle,
  Building2,
  CalendarClock,
  Check,
  ChevronDown,
  HelpCircle,
  Package,
  Truck,
  CheckCircle2,
} from 'lucide-react';
import type { TrackingResult } from '../types/tracking';
import { getStatusStyleMeta } from '../utils/format';
import { StatusBadge } from './StatusBadge';

interface StatusStepperProps {
  result: TrackingResult;
}

export function StatusStepper({ result }: StatusStepperProps) {
  const [showTranslation, setShowTranslation] = useState(false);

  const isException = result.currentStatus === 'EXCEPTION';
  const clampedStep = Math.min(4, Math.max(1, result.progressStep || 1));
  const latestEvent = result.events?.[0];
  const statusMeta = getStatusStyleMeta(result.currentStatus);

  const step3Title =
    result.currentStatus === 'AT_CDD'
      ? 'No CDD (Retirada)'
      : result.currentStatus === 'OUT_FOR_DELIVERY'
        ? 'Saiu p/ entrega'
        : 'No CDD / Saiu p/ entrega';

  const steps = [
    {
      step: 1,
      title: 'Postado',
      subtitle: 'Coleta / Origem',
      icon: Package,
    },
    {
      step: 2,
      title: 'Em trânsito',
      subtitle: 'Entre unidades',
      icon: Truck,
    },
    {
      step: 3,
      title: step3Title,
      subtitle: result.cdd ? result.cdd.city : 'Unidade local',
      icon: Building2,
    },
    {
      step: 4,
      title: 'Entregue',
      subtitle: 'Finalizado',
      icon: CheckCircle2,
    },
  ];

  return (
    <section
      aria-labelledby="stepper-heading"
      className="rs-card rounded-2xl border border-[#DCD5C9] bg-[#FCFBF8] p-5 shadow-sm sm:p-6"
    >
      {/* Top summary row */}
      <div className="flex flex-col gap-4 border-b border-[#EAE5DC] pb-5 lg:flex-row lg:items-center lg:justify-between">
        <div className="space-y-1.5">
          <div className="flex flex-wrap items-center gap-2.5">
            <h2
              id="stepper-heading"
              className="font-mono text-xl font-bold tracking-wide text-[#23221F] sm:text-2xl"
            >
              {result.trackingCode}
            </h2>
            <span className="rounded-lg border border-[#DCD5C9] bg-[#EAE5DC] px-2.5 py-0.5 text-xs font-semibold text-[#23221F]">
              {result.carrier.name}
            </span>
          </div>

          <div className="flex flex-wrap items-center gap-3 pt-1">
            <StatusBadge
              status={result.currentStatus}
              label={result.currentStatusLabel}
            />
            {result.estimatedDelivery && (
              <span className="inline-flex items-center gap-1.5 text-sm text-[#65625A]">
                <CalendarClock className="h-4 w-4 shrink-0 text-[#2E5A44]" aria-hidden="true" />
                <span>
                  Previsão:{' '}
                  <strong className="font-semibold text-[#23221F]">
                    {result.estimatedDelivery}
                  </strong>
                </span>
              </span>
            )}
          </div>
        </div>

        <button
          type="button"
          onClick={() => setShowTranslation((prev) => !prev)}
          aria-expanded={showTranslation}
          className="inline-flex min-h-[44px] items-center justify-center gap-2 self-start rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] px-4 py-2 text-sm font-medium text-[#23221F] transition-colors hover:bg-[#EAE5DC] lg:self-center"
        >
          <HelpCircle className="h-4 w-4 shrink-0 text-[#2E5A44]" aria-hidden="true" />
          <span>Entender tradução do status</span>
          <ChevronDown
            className={`h-4 w-4 shrink-0 text-[#65625A] transition-transform ${
              showTranslation ? 'rotate-180' : ''
            }`}
            aria-hidden="true"
          />
        </button>
      </div>

      {/* Expandable status translation card */}
      {showTranslation && (
        <div className="mt-4 rounded-xl border border-[#DCD5C9] bg-[#F6F4EE] p-4 text-sm text-[#23221F]">
          <p className="font-semibold text-[#2E5A44]">
            Por que mostramos &ldquo;{result.currentStatusLabel}&rdquo;?
          </p>
          <p className="mt-1 text-[#65625A]">{statusMeta.explanation}</p>
          {latestEvent && (
            <div className="mt-3 grid gap-2 rounded-lg border border-[#DCD5C9] bg-[#FCFBF8] p-3 sm:grid-cols-2">
              <div>
                <span className="block text-[11px] font-semibold tracking-wide text-[#65625A] uppercase">
                  Texto original da {result.carrier.name}
                </span>
                <span className="mt-0.5 block font-medium text-[#23221F]">
                  &ldquo;{latestEvent.rawStatus}&rdquo;
                </span>
              </div>
              <div>
                <span className="block text-[11px] font-semibold tracking-wide text-[#65625A] uppercase">
                  Estado padronizado Rapid Screening
                </span>
                <span className="mt-0.5 block font-mono text-xs font-semibold text-[#2E5A44]">
                  {result.currentStatus} → {result.currentStatusLabel}
                </span>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Exception calm notice if EXCEPTION */}
      {isException && (
        <div
          role="status"
          className="mt-4 flex items-start gap-3 rounded-xl border border-[#E3C1B2] bg-[#F7EAE4] p-4 text-sm text-[#8F4124]"
        >
          <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-[#B85D3B]" aria-hidden="true" />
          <div>
            <p className="font-semibold">
              Ocorrência registrada nesta etapa ({result.currentStatusLabel})
            </p>
            <p className="mt-0.5 text-[#8F4124]/90">
              {latestEvent?.description ||
                'Verifique a linha do tempo abaixo para conferir o detalhe informado pela transportadora.'}
            </p>
          </div>
        </div>
      )}

      {/* 4-Step horizontal / responsive stepper */}
      <nav aria-label="Progresso da entrega" className="mt-6">
        <ol className="grid grid-cols-1 gap-4 sm:grid-cols-4 sm:gap-2">
          {steps.map((item, idx) => {
            const StepIcon = item.icon;
            const isCompleted = !isException && clampedStep > item.step;
            const isCurrent = clampedStep === item.step;
            const isDoneAll = !isException && clampedStep === 4 && item.step === 4;

            let circleClasses =
              'border-[#DCD5C9] bg-[#EAE5DC] text-[#65625A]';
            let lineClasses = 'bg-[#DCD5C9]';
            let titleClasses = 'text-[#65625A]';

            if (isCompleted || isDoneAll) {
              circleClasses = 'border-[#2E5A44] bg-[#2E5A44] text-[#FCFBF8]';
              lineClasses = 'bg-[#2E5A44]';
              titleClasses = 'text-[#23221F]';
            } else if (isCurrent) {
              if (isException) {
                circleClasses =
                  'border-[#B85D3B] bg-[#F7EAE4] text-[#8F4124] ring-4 ring-[#F7EAE4]';
                titleClasses = 'text-[#8F4124]';
              } else if (result.currentStatus === 'AT_CDD') {
                circleClasses =
                  'border-[#B85D3B] bg-[#B85D3B] text-[#FCFBF8] ring-4 ring-[#F7EAE4]';
                titleClasses = 'text-[#8F4124]';
              } else {
                circleClasses =
                  'border-[#2E5A44] bg-[#E6EFEA] text-[#2E5A44] ring-4 ring-[#E6EFEA]';
                titleClasses = 'text-[#234735]';
              }
            }

            return (
              <li
                key={item.step}
                aria-current={isCurrent ? 'step' : undefined}
                className="relative flex items-center gap-3 sm:flex-col sm:items-center sm:text-center"
              >
                {/* Connector line on sm+ screens */}
                {idx < steps.length - 1 && (
                  <div
                    aria-hidden="true"
                    className={`hidden sm:block absolute top-5 left-[calc(50%+22px)] right-[calc(-50%+22px)] h-0.5 ${
                      clampedStep > item.step ? 'bg-[#2E5A44]' : lineClasses
                    }`}
                  />
                )}

                <div
                  className={`relative z-10 flex h-10 w-10 shrink-0 items-center justify-center rounded-full border-2 transition-colors ${circleClasses}`}
                >
                  {isCompleted || isDoneAll ? (
                    <Check className="h-5 w-5" aria-hidden="true" />
                  ) : (
                    <StepIcon className="h-5 w-5" aria-hidden="true" />
                  )}
                </div>

                <div className="min-w-0">
                  <p className={`text-sm font-semibold leading-snug ${titleClasses}`}>
                    <span className="mr-1 font-mono text-xs opacity-80">{item.step}.</span>
                    {item.title}
                  </p>
                  <p className="text-xs text-[#65625A]">{item.subtitle}</p>
                </div>
              </li>
            );
          })}
        </ol>
      </nav>
    </section>
  );
}
