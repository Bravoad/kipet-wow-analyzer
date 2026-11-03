import { useCallback, useEffect, useState } from 'react';
import { ApiError, get, nextPollDelay, wait } from '../shared/api.ts';
import type { Envelope, Job } from '../shared/types.ts';

interface State<T> { binding: string; result: Envelope<T> | null; loading: boolean; error: ApiError | null; paused: boolean }
const privacyCodes = ['REPORT_UNAVAILABLE', 'REPORT_NOT_FOUND', 'GUILD_UNAVAILABLE', 'GUILD_NOT_FOUND', 'PROVIDER_ACCESS_DENIED'];

export function useCombatResource<T>(url: string | null, onAccessRevoked?: (error: ApiError) => void, version = '') {
  const [state, setState] = useState<State<T> | null>(null);
  const [revision, setRevision] = useState(0);
  const binding = url ? `${url}|${version}` : '';
  useEffect(() => {
    if (!url) return;
    const controller = new AbortController(); const signal = controller.signal;
    const initial: State<T> = { binding, result: null, loading: true, error: null, paused: false };
    setState(initial);
    const run = async () => {
      try {
        let response = await get<T | null>(url, signal);
        const began = Date.now(); let attempt = 0; let recoveredMissingJob = false;
        while ((response.status === 202 || response.body.data === null && response.body.meta.job_id !== null) && !signal.aborted) {
          const id = response.body.meta.job_id;
          if (!id) throw new ApiError(502, { code: 'INVALID_RESPONSE', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
          const delay = nextPollDelay(attempt++, response.retryAfter);
          if (Date.now() - began + delay > 120000) { setState({ ...initial, loading: false, paused: true }); return; }
          await wait(delay, signal);
          try {
            const job = await get<Job>(`/api/v1/jobs/${encodeURIComponent(id)}`, signal);
            if (job.body.data.status === 'failed' || job.body.data.status === 'cancelled') throw new ApiError(503, { code: job.body.data.error_code ?? 'PROVIDER_UNAVAILABLE', message: '', retryable: true, retry_after_seconds: job.retryAfter, job_id: id, fields: [] });
            if (job.body.data.status !== 'succeeded') { response.retryAfter = Math.max(response.retryAfter ?? 0, job.retryAfter ?? 0, job.body.data.next_poll_after_seconds ?? 0); continue; }
          } catch (failure) {
            if (!(failure instanceof ApiError) || failure.detail.code !== 'JOB_NOT_FOUND' || recoveredMissingJob) throw failure;
            // Purging a revoked report also removes its jobs. Re-read the
            // guarded resource once so a privacy denial clears old metadata.
            recoveredMissingJob = true;
          }
          response = await get<T | null>(url, signal);
        }
        if (signal.aborted) return;
        if (response.status !== 200 || response.body.data === null) throw new ApiError(404, { code: 'REPORT_UNAVAILABLE', message: '', retryable: false, retry_after_seconds: null, job_id: null, fields: [] });
        setState({ ...initial, loading: false, result: response.body as Envelope<T> });
      } catch (failure) {
        if (signal.aborted) return;
        const error = failure instanceof ApiError ? failure : new ApiError(0, { code: 'NETWORK_ERROR', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
        if (privacyCodes.includes(error.detail.code)) onAccessRevoked?.(error);
        setState({ ...initial, loading: false, error });
      }
    };
    void run();
    return () => controller.abort();
  }, [url, binding, revision, onAccessRevoked]);
  const invalidate = useCallback((error: ApiError) => setState(previous => previous ? { ...previous, result: null, loading: false, error, paused: false } : previous), []);
  const reload = useCallback(() => setRevision(value => value + 1), []);
  const current = state?.binding === binding ? state : null;
  return { result: current?.result ?? null, loading: !!url && (!current || current.loading), error: current?.error ?? null, paused: current?.paused ?? false, reload, invalidate };
}
