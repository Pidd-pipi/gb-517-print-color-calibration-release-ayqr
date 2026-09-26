
import { useEffect, useState } from 'react';
import { request } from '../api/client';
import type { DomainRecord } from '../types/domain';

export interface EligibleProofsState {
  proofs: DomainRecord[];
  loading: boolean;
  error: string;
}

// useEligibleProofs loads accepted proofs pinned to a run's current
// configuration version. These are the only proofs a reviewer may carry into a
// release decision; stale proofs of older revisions are never returned.
export function useEligibleProofs(runId: number | null): EligibleProofsState {
  const [state, setState] = useState<EligibleProofsState>({ proofs: [], loading: false, error: '' });

  useEffect(() => {
    if (!runId) {
      setState({ proofs: [], loading: false, error: '' });
      return;
    }
    let cancelled = false;
    setState((previous) => ({ ...previous, loading: true, error: '' }));
    request<DomainRecord[]>(`/proofs?page=1&pageSize=100&runId=${runId}&eligibleOnly=true`)
      .then((result) => {
        if (!cancelled) setState({ proofs: result.data, loading: false, error: '' });
      })
      .catch((error) => {
        if (!cancelled) setState({ proofs: [], loading: false, error: error instanceof Error ? error.message : String(error) });
      });
    return () => { cancelled = true; };
  }, [runId]);

  return state;
}
