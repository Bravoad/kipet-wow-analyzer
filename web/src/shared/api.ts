import type { ApiFailure, CharacterKey, Envelope, Locale } from './types.ts';

export class ApiError extends Error {
  status: number;
  detail: ApiFailure;
  constructor(status: number, detail: ApiFailure) { super(detail.message); this.status = status; this.detail = detail; }
}

export function characterPath(key: CharacterKey, locale: Locale, section = '') {
  return `/api/v1/characters/${encodeURIComponent(key.region)}/${encodeURIComponent(key.realm)}/${encodeURIComponent(key.name)}${section ? `/${section}` : ''}?locale=${locale}`;
}

export function nextPollDelay(attempt: number, suggestedSeconds: number | null, retryAfterSeconds: number | null = null) {
  const local = Math.min(15, Math.max(3, 3 * 1.35 ** attempt));
  return Math.max(local, suggestedSeconds ?? 0, retryAfterSeconds ?? 0) * 1000;
}

export async function request<T>(url: string, options: RequestInit = {}): Promise<{ body: T; status: number; retryAfter: number | null }> {
  let response: Response;
  try { response = await fetch(url, { ...options, headers: { Accept: 'application/json', ...options.headers } }); }
  catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ApiError(0, { code: 'NETWORK_ERROR', message: 'Connection unavailable', retryable: true, retry_after_seconds: null, job_id: null, fields: [] });
  }
  const retryHeader = response.headers.get('Retry-After');
  const retryAfter = retryHeader ? (/^\d+$/.test(retryHeader) ? Number(retryHeader) : Math.max(1, Math.ceil((Date.parse(retryHeader) - Date.now()) / 1000))) : null;
  let body: T & { error?: ApiFailure };
  try { body = await response.json(); }
  catch { throw new ApiError(response.status, { code: 'INVALID_RESPONSE', message: 'Invalid response', retryable: true, retry_after_seconds: retryAfter, job_id: null, fields: [] }); }
  if (!response.ok) throw new ApiError(response.status, body.error ?? { code: 'INFRASTRUCTURE_UNAVAILABLE', message: 'Service unavailable', retryable: true, retry_after_seconds: retryAfter, job_id: null, fields: [] });
  return { body, status: response.status, retryAfter };
}

export function get<T>(url: string, signal?: AbortSignal) { return request<Envelope<T>>(url, { signal }); }

export function wait(milliseconds: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) { reject(new DOMException('Aborted', 'AbortError')); return; }
    const done = () => { signal.removeEventListener('abort', cancel); resolve(); };
    const timer = setTimeout(done, milliseconds);
    const cancel = () => { clearTimeout(timer); signal.removeEventListener('abort', cancel); reject(new DOMException('Aborted', 'AbortError')); };
    signal.addEventListener('abort', cancel, { once: true });
  });
}
