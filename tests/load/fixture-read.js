import http from 'k6/http';
import { check, fail, sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';

// Deliberately local and small: this measures the deployed limiter, not a
// synthetic bypass using different IP headers or unbounded identities.
const base = (__ENV.BASE_URL || 'http://127.0.0.1:5173').replace(/\/$/, '');
if (!/^http:\/\/(127\.0\.0\.1|localhost|\[::1\]|host\.docker\.internal)(:\d{1,5})?$/.test(base)) throw new Error('Only a local HTTP fixture origin is allowed');
const vus = Number(__ENV.VUS || 8);
const seconds = Number(__ENV.DURATION_SECONDS || 20);
if (!Number.isInteger(vus) || vus < 1 || vus > 8 || !Number.isInteger(seconds) || seconds < 1 || seconds > 20) throw new Error('VUS must be 1..8 and DURATION_SECONDS 1..20');
const requireRateLimit = vus === 8 && seconds === 20;

const served = new Counter('fixture_reads_200');
const pending = new Counter('fixture_reads_202');
const limited = new Counter('fixture_reads_429');
const unexpected = new Counter('unexpected_responses');
const duration200 = new Trend('fixture_read_200_duration_ms', true);
const duration429 = new Trend('fixture_read_429_duration_ms', true);
const paths = [
  '/api/v1/characters/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82',
  '/api/v1/characters/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82/equipment',
  '/api/v1/characters/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82/mythic-plus',
  '/api/v1/characters/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82/analysis?content=general_pve',
  '/api/v1/guilds/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/kipet-lab/analysis',
  '/api/v1/meta/realms?region=eu&limit=30',
];

export const options = {
  scenarios: { local_reads: { executor: 'constant-vus', vus, duration: `${seconds}s`, gracefulStop: '3s' } },
  setupTimeout: '8s',
  thresholds: {
    unexpected_responses: ['count==0'],
    fixture_reads_200: ['count>0'],
    checks: ['rate==1'],
    ...(requireRateLimit ? { fixture_reads_429: ['count>0'] } : {}),
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'max'],
};

export function setup() {
  for (let attempt = 0; attempt < 3; attempt++) {
    const runtime = http.get(`${base}/api/v1/runtime`, { timeout: '2s', redirects: 0 });
    if (runtime.status === 429 && attempt < 2) {
      const retry = Number(runtime.headers['Retry-After']);
      if (!(retry > 0 && retry <= 2)) fail('Runtime preflight Retry-After exceeds the bounded local setup window');
      sleep(retry);
      continue;
    }
    if (runtime.status !== 200 || runtime.json('provider_mode') !== 'fixture') fail('Load test requires an explicitly configured fixture deployment');
    return { providerMode: 'fixture' };
  }
  fail('Fixture runtime preflight did not finish');
}

export default function () {
  // This immutable pool cannot fan out into unknown characters or guilds.
  const response = http.get(base + paths[(__ITER + __VU - 1) % paths.length], { timeout: '3s', redirects: 0 });
  let body;
  try { body = response.json(); } catch { body = null; }
  const is200 = response.status === 200;
  const is202 = response.status === 202;
  const is429 = response.status === 429;
  const valid = check(response, {
    'documented response status': () => is200 || is202 || is429,
    'private response is not cached': r => r.headers['Cache-Control'] === 'no-store',
    'request ID is present': r => !!r.headers['X-Request-Id'],
    'response has expected envelope': () => is429 ? body?.error?.code === 'USER_RATE_LIMITED' : !!body?.meta,
    'rate limit has positive Retry-After': r => !is429 || Number(r.headers['Retry-After']) > 0,
  });
  unexpected.add(valid ? 0 : 1);
  served.add(is200 ? 1 : 0); pending.add(is202 ? 1 : 0); limited.add(is429 ? 1 : 0);
  if (is200) duration200.add(response.timings.duration);
  if (is429) duration429.add(response.timings.duration);
  sleep(0.25);
}

export function handleSummary(data) {
  const values = name => data.metrics[name]?.values || null;
  const counts = { served_200: values('fixture_reads_200')?.count || 0, pending_202: values('fixture_reads_202')?.count || 0, limited_429: values('fixture_reads_429')?.count || 0, unexpected: values('unexpected_responses')?.count || 0 };
  const result = {
    test: 'local-fixture-read-limiter', version: 1, generated_at: new Date().toISOString(), base, vus, duration_seconds: seconds,
    required_provider_mode: 'fixture',
    passed: counts.served_200 > 0 && counts.unexpected === 0 && values('checks')?.rate === 1 && (!requireRateLimit || counts.limited_429 > 0),
    note: 'Local bounded smoke only; latency of accepted reads excludes expected 429. No capacity, SLO or real-provider claim.',
    counts,
    served_200_duration_ms: values('fixture_read_200_duration_ms'),
    limited_429_duration_ms: values('fixture_read_429_duration_ms'),
    http_req_failed_rate_including_429: values('http_req_failed')?.rate ?? null,
    checks: values('checks'),
  };
  return { stdout: `${JSON.stringify(result, null, 2)}\n`, [__ENV.SUMMARY_PATH || 'artifacts/load-fixture-summary.json']: `${JSON.stringify(result, null, 2)}\n` };
}
