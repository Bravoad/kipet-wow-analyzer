import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import type { RaiderProfile, RaiderRun } from './raider.ts';
import { raiderDungeonComparison } from './raiderDungeonComparison.ts';
import type { Envelope } from './types.ts';

const season = 'season-mn-2';
const validLink = 'https://raider.io/mythic-plus-runs/season-mn-2/';

function run(dungeon: string, slug: string, overrides: Partial<RaiderRun> = {}): RaiderRun {
  return {
    dungeon, level: 12, duration_ms: 1200, par_time_ms: 1000, timed: false, score: 0,
    completed_at: null, url: validLink + slug, ...overrides,
  };
}

function profile(name: string, runs: RaiderRun[] | null, observedSeason: string | null = season): Envelope<RaiderProfile> {
  return {
    data: {
      name, region: 'eu', realm: { name: 'Gordunni', slug: 'gordunni' },
      profile_url: `https://raider.io/characters/eu/gordunni/${encodeURIComponent(name)}`,
      race: null, class: 'Mage', specialization: 'Frost', role: 'DPS', faction: null,
      last_crawled_at: null, gear: null, talents: null, raids: null,
      mythic_plus: { season: observedSeason, rating: 0, scores: null, best_runs: runs },
      coverage: { mythic_plus: { state: 'complete', reason: null, source_updated_at: null } },
    },
    meta: {
      source: 'raiderio', status: 'fresh', snapshot_id: name, job_id: null,
      fetched_at: '2026-11-03T09:00:00Z', valid_until: '2026-11-04T09:00:00Z',
      patch: null, season: null, locale: 'ru-RU', coverage: {},
    },
    request_id: 'request',
  };
}

test('Raider.IO dungeon comparison aligns observed runs and preserves empty versus unavailable lists', () => {
  const firstRun = run(' Écho ', '111-12-echo');
  const secondRun = run('E\u0301cho', '222-12-echo', { timed: null, duration_ms: null });
  const otherRun = run('Vault', '333-12-vault');
  const comparison = raiderDungeonComparison([
    profile('Alpha', [firstRun, firstRun, secondRun]),
    profile('Beta', [otherRun]),
    profile('Gamma', []),
    profile('Delta', null),
  ]);
  assert.ok(comparison);
  assert.equal(comparison.season, season);
  assert.deepEqual(comparison.columns.map(column => column.name), ['Alpha', 'Beta', 'Gamma', 'Delta']);
  assert.deepEqual(comparison.columns.map(column => column.coverage), ['available', 'available', 'empty', 'unavailable']);
  assert.equal(new Set(comparison.columns.map(column => column.id)).size, 4);
  assert.deepEqual(comparison.rows, [
    { dungeon: 'Vault', cells: [[], [otherRun], [], []] },
    { dungeon: 'Écho', cells: [[firstRun, secondRun], [], [], []] },
  ]);
});

test('Raider.IO dungeon comparison omits unverified or different-season runs and marks their coverage partial', () => {
  const confirmed = run('Confirmed', '111-12-confirmed');
  const unverified = [
    null,
    'https://raider.io/mythic-plus-runs/season-mn-1/111-12-confirmed',
    'https://raider.io.example/mythic-plus-runs/season-mn-2/111-12-confirmed',
    validLink + '111-12-confirmed#fragment',
    validLink + '111-12-confirmed?query=1',
    validLink + 'extra/../111-12-confirmed',
  ].map(url => run('Unverified', '111-12-unverified', { url }));
  const first = profile('Alpha', [confirmed, ...unverified]);
  const second = profile('Beta', [confirmed]);
  second.data.coverage.mythic_plus = { state: 'partial', reason: null, source_updated_at: null };
  const comparison = raiderDungeonComparison([first, second]);
  assert.ok(comparison);
  assert.deepEqual(comparison.columns.map(column => column.coverage), ['partial', 'partial']);
  assert.deepEqual(comparison.rows, [{ dungeon: 'Confirmed', cells: [[confirmed], [confirmed]] }]);
});

test('Raider.IO dungeon comparison requires a common known season and distinct published profiles', () => {
  const first = profile('Alpha', []);
  const unavailable = profile('Beta', []);
  unavailable.meta.status = 'pending';
  for (const candidates of [
    [first],
    [first, null],
    [first, unavailable],
    [first, profile('Beta', [], null)],
    [first, profile('Beta', [], 'season-mn-1')],
    [first, profile('alpha', [])],
  ]) {
    assert.equal(raiderDungeonComparison(candidates), null);
  }
});
