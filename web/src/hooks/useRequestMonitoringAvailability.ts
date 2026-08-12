import { useEffect, useMemo, useState } from 'react';
import {
  isUsageServiceId,
  normalizeUsageServiceBase,
  usageServiceApi,
} from '@/services/api/usageService';
import { useAuthStore, useUsageServiceStore } from '@/stores';
import { detectApiBaseFromLocation } from '@/utils/connection';

export type RequestMonitoringUnavailableReason =
  | 'checking'
  | 'service_not_configured'
  | 'service_unavailable'
  | 'monitoring_disabled';

export interface RequestMonitoringAvailability {
  checking: boolean;
  available: boolean;
  serviceBase: string;
  reason: RequestMonitoringUnavailableReason | '';
}

export function useRequestMonitoringAvailability(): RequestMonitoringAvailability {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const usageServiceEnabled = useUsageServiceStore((state) => state.enabled);
  const usageServiceBase = useUsageServiceStore((state) => state.serviceBase);
  const usageServiceRevision = useUsageServiceStore((state) => state.revision);
  const [state, setState] = useState<RequestMonitoringAvailability>({
    checking: true,
    available: false,
    serviceBase: '',
    reason: 'checking',
  });

  const candidates = useMemo(() => {
    return Array.from(
      new Set(
        [
          usageServiceEnabled && usageServiceBase ? usageServiceBase : '',
          apiBase,
          detectApiBaseFromLocation(),
        ]
          .map((value) => normalizeUsageServiceBase(value || ''))
          .filter(Boolean)
      )
    );
  }, [apiBase, usageServiceBase, usageServiceEnabled]);

  useEffect(() => {
    let cancelled = false;

    const detect = async () => {
      const candidateBase = candidates[0] || detectApiBaseFromLocation() || '';

      setState((current) => ({ ...current, checking: true, reason: 'checking' }));

      for (const candidate of candidates) {
        try {
          const info = await usageServiceApi.getInfo(candidate);
          if (info && isUsageServiceId(info.service)) {
            if (cancelled) return;
            setState({
              checking: false,
              available: true,
              serviceBase: candidate,
              reason: '',
            });
            return;
          }
        } catch {
          // Fallback to built-in CPA2API monitoring
        }
      }

      if (cancelled) return;
      setState({
        checking: false,
        available: true,
        serviceBase: candidateBase,
        reason: '',
      });
    };

    void detect();

    return () => {
      cancelled = true;
    };
  }, [candidates, managementKey, usageServiceBase, usageServiceEnabled, usageServiceRevision]);

  return state;
}
