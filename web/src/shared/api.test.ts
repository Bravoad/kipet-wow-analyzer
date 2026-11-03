import assert from 'node:assert/strict';
import test from 'node:test';
import { characterPath, nextPollDelay, wait } from './api.ts';

test('character paths encode each segment without changing identity', () => {
  assert.equal(characterPath({ region: 'eu', realm: 'гордунни', name: 'Кипет' }, 'ru-RU', 'talents'), '/api/v1/characters/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82/talents?locale=ru-RU');
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
