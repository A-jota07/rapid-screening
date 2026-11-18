import type { StandardStatus } from '../types/tracking';

const ptBrDateTime = new Intl.DateTimeFormat('pt-BR', {
  dateStyle: 'short',
  timeStyle: 'short',
});

const ptBrDateOnly = new Intl.DateTimeFormat('pt-BR', {
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
});

const ptBrTimeOnly = new Intl.DateTimeFormat('pt-BR', {
  hour: '2-digit',
  minute: '2-digit',
});

export function formatDateTimePtBR(isoString?: string): string {
  if (!isoString) return '—';
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) return isoString;
  return ptBrDateTime.format(date);
}

export function formatDateAndTimeParts(isoString?: string): {
  date: string;
  time: string;
} {
  if (!isoString) return { date: '—', time: '' };
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) return { date: isoString, time: '' };
  return {
    date: ptBrDateOnly.format(date),
    time: ptBrTimeOnly.format(date),
  };
}

export function formatRelativePtBR(isoString?: string): string {
  if (!isoString) return '';
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) return '';

  const diffSec = Math.round((date.getTime() - Date.now()) / 1000);
  const absSec = Math.abs(diffSec);

  if (absSec < 60) {
    return diffSec <= 0 ? 'agora mesmo' : 'em instantes';
  }
  const rtf = new Intl.RelativeTimeFormat('pt-BR', { numeric: 'auto' });
  if (absSec < 3600) {
    return rtf.format(Math.round(diffSec / 60), 'minute');
  }
  if (absSec < 86400) {
    return rtf.format(Math.round(diffSec / 3600), 'hour');
  }
  return rtf.format(Math.round(diffSec / 86400), 'day');
}

export interface StatusStyleMeta {
  badgeClasses: string;
  dotClasses: string;
  shortLabel: string;
  explanation: string;
}

export function getStatusStyleMeta(status: StandardStatus): StatusStyleMeta {
  switch (status) {
    case 'POSTED':
      return {
        badgeClasses: 'bg-[#EAE5DC] text-[#23221F] border-[#DCD5C9]',
        dotClasses: 'bg-[#65625A]',
        shortLabel: 'Postado',
        explanation:
          'A transportadora registrou o recebimento físico do pacote na unidade de origem.',
      };
    case 'IN_TRANSIT':
      return {
        badgeClasses: 'bg-[#F9F0E1] text-[#835014] border-[#E4CFA8]',
        dotClasses: 'bg-[#C47D2B]',
        shortLabel: 'Em trânsito',
        explanation:
          'A encomenda está em deslocamento entre centros de triagem e hubs regionais.',
      };
    case 'AT_CDD':
      return {
        badgeClasses: 'bg-[#F7EAE4] text-[#8F4124] border-[#E3C1B2]',
        dotClasses: 'bg-[#B85D3B]',
        shortLabel: 'Alocado no CDD',
        explanation:
          'O pacote chegou ao Centro de Distribuição Domiciliária (CDD) responsável pela sua região e está disponível ou em preparação para retirada.',
      };
    case 'OUT_FOR_DELIVERY':
      return {
        badgeClasses: 'bg-[#E6EFEA] text-[#234735] border-[#BBD4C6]',
        dotClasses: 'bg-[#2E5A44]',
        shortLabel: 'Saiu para entrega',
        explanation:
          'Um motorista ou carteiro já está em rota local realizando a entrega no endereço de destino.',
      };
    case 'DELIVERED':
      return {
        badgeClasses: 'bg-[#E6EFEA] text-[#234735] border-[#2E5A44]/40',
        dotClasses: 'bg-[#2E5A44]',
        shortLabel: 'Entregue',
        explanation:
          'Entrega concluída e confirmada pela transportadora no endereço de destino ou balcão de retirada.',
      };
    case 'EXCEPTION':
      return {
        badgeClasses: 'bg-[#F7EAE4] text-[#8F4124] border-[#B85D3B]/50',
        dotClasses: 'bg-[#B85D3B]',
        shortLabel: 'Ocorrência / Atenção',
        explanation:
          'Houve uma tentativa de entrega, pendência aduaneira, endereço divergente ou evento que requer verificação.',
      };
  }
}

export function formatProviderName(providerUsed: string, cached?: boolean): string {
  if (cached || providerUsed === 'cache') {
    return 'Cache Local — 0 créditos gastos';
  }
  switch (providerUsed) {
    case 'free_direct':
      return 'Free Direct (Consulta Direta Gratuita)';
    case 'siterastreio':
      return 'Site Rastreio (1.000 grátis/mês)';
    case '17track':
      return '17TRACK (Agregador Primário)';
    case 'trackingmore':
      return 'TrackingMore (Agregador Secundário)';
    case 'smart_demo_fallback':
      return 'Smart Fallback (Simulação Realista Local)';
    default:
      return providerUsed || 'Consulta Direta';
  }
}
