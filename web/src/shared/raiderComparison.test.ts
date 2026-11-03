import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import type { RaiderProfile } from './raider.ts';
import { makeRaiderComparisonExport, raiderComparisonContext } from './raiderComparison.ts';
import type { Envelope, Meta } from './types.ts';

const now = Date.parse('2026-11-03T09:01:00Z');

function character(name: string, snapshotID: string, overrides: Partial<RaiderProfile> = {}): Envelope<RaiderProfile> {
  return {
    data: {
      name, region: 'eu', realm: { name: 'Gordunni', slug: 'gordunni' },
      profile_url: `https://raider.io/characters/eu/gordunni/${encodeURIComponent(name)}`,
      race: null, class: 'Mage', specialization: 'Frost', role: 'DPS', faction: null, last_crawled_at: null,
      gear: { equipped_item_level: 331.25, total_item_level: null, updated_at: null, items: [] },
      talents: { export_code: 'TalentLoadout', specialization_id: null },
      mythic_plus: { season: 'season-mn-2', rating: 0, scores: {}, best_runs: [] },
      raids: [],
      coverage: { mythic_plus: { state: 'partial', reason: null, source_updated_at: null } },
      ...overrides,
    },
    meta: {
      source: 'raiderio', status: 'fresh', snapshot_id: snapshotID, job_id: null,
      fetched_at: '2026-11-03T09:00:00Z', valid_until: '2026-11-04T09:00:00Z',
      patch: null, season: null, locale: 'ru-RU', coverage: {},
    },
    request_id: 'request',
  };
}

const first = character('Первый', 'first');
const second = character('Второй', 'second');
const unknown = { sameSpecialization: null, sameMythicSeason: null, duplicateProfiles: false };

test('Raider.IO comparison reports matching and different observed context separately', () => {
  assert.deepEqual(raiderComparisonContext([first, second]), {
    sameSpecialization: true, sameMythicSeason: true, duplicateProfiles: false,
  });
  const otherClass = character('Другой', 'other', {
    class: 'Death Knight',
    mythic_plus: { season: 'season-mn-1', rating: 500, scores: null, best_runs: null },
  });
  assert.deepEqual(raiderComparisonContext([first, otherClass]), {
    sameSpecialization: false, sameMythicSeason: false, duplicateProfiles: false,
  });
  const missingSeason = character('Другой', 'other', { specialization: ' ', mythic_plus: null });
  assert.deepEqual(raiderComparisonContext([first, missingSeason]), unknown);
  assert.deepEqual(raiderComparisonContext([first, null]), unknown);
  assert.deepEqual(raiderComparisonContext([first]), unknown);
});

test('Raider.IO comparison detects duplicate profiles after realm resolution', () => {
  const alias = character('первый', 'alias', { realm: { name: 'Гордунни', slug: 'gordunni' } });
  assert.deepEqual(raiderComparisonContext([first, alias]), { ...unknown, duplicateProfiles: true });
  assert.equal(makeRaiderComparisonExport([first, alias], ['first', 'alias'], now), null);
});

test('Raider.IO comparison export preserves provenance and selected observations', () => {
  const exported = makeRaiderComparisonExport([first, second], ['first', 'second'], now);
  assert.ok(exported);
  assert.equal(exported.schema_version, 1);
  assert.equal(exported.source, 'raiderio');
  assert.deepEqual(exported.compatibility, { same_specialization: true, same_mythic_season: true });
  assert.deepEqual(exported.characters.map(entry => entry.name), ['Первый', 'Второй']);
  const [entry] = exported.characters;
  assert.equal(entry.profile_url, first.data.profile_url);
  assert.equal(entry.snapshot.id, 'first');
  assert.equal(entry.snapshot.valid_until, first.meta.valid_until);
  assert.equal(entry.equipped_item_level, 331.25);
  assert.deepEqual(entry.mythic_plus, { season: 'season-mn-2', rating: 0 });
  assert.equal(entry.coverage.mythic_plus?.state, 'partial');
  assert.deepEqual(entry.raids, []);
  assert.doesNotMatch(JSON.stringify(exported), /TalentLoadout|"gear"|"best_runs"/);
});

test('Raider.IO comparison export requires every expected current usable snapshot', () => {
  const invalid: Partial<Meta>[] = [
    { status: 'stale' }, { status: 'pending' }, { job_id: 'refresh' },
    { valid_until: new Date(now).toISOString() }, { source: 'fixture' },
    { snapshot_id: 'replaced' }, { fetched_at: 'invalid' },
  ];
  for (const override of invalid) {
    assert.equal(makeRaiderComparisonExport([first, { ...second, meta: { ...second.meta, ...override } }], ['first', 'second'], now), null);
  }
  assert.equal(makeRaiderComparisonExport([first, null], ['first', 'second'], now), null);
  assert.equal(makeRaiderComparisonExport([first, { ...second, data: null }], ['first', 'second'], now), null);
  assert.equal(makeRaiderComparisonExport([first, second], ['first'], now), null);
  const unsafeLink = { ...second, data: { ...second.data, profile_url: 'https://raider.io.example/characters/eu/gordunni/Name' } };
  assert.equal(makeRaiderComparisonExport([first, unsafeLink], ['first', 'second'], now), null);
});
