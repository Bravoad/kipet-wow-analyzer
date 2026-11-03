import { ApiError, request, wait } from './api.ts';
import type { Runtime } from './types.ts';

export function runtimeRetrySeconds(error: unknown) {
  const seconds = error instanceof ApiError ? error.detail.retry_after_seconds : null;
  return seconds && Number.isFinite(seconds) && seconds > 0 ? seconds : 0;
}

export async function loadRuntime(signal: AbortSignal): Promise<Runtime> {
  const retryDelays = [1000, 2000];
  for (let attempt = 0; ; attempt++) {
    signal.throwIfAborted();
    const timeout = AbortSignal.timeout(5000);
    try {
      const { body } = await request<Runtime | { data: Runtime }>('/api/v1/runtime', {
        signal: AbortSignal.any([signal, timeout]),
      });
      const runtime = body && typeof body === 'object' && 'data' in body ? body.data : body;
      if (!runtime || (runtime.provider_mode !== 'fixture' && runtime.provider_mode !== 'blizzard') || !Array.isArray(runtime.demo_characters)) {
        throw new ApiError(200, { code: 'INVALID_RESPONSE', message: 'Invalid runtime response', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
      }
      return runtime;
    } catch (error) {
      signal.throwIfAborted();
      const retryable = timeout.aborted || error instanceof ApiError &&
        (error.status === 0 || error.status === 408 || error.status === 429 || error.status >= 500 || (error.status === 200 && error.detail.code === 'INVALID_RESPONSE'));
      const delay = Math.max(retryDelays[attempt] ?? 0, runtimeRetrySeconds(error) * 1000);
      if (!retryable || attempt >= retryDelays.length || delay > 5000) throw error;
      await wait(delay, signal);
    }
  }
}
