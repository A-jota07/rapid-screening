import { MapPin, Clock, FileText } from 'lucide-react';
import type { TrackingEvent } from '../types/tracking';
import { formatDateAndTimeParts } from '../utils/format';
import { StatusBadge } from './StatusBadge';

interface TimelineProps {
  events: TrackingEvent[];
  carrierName: string;
}

export function Timeline({ events, carrierName }: TimelineProps) {
  return (
    <section
      aria-labelledby="timeline-heading"
      className="rs-card rounded-2xl border border-[#DCD5C9] bg-[#FCFBF8] p-5 shadow-sm sm:p-6"
    >
      <div className="mb-5 flex flex-wrap items-center justify-between gap-2 border-b border-[#EAE5DC] pb-4">
        <div>
          <h2 id="timeline-heading" className="text-lg font-bold text-[#23221F]">
            Linha do Tempo da Encomenda
          </h2>
          <p className="text-sm text-[#65625A]">
            Histórico completo traduzido com preservação do texto original da {carrierName}.
          </p>
        </div>
        <span className="rounded-full border border-[#DCD5C9] bg-[#F6F4EE] px-3 py-1 text-xs font-semibold text-[#65625A]">
          {events.length} {events.length === 1 ? 'evento' : 'eventos'}
        </span>
      </div>

      {events.length === 0 ? (
        <p className="rounded-xl border border-dashed border-[#DCD5C9] bg-[#F6F4EE] p-6 text-center text-sm text-[#65625A]">
          Nenhum evento detalhado foi retornado até o momento.
        </p>
      ) : (
        <ol className="relative space-y-5 before:absolute before:top-3 before:bottom-3 before:left-[15px] before:w-0.5 before:bg-[#DCD5C9]">
          {events.map((ev, index) => {
            const isLatest = index === 0;
            const { date, time } = formatDateAndTimeParts(ev.timestamp);

            const dotStyle = isLatest
              ? ev.standardStatus === 'AT_CDD' || ev.standardStatus === 'EXCEPTION'
                ? 'border-[#B85D3B] bg-[#B85D3B] ring-4 ring-[#F7EAE4]'
                : 'border-[#2E5A44] bg-[#2E5A44] ring-4 ring-[#E6EFEA]'
              : 'border-[#DCD5C9] bg-[#FCFBF8]';

            return (
              <li key={`${ev.timestamp}-${index}`} className="relative pl-10">
                <span
                  aria-hidden="true"
                  className={`absolute top-2.5 left-2 h-4 w-4 rounded-full border-2 ${dotStyle}`}
                />

                <div
                  className={`rounded-xl border p-4 transition-colors ${
                    isLatest
                      ? 'border-[#BBD4C6] bg-[#F6F4EE]'
                      : 'border-[#DCD5C9] bg-[#FCFBF8]'
                  }`}
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <StatusBadge
                        status={ev.standardStatus}
                        label={ev.standardLabel}
                        size="sm"
                      />
                      {isLatest && (
                        <span className="rounded-md bg-[#2E5A44] px-2 py-0.5 text-[11px] font-semibold text-[#FCFBF8]">
                          Mais recente
                        </span>
                      )}
                    </div>

                    <div className="inline-flex items-center gap-1.5 font-mono text-xs text-[#65625A]">
                      <Clock className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      <time dateTime={ev.timestamp}>
                        {date}
                        {time ? ` · ${time}` : ''}
                      </time>
                    </div>
                  </div>

                  <p className="mt-2.5 text-sm font-medium text-[#23221F] leading-relaxed">
                    {ev.description}
                  </p>

                  <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-[#65625A]">
                    {ev.location && (
                      <span className="inline-flex items-center gap-1.5 font-medium text-[#23221F]">
                        <MapPin className="h-3.5 w-3.5 shrink-0 text-[#2E5A44]" aria-hidden="true" />
                        <span>{ev.location}</span>
                      </span>
                    )}

                    {ev.rawStatus && (
                      <span className="inline-flex items-center gap-1.5 rounded-md border border-[#DCD5C9] bg-[#EAE5DC]/70 px-2 py-0.5 text-[11px] text-[#65625A]">
                        <FileText className="h-3 w-3 shrink-0" aria-hidden="true" />
                        <span>
                          Original: <strong className="font-medium text-[#23221F]">{ev.rawStatus}</strong>
                        </span>
                      </span>
                    )}
                  </div>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
