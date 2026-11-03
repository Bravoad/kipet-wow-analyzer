import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, get, nextPollDelay, request, wait } from '../shared/api.ts';
import { raiderPath } from '../shared/raider.ts';
import type { RaiderGuildProfile, RaiderProfile, RaiderResource } from '../shared/raider.ts';
import type { CharacterKey, Envelope, Job, Locale } from '../shared/types.ts';

interface State<T> {
  path: string;
  profile: Envelope<T> | null;
  busy: boolean;
  paused: boolean;
  error: ApiError | null;
}

function resourceError(code: string) {
  return new ApiError(502, { code, message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
}

function useRaiderResource<T>(key: CharacterKey | null, locale: Locale, resource: RaiderResource) {
  const [state, setState] = useState<State<T> | null>(null);
  const controller = useRef<AbortController | null>(null);
  const path = key ? raiderPath(key, locale, resource) : null;
  const refreshBody = key ? JSON.stringify({ ...key, locale }) : null;

  const load = useCallback(async (refresh = false) => {
    if (!path || !refreshBody) return;
    controller.current?.abort();
    const abort = new AbortController();
    controller.current = abort;
    const signal = abort.signal;
    const initial: State<T> = { path, profile: null, busy: true, paused: false, error: null };
    setState(initial);

    const read = () => get<T | null>(path, signal);
    try {
      let response = refresh ? await request<Envelope<T | null>>(`/api/v1/refresh/raiderio/${resource}`, {
        method: 'POST', signal, headers: { 'Content-Type': 'application/json' }, body: refreshBody,
      }) : await read();
      let profile: Envelope<T> | null = null;
      const deadline = Date.now() + 120000;
      let attempt = 0;
      let recoveredJob: string | null = null;

      while (!signal.aborted) {
        if (response.status === 200 && response.body.data) {
          if (response.body.meta.source !== 'raiderio') throw resourceError('INVALID_RESPONSE');
          profile = response.body as Envelope<T>;
        }
        const jobID = response.body.meta.job_id;
        if (!jobID) {
          if (!profile) throw resourceError('INVALID_RESPONSE');
          setState({ ...initial, profile, busy: false });
          return;
        }

        setState({ ...initial, profile });
        let suggested = response.retryAfter;
        while (!signal.aborted) {
          const delay = nextPollDelay(attempt++, suggested);
          if (Date.now() + delay > deadline) {
            setState({ ...initial, profile, busy: false, paused: true });
            return;
          }
          await wait(delay, signal);
          try {
            const job = await get<Job>(`/api/v1/jobs/${encodeURIComponent(jobID)}`, signal);
            if (job.body.data.status === 'succeeded') break;
            if (job.body.data.status === 'failed' || job.body.data.status === 'cancelled') {
              throw resourceError(job.body.data.error_code ?? (job.body.data.status === 'cancelled' ? 'CANCELLED' : 'PROVIDER_UNAVAILABLE'));
            }
            suggested = Math.max(job.body.data.next_poll_after_seconds ?? 0, job.retryAfter ?? 0);
          } catch (failure) {
            // A purge can remove the job. Re-read the guarded endpoint once.
            if (!(failure instanceof ApiError) || failure.detail.code !== 'JOB_NOT_FOUND' || recoveredJob !== null) throw failure;
            recoveredJob = jobID;
            break;
          }
        }
        if (signal.aborted) return;
        response = await read();
        if (response.body.meta.job_id === jobID) throw resourceError('INVALID_RESPONSE');
        profile = null;
      }
    } catch (failure) {
      if (signal.aborted) return;
      const error = failure instanceof ApiError ? failure : new ApiError(0, { code: 'NETWORK_ERROR', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
      setState({ ...initial, busy: false, error });
    }
  }, [path, refreshBody, resource]);

  useEffect(() => {
    if (path) void load();
    return () => controller.current?.abort();
  }, [path, load]);

  const current = path && state?.path === path ? state : null;
  return {
    profile: current?.profile ?? null,
    busy: !!path && (!current || current.busy),
    paused: current?.paused ?? false,
    error: current?.error ?? null,
    reload: () => void load(),
    refresh: () => void load(true),
  };
}

export function useRaiderProfile(key: CharacterKey | null, locale: Locale) {
  return useRaiderResource<RaiderProfile>(key, locale, 'characters');
}

export function useRaiderGuild(key: CharacterKey | null, locale: Locale) {
  return useRaiderResource<RaiderGuildProfile>(key, locale, 'guilds');
}
