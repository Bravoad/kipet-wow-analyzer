import assert from 'node:assert/strict';
import test from 'node:test';
import { dictionary } from '../i18n.ts';
import { formatProgressDate, formatRunDuration, matchesProgress, progressBinding, progressNeedsPoll, progressPath, raidDifficulty, raidKillState, raidStatus } from './progress.ts';
import type { Envelope, MythicPlus } from './types.ts';

test('progress paths preserve identity and translate the public mythic-plus route', () => {
  const key = { region: 'eu' as const, realm: 'гордунни', name: 'Кипет' };
  assert.equal(progressPath(key, 'ru-RU', 'mythic_plus'), '/api/v1/characters/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82/mythic-plus?locale=ru-RU');
  assert.match(progressPath(key, 'en-US', 'raids'), /\/raids\?locale=en-US$/);
});

test('progress data and unavailable responses stay bound to the displayed snapshot', () => {
  const response = { data: { character_id: 'character' }, meta: { snapshot_id: 'snapshot' } } as Envelope<MythicPlus | null>;
  assert(matchesProgress(response, 'character', 'snapshot'));
  assert(!matchesProgress(response, 'another', 'snapshot'));
  assert(!matchesProgress(response, 'character', 'older'));
  assert(matchesProgress({ ...response, data: null }, 'character', 'snapshot'));
  assert(!matchesProgress({ ...response, data: null }, 'character', 'older'));
  assert.notEqual(progressBinding('character', 'snapshot', 'raids', 'ru-RU'), progressBinding('character', 'snapshot', 'mythic_plus', 'ru-RU'));
  assert.notEqual(progressBinding('character', 'snapshot', 'raids', 'ru-RU'), progressBinding('character', 'snapshot', 'raids', 'en-US'));
});

test('durations keep unknown values distinct from a recorded zero and support hour-long runs', () => {
  assert.equal(formatRunDuration(null), '—');
  assert.equal(formatRunDuration(Number.NaN), '—');
  assert.equal(formatRunDuration(-1), '—');
  assert.equal(formatRunDuration(0), '0:00');
  assert.equal(formatRunDuration(1599999), '26:39');
  assert.equal(formatRunDuration(3605000), '1:00:05');
  assert.equal(formatProgressDate(null, 'en-US'), '—');
  assert.equal(formatProgressDate('invalid', 'ru-RU'), '—');
  assert.match(formatProgressDate('2020-05-01T14:00:00Z', 'ru-RU'), /2020/);
});

test('a legacy unavailable section waits for its refresh job before offering a matching-snapshot reload', () => {
  const response = { data: null, meta: { snapshot_id: 'old', job_id: 'refresh' } } as Envelope<MythicPlus | null>;
  assert(progressNeedsPoll(200, response));
  assert(progressNeedsPoll(202, { ...response, meta: { ...response.meta, job_id: null } }));
  assert(!progressNeedsPoll(200, { ...response, meta: { ...response.meta, job_id: null } }));
  assert(!progressNeedsPoll(200, { ...response, data: { character_id: 'character' } as MythicPlus }));
  assert(!matchesProgress({ ...response, meta: { ...response.meta, snapshot_id: 'new' } }, 'character', 'old'));
});

test('raid kill observations distinguish unknown, recorded zero, and confirmed kills in both locales', () => {
  for (const locale of ['ru-RU', 'en-US'] as const) {
    const t = dictionary(locale);
    assert.equal(raidKillState(null, t), t.progressKillsUnknown);
    assert.equal(raidKillState(0, t), t.progressNoKillsRecorded);
    assert.equal(raidKillState(3, t), t.progressKillsRecorded);
    assert.equal(raidDifficulty('HEROIC', t), t.progressHeroic);
    assert.equal(raidDifficulty('unknown', t), t.progressModeUnknown);
    assert.equal(raidDifficulty('future_difficulty', t), 'future_difficulty');
    assert.equal(raidStatus('IN_PROGRESS', t), t.progressInProgress);
    assert.equal(raidStatus('unknown', t), t.progressStatusUnknown);
  }
});
