import { useEffect, useState } from 'react';
import { ApiError, get, nextPollDelay, wait } from '../shared/api.ts';
import { matchesProgress, progressBinding, progressNeedsPoll, progressPath } from '../shared/progress.ts';
import type { Character, CharacterKey, Envelope, Job, Locale, ProgressData, ProgressSection } from '../shared/types.ts';

interface ProgressState {
  binding: string; result: Envelope<ProgressData[ProgressSection] | null> | null;
  loading: boolean; error: ApiError | null; outdated: boolean; paused: boolean;
}

export function useProgress(key: CharacterKey | null, profile: Envelope<Character> | null, locale: Locale, section: ProgressSection | null, onUnavailable?: (error: ApiError) => void) {
  const [state, setState] = useState<ProgressState | null>(null);
  const [retry, setRetry] = useState(0);
  const characterID = profile?.data.id; const snapshotID = profile?.meta.snapshot_id;
  const binding = characterID && snapshotID && section ? progressBinding(characterID, snapshotID, section, locale) : '';
  const region = key?.region; const realm = key?.realm; const name = key?.name;

  useEffect(() => {
    if (!binding || !characterID || !snapshotID || !region || !realm || !name || !section) return;
    const abort = new AbortController(); const signal = abort.signal;
    const initial: ProgressState = { binding, result: null, loading: true, error: null, outdated: false, paused: false };
    setState(initial);
    const url = progressPath({ region, realm, name }, locale, section);
    const run = async () => {
      try {
        let result = await get<ProgressData[ProgressSection] | null>(url, signal);
        const began = Date.now(); let attempt = 0; let recovered = false;
        while (progressNeedsPoll(result.status, result.body) && !signal.aborted) {
          const jobID = result.body.meta.job_id;
          if (!jobID) throw new ApiError(502, { code: 'INVALID_RESPONSE', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
          const delay = nextPollDelay(attempt++, result.retryAfter);
          if (Date.now() - began + delay > 120000) { setState({ ...initial, loading: false, paused: true }); return; }
          await wait(delay, signal);
          try {
            const job = await get<Job>(`/api/v1/jobs/${encodeURIComponent(jobID)}`, signal);
            if (job.body.data.status === 'failed' || job.body.data.status === 'cancelled') throw new ApiError(503, { code: job.body.data.error_code ?? 'PROVIDER_UNAVAILABLE', message: '', retryable: true, retry_after_seconds: null, job_id: jobID, fields: [] });
            if (job.body.data.status !== 'succeeded') { result.retryAfter = Math.max(result.retryAfter ?? 0, job.body.data.next_poll_after_seconds ?? 0, job.retryAfter ?? 0); continue; }
          } catch (failure) {
            if (!(failure instanceof ApiError) || failure.detail.code !== 'JOB_NOT_FOUND' || recovered) throw failure;
            recovered = true;
          }
          result = await get<ProgressData[ProgressSection] | null>(url, signal);
        }
        if (signal.aborted) return;
        if (result.status !== 200 || !matchesProgress(result.body, characterID, snapshotID)) { setState({ ...initial, loading: false, outdated: true }); return; }
        setState({ ...initial, loading: false, result: result.body });
      } catch (failure) {
        if (signal.aborted) return;
        const error = failure instanceof ApiError ? failure : new ApiError(0, { code: 'NETWORK_ERROR', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
        if (['CHARACTER_UNAVAILABLE', 'PROVIDER_ACCESS_DENIED', 'REALM_NOT_FOUND'].includes(error.detail.code)) onUnavailable?.(error);
        setState({ ...initial, loading: false, error });
      }
    };
    void run();
    return () => abort.abort();
  }, [binding, characterID, snapshotID, region, realm, name, locale, section, retry, onUnavailable]);

  const current = state?.binding === binding ? state : null;
  return {
    result: section ? current?.result ?? null : null,
    loading: !!section && !!binding && (!current || current.loading), error: current?.error ?? null,
    outdated: current?.outdated ?? false, paused: current?.paused ?? false,
    reload: () => { setState(null); setRetry(value => value + 1); },
  };
}
