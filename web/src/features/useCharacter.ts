import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, characterPath, get, nextPollDelay, request, wait } from '../shared/api.ts';
import type { Character, CharacterKey, Envelope, Equipment, Job, Locale, SectionData, Statistics, Talents } from '../shared/types.ts';

export type SectionResults = { [K in keyof SectionData]?: Envelope<SectionData[K] | null> };
export function useCharacter(locale: Locale) {
  const [profile, setProfile] = useState<Envelope<Character> | null>(null);
  const [sections, setSections] = useState<SectionResults>({});
  const [sectionErrors, setSectionErrors] = useState<Partial<Record<keyof SectionData, ApiError>>>({});
  const [error, setError] = useState<ApiError | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [busy, setBusy] = useState(false);
  const [paused, setPaused] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const currentKey = useRef<CharacterKey | null>(null);

  const start = useCallback(async (key: CharacterKey, mode: 'load' | 'refresh' | 'check' = 'load', resumeJob?: string) => {
    controller.current?.abort();
    const abort = new AbortController(); controller.current = abort;
    const signal = abort.signal;
    currentKey.current = key;
    setError(null); setPaused(false); setBusy(true); setJob(null);
    if (mode === 'load') { setProfile(null); setSections({}); setSectionErrors({}); }
    const url = characterPath(key, locale);

    const loadSections = async () => {
      await Promise.all((['equipment', 'statistics', 'talents'] as const).map(async section => {
        try {
          const result = await get<Equipment | Statistics | Talents | null>(characterPath(key, locale, section), signal);
          if (!signal.aborted && result.status === 200) {
            setSections(previous => ({ ...previous, [section]: result.body }));
            setSectionErrors(previous => ({ ...previous, [section]: undefined }));
          }
        } catch (failure) {
          if (signal.aborted) return;
          if (failure instanceof ApiError) {
            if (failure.detail.code === 'CHARACTER_UNAVAILABLE' || failure.detail.code === 'PROVIDER_ACCESS_DENIED') {
              setProfile(null); setSections({}); setError(failure);
            } else setSectionErrors(previous => ({ ...previous, [section]: failure }));
          }
        }
      }));
    };
    const read = async () => {
      const response = await get<Character | null>(url, signal);
      if (response.status === 200 && response.body.data && !signal.aborted) {
        setProfile(response.body as Envelope<Character>);
        await loadSections();
      }
      return response;
    };
    try {
      let jobID = resumeJob;
      let suggested = 3;
      if (mode === 'refresh') {
        const refresh = await request<Envelope<{ disposition: string } | null>>('/api/v1/refresh/characters', { method: 'POST', signal, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...key, locale }) });
        jobID = refresh.body.meta.job_id ?? undefined;
        suggested = refresh.retryAfter ?? 3;
        if (refresh.status === 200) { await read(); return; }
      } else if (!jobID) {
        const response = await read();
        jobID = response.body.meta.job_id ?? undefined;
        suggested = response.retryAfter ?? 3;
      }
      if (!jobID) return;
      let recoveryUsed = false;
      const began = Date.now();
      for (let attempt = 0; !signal.aborted; attempt++) {
        const delay = nextPollDelay(attempt, suggested);
        if (Date.now() - began + delay > 120000) { setPaused(true); setJob(previous => previous ?? { id: jobID!, kind: 'refresh_character', status: 'queued', created_at: '', updated_at: '', next_poll_after_seconds: null, error_code: null }); break; }
        await wait(delay, signal);
        try {
          const response = await get<Job>(`/api/v1/jobs/${encodeURIComponent(jobID)}`, signal);
          const nextJob = response.body.data; setJob(nextJob);
          suggested = Math.max(nextJob.next_poll_after_seconds ?? 0, response.retryAfter ?? 0);
          if (nextJob.status === 'succeeded') { await read(); break; }
          if (nextJob.status === 'failed' || nextJob.status === 'cancelled') {
            const code = nextJob.error_code ?? (nextJob.status === 'cancelled' ? 'CANCELLED' : 'PROVIDER_UNAVAILABLE');
            if (code === 'CHARACTER_UNAVAILABLE' || code === 'PROVIDER_ACCESS_DENIED') { setProfile(null); setSections({}); }
            throw new ApiError(503, { code, message: '', retryable: true, retry_after_seconds: null, job_id: jobID, fields: [] });
          }
        } catch (failure) {
          if (failure instanceof ApiError && failure.detail.code === 'JOB_NOT_FOUND' && !recoveryUsed) {
            recoveryUsed = true;
            const response = await read();
            const nextID = response.body.meta.job_id;
            if (nextID && nextID !== jobID) { jobID = nextID; suggested = response.retryAfter ?? 3; continue; }
            if (response.status === 200) break;
          }
          throw failure;
        }
      }
    } catch (failure) {
      if (signal.aborted) return;
      const apiError = failure instanceof ApiError ? failure : new ApiError(0, { code: 'NETWORK_ERROR', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
      if (apiError.detail.code === 'CHARACTER_UNAVAILABLE' || apiError.detail.code === 'PROVIDER_ACCESS_DENIED') { setProfile(null); setSections({}); }
      setError(apiError);
    } finally { if (!signal.aborted) setBusy(false); }
  }, [locale]);

  useEffect(() => {
    if (currentKey.current) void start(currentKey.current);
    return () => controller.current?.abort();
  }, [start]);
  const invalidate = useCallback((failure: ApiError) => {
    controller.current?.abort(); setProfile(null); setSections({}); setSectionErrors({});
    setJob(null); setBusy(false); setPaused(false); setError(failure);
  }, []);
  return { profile, sections, sectionErrors, error, job, busy, paused, key: currentKey.current,
    load: (key: CharacterKey) => start(key),
    refresh: () => currentKey.current ? start(currentKey.current, 'refresh') : Promise.resolve(),
    check: () => currentKey.current ? start(currentKey.current, 'check', job?.id) : Promise.resolve(),
    dismissError: () => setError(null),
    invalidate,
  };
}
