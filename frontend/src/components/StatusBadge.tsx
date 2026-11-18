import {
  AlertTriangle,
  Building2,
  CheckCircle2,
  PackageCheck,
  Truck,
  Navigation,
} from 'lucide-react';
import type { StandardStatus } from '../types/tracking';
import { getStatusStyleMeta } from '../utils/format';

export function StatusIcon({
  status,
  className = 'h-4 w-4 shrink-0',
}: {
  status: StandardStatus;
  className?: string;
}) {
  switch (status) {
    case 'POSTED':
      return <PackageCheck className={className} aria-hidden="true" />;
    case 'IN_TRANSIT':
      return <Truck className={className} aria-hidden="true" />;
    case 'AT_CDD':
      return <Building2 className={className} aria-hidden="true" />;
    case 'OUT_FOR_DELIVERY':
      return <Navigation className={className} aria-hidden="true" />;
    case 'DELIVERED':
      return <CheckCircle2 className={className} aria-hidden="true" />;
    case 'EXCEPTION':
      return <AlertTriangle className={className} aria-hidden="true" />;
  }
}

interface StatusBadgeProps {
  status: StandardStatus;
  label?: string;
  size?: 'sm' | 'md';
}

export function StatusBadge({ status, label, size = 'md' }: StatusBadgeProps) {
  const meta = getStatusStyleMeta(status);
  const padding =
    size === 'sm' ? 'px-2.5 py-1 text-xs gap-1.5' : 'px-3 py-1.5 text-sm gap-2';

  return (
    <span
      className={`rs-badge inline-flex items-center rounded-full border font-semibold ${padding} ${meta.badgeClasses}`}
    >
      <StatusIcon
        status={status}
        className={size === 'sm' ? 'h-3.5 w-3.5 shrink-0' : 'h-4 w-4 shrink-0'}
      />
      <span>{label || meta.shortLabel}</span>
    </span>
  );
}
