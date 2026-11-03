import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

function localOrigin(value) {
  const url = new URL(value);
  assert.equal(url.protocol, 'http:');
  assert(['127.0.0.1', 'localhost', '[::1]', 'host.docker.internal'].includes(url.hostname), 'Only local fixture origins are allowed');
  assert(!url.username && !url.password && url.pathname === '/' && !url.search && !url.hash);
  return url.origin;
}
const gateway = localOrigin(process.env.API_BASE_URL || 'http://127.0.0.1:5173');
const direct = localOrigin(process.env.API_DIRECT_BASE_URL || 'http://127.0.0.1:8080');
const observations = [];
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
async function fetchLocal(origin, path, options = {}) {
  const response = await fetch(origin + path, { ...options, redirect: 'manual', signal: AbortSignal.timeout(5000) });
  return response;
}
async function api(path, options = {}) {
  // Cooperates with the real limiter; no IP spoofing and at most two retries.
  for (let attempt = 0; ; attempt++) {
    const response = await fetchLocal(gateway, path, options);
    if (response.status !== 429 || attempt === 2) return response;
    const seconds = Number(response.headers.get('retry-after'));
    assert(seconds > 0 && seconds <= 30, 'Unexpected Retry-After in local smoke');
    await response.arrayBuffer();
    await wait(seconds * 1000);
  }
}
async function run(name, fn) {
  try { const detail = await fn(); observations.push({ name, passed: true, ...detail }); }
  catch (error) { observations.push({ name, passed: false, error: error.message }); }
}
const runtime = await api('/api/v1/runtime');
assert.equal(runtime.status, 200);
assert.equal((await runtime.json()).provider_mode, 'fixture', 'Security smoke must use the explicit fixture deployment');

await run('gateway security headers', async () => {
  const response = await fetchLocal(gateway, '/');
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('x-content-type-options'), 'nosniff');
  assert.equal(response.headers.get('referrer-policy'), 'same-origin');
  const policy = response.headers.get('content-security-policy') || '';
  for (const directive of ["default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"]) assert(policy.includes(directive), directive);
  assert(!response.headers.get('server')?.match(/nginx\//));
  return { status: response.status };
});
for (const [name, origin] of [['gateway', gateway], ['direct-api', direct]]) {
  for (const path of ['/health/live', '/health/ready', '/metrics', '/metrics/']) await run(`${name} internal path blocked: ${path}`, async () => {
    const response = await fetchLocal(origin, path);
    assert.equal(response.status, 404);
    return { status: response.status };
  });
}
await run('API headers and server-generated request ID', async () => {
  const response = await api('/api/v1/meta/realms?region=eu', { headers: { 'X-Request-ID': 'untrusted-client-value' } });
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.equal(response.headers.get('x-content-type-options'), 'nosniff');
  assert.match(response.headers.get('content-type') || '', /^application\/json/);
  assert.match(response.headers.get('x-request-id') || '', /^[0-9a-f-]{36}$/);
  assert.notEqual(response.headers.get('x-request-id'), 'untrusted-client-value');
  return { status: response.status };
});
const cases = [
  { name: 'unsupported region', path: '/api/v1/characters/kr/stormrage/Arcanis', status: 400, code: 'INVALID_ARGUMENT' },
  { name: 'caller GraphQL rejected', path: '/api/v1/comparisons/characters', method: 'POST', body: { characters: [], query: 'query { reportData { reports { data { code } } } }' }, status: 400, code: 'INVALID_ARGUMENT' },
  { name: 'force refresh rejected', path: '/api/v1/refresh/guilds', method: 'POST', body: { region: 'eu', realm: 'гордунни', name: 'kipet-lab', force: true }, status: 400, code: 'INVALID_ARGUMENT' },
  { name: '5 KiB JSON reaches strict API decoder', path: '/api/v1/comparisons/characters', method: 'POST', body: { characters: [], unsupported: 'x'.repeat(5000) }, status: 400, code: 'INVALID_ARGUMENT' },
  { name: 'oversized JSON has safe error envelope', path: '/api/v1/comparisons/characters', method: 'POST', body: { characters: [{ name: 'x'.repeat(9000) }] }, status: 413, code: 'REQUEST_TOO_LARGE' },
  { name: 'gateway oversized JSON has safe error envelope', path: '/api/v1/comparisons/characters', method: 'POST', body: { characters: [{ name: 'x'.repeat(17000) }] }, status: 413, code: 'REQUEST_TOO_LARGE' },
];
for (const testCase of cases) await run(testCase.name, async () => {
  const response = await api(testCase.path, { method: testCase.method || 'GET', headers: testCase.body ? { 'Content-Type': 'application/json' } : {}, body: testCase.body ? JSON.stringify(testCase.body) : undefined });
  assert.equal(response.status, testCase.status);
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.match(response.headers.get('content-type') || '', /^application\/json/);
  const body = await response.json();
  assert.equal(body.error.code, testCase.code);
  assert(!JSON.stringify(body).match(/postgres:\/\/|client_secret|goroutine|SELECT /i));
  await wait(600);
  return { status: response.status };
});

const result = { test: 'local-fixture-http-security-smoke', version: 1, generated_at: new Date().toISOString(), gateway, direct_api: direct, passed: observations.every(test => test.passed), checks: observations, note: 'A bounded regression smoke, not a penetration test or production security audit.' };
const outputPath = fileURLToPath(new URL('../../artifacts/security-http-summary.json', import.meta.url));
await mkdir(fileURLToPath(new URL('../../artifacts/', import.meta.url)), { recursive: true });
await writeFile(outputPath, JSON.stringify(result, null, 2) + '\n');
console.log(JSON.stringify(result, null, 2));
if (!result.passed) process.exitCode = 1;
