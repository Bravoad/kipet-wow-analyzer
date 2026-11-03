import assert from 'node:assert/strict';
import test from 'node:test';
import { characterPath, matchesCharacterSnapshot, nextPollDelay, wait } from './api.ts';
import { loadRuntime, runtimeRetrySeconds } from './runtime.ts';

test('character paths encode each segment without changing identity', () => {
  assert.equal(characterPath({ region: 'eu', realm: 'гордунни', name: 'Кипет' }, 'ru-RU', 'talents'), '/api/v1/characters/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82/talents?locale=ru-RU');
});
test('character sections require a matching character and snapshot, even when unavailable', () => {
  const section = { data: { character_id: 'character' }, meta: { snapshot_id: 'snapshot' } };
  assert(matchesCharacterSnapshot(section, 'character', 'snapshot'));
  assert(!matchesCharacterSnapshot(section, 'another-character', 'snapshot'));
  assert(!matchesCharacterSnapshot(section, 'character', 'older-snapshot'));
  assert(matchesCharacterSnapshot({ ...section, data: null }, 'character', 'snapshot'));
  assert(!matchesCharacterSnapshot({ ...section, data: null }, 'character', 'older-snapshot'));
  assert(!matchesCharacterSnapshot({ data: null, meta: { snapshot_id: null } }, 'character', null));
});
test('polling has bounded local backoff but respects longer authoritative delays', () => {
  assert.equal(nextPollDelay(0, null), 3000);
  assert.equal(nextPollDelay(100, null), 15000);
  assert.equal(nextPollDelay(0, 30), 30000);
  assert.equal(nextPollDelay(0, null, 120), 120000);
});
test('cancelling a search cancels its outstanding poll wait', async () => {
  const controller = new AbortController();
  const pending = wait(60000, controller.signal);
  controller.abort();
  await assert.rejects(pending, { name: 'AbortError' });
});

test('runtime settings recover automatically after a temporary gateway error', async t => {
  const runtime = { provider_mode: 'fixture', demo_characters: [], raiderio_enabled: true };
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: runtime }));
  fetch.mock.mockImplementationOnce(async () => new Response('Unavailable', { status: 503 }));
  assert.deepEqual(await loadRuntime(new AbortController().signal), runtime);
  assert.equal(fetch.mock.callCount(), 2);
});

test('a long Retry-After stops automatic runtime retries and is retained for manual retry', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json(
    { error: { code: 'RATE_LIMITED', message: '', retry_after_seconds: 2 } },
    { status: 429, headers: { 'Retry-After': '30' } },
  ));
  await assert.rejects(loadRuntime(new AbortController().signal), error => runtimeRetrySeconds(error) === 30);
  assert.equal(fetch.mock.callCount(), 1);
});
