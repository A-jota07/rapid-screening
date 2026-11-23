import { Building2, Clock, History, ArrowUpRight, Trash2 } from 'lucide-react';
import type { RecentTrackingSummary } from '../types/tracking';
import { formatRelativePtBR } from '../utils/format';
import { StatusBadge } from './StatusBadge';

interface RecentSearchesProps {
  items: RecentTrackingSummary[];
  activeCode?: string;
  onSelectCode: (code: string) => void;
  onClearAll?: () => void;
}

export function RecentSearches({
  items,
  activeCode,
  onSelectCode,
  onClearAll,
}: RecentSearchesProps) {
  return (
    <section
      aria-labelledby="recent-heading"
      className="rs-card rounded-2xl border border-[#DCD5C9] bg-[#FCFBF8] p-5 shadow-sm sm:p-6"
    >
      <div className="mb-4 flex items-center justify-between gap-2 border-b border-[#EAE5DC] pb-4">
        <div className="flex items-center gap-2">
          <History className="h-5 w-5 text-[#2E5A44]" aria-hidden="true" />
          <div>
            <h2 id="recent-heading" className="text-lg font-bold text-[#23221F]">
              Rastreamentos Recentes
            </h2>
            <p className="text-xs text-[#65625A]">
              Consultas salvas localmente neste navegador.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {items.length > 0 && onClearAll && (
            <button
              type="button"
              onClick={onClearAll}
              aria-label="Limpar histórico local de rastreamentos"
              className="inline-flex items-center gap-1 rounded-lg border border-[#DCD5C9] bg-[#F6F4EE] px-2.5 py-1 text-xs font-medium text-[#65625A] transition-colors hover:bg-[#F7EAE4] hover:text-[#8F4124]"
            >
              <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Limpar</span>
            </button>
          )}
          <span className="rounded-full border border-[#DCD5C9] bg-[#F6F4EE] px-2.5 py-0.5 font-mono text-xs font-semibold text-[#65625A]">
            {items.length}
          </span>
        </div>
      </div>

      {items.length === 0 ? (
        <p className="rounded-xl border border-dashed border-[#DCD5C9] bg-[#F6F4EE] p-5 text-center text-sm text-[#65625A]">
          Nenhum rastreamento salvo ainda. Consulte um dos códigos de exemplo acima para iniciar.
        </p>
      ) : (
        <ul className="space-y-2.5">
          {items.map((item) => {
            const isSelected = activeCode === item.trackingCode;
            return (
              <li key={item.trackingCode}>
                <button
                  type="button"
                  onClick={() => onSelectCode(item.trackingCode)}
                  className={`group flex w-full flex-col gap-2 rounded-xl border p-3.5 text-left transition-colors ${
                    isSelected
                      ? 'border-[#2E5A44] bg-[#E6EFEA]/70'
                      : 'border-[#DCD5C9] bg-[#F6F4EE] hover:border-[#2E5A44]/60 hover:bg-[#EAE5DC]/60'
                  }`}
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-sm font-bold tracking-wide text-[#23221F]">
                        {item.trackingCode}
                      </span>
                      <span className="rounded bg-[#FCFBF8] px-2 py-0.5 text-[11px] font-semibold text-[#65625A] border border-[#DCD5C9]">
                        {item.carrierName}
                      </span>
                    </div>

                    <ArrowUpRight
                      className="h-4 w-4 text-[#65625A] transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5 group-hover:text-[#2E5A44]"
                      aria-hidden="true"
                    />
                  </div>

                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <StatusBadge
                      status={item.currentStatus}
                      label={item.currentStatusLabel}
                      size="sm"
                    />
                    <span className="inline-flex items-center gap-1 text-[11px] text-[#65625A]">
                      <Clock className="h-3 w-3" aria-hidden="true" />
                      {formatRelativePtBR(item.lastUpdatedAt)}
                    </span>
                  </div>

                  {item.cddName && (
                    <p className="inline-flex items-center gap-1.5 text-xs font-medium text-[#8F4124]">
                      <Building2 className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      <span className="truncate">{item.cddName}</span>
                    </p>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
