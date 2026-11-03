import { useCallback, useEffect, useState } from 'react';
import { ApiError, get, nextPollDelay, request, wait } from '../shared/api.ts';
import type { CharacterComparison } from '../shared/social.ts';
import type { CharacterKey, Envelope, Job, Locale } from '../shared/types.ts';

export function requestComparison(keys: CharacterKey[], locale: Locale, signal?: AbortSignal) { return request<Envelope<CharacterComparison>>('/api/v1/comparisons/characters', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ characters: keys, locale }), signal }); }
interface State { binding: string; result: Envelope<CharacterComparison> | null; busy: boolean; paused: boolean; error: ApiError | null }
export function useComparison(keys: CharacterKey[] | null, locale: Locale) {
  const binding = keys ? JSON.stringify([keys, locale]) : '';
  const [state, setState] = useState<State | null>(null);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!keys || !binding) return;
    const controller = new AbortController(); const signal = controller.signal;
    const initial: State = { binding, result: null, busy: true, paused: false, error: null };
    setState(initial);
    const run = async () => {
      try {
        const began = Date.now(); let attempt = 0; const terminal = new Set<string>(); let recovered = false;
        let response = await requestComparison(keys, locale, signal);
        for (;;) {
          if (signal.aborted) return;
          const ids = [...new Set(response.body.data.entries.map(entry => entry.meta.job_id).filter((id): id is string => !!id && !terminal.has(id)))];
          setState({ ...initial, result: response.body, busy: ids.length > 0 });
          if (!ids.length) return;
          const delay = nextPollDelay(attempt++, response.retryAfter);
          if (Date.now() - began + delay > 120000) { setState({ ...initial, result: response.body, busy: false, paused: true }); return; }
          await wait(delay, signal);
          let recoveredThisRound = false;
          for (const id of ids) {
            try {
              const job = await get<Job>(`/api/v1/jobs/${encodeURIComponent(id)}`, signal);
              if (['succeeded', 'failed', 'cancelled'].includes(job.body.data.status)) terminal.add(id);
              else response.retryAfter = Math.max(response.retryAfter ?? 0, job.retryAfter ?? 0, job.body.data.next_poll_after_seconds ?? 0);
            } catch (failure) {
              if (!(failure instanceof ApiError) || failure.detail.code !== 'JOB_NOT_FOUND' || recovered) throw failure;
              recovered = true; recoveredThisRound = true; terminal.add(id);
            }
          }
          if (signal.aborted) return;
          if (ids.every(id => terminal.has(id)) || recoveredThisRound) response = await requestComparison(keys, locale, signal);
        }
      } catch (failure) {
        if (signal.aborted) return;
        const error = failure instanceof ApiError ? failure : new ApiError(0, { code: 'NETWORK_ERROR', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
        setState({ ...initial, busy: false, error });
      }
    };
    void run(); return () => controller.abort();
    // binding contains the complete normalized request, preserving cancellation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [binding, revision]);
  const reload = useCallback(() => setRevision(value => value + 1), []);
  const current = state?.binding === binding ? state : null;
  return { result: current?.result ?? null, busy: !!binding && (!current || current.busy), paused: current?.paused ?? false, error: current?.error ?? null, reload };
}
