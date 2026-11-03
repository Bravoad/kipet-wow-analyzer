import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import type { RaiderGuildProfile } from './raider.ts';
import { makeRaiderGuildExport } from './raiderGuildExport.ts';
import type { Envelope, Meta } from './types.ts';

const now = Date.parse('2026-11-03T09:01:00Z');
const guild: Envelope<RaiderGuildProfile> = {
  data: {
    name: 'Example Guild',
    region: 'eu',
    realm: { name: 'Gordunni', slug: 'gordunni' },
    profile_url: 'https://raider.io/guilds/eu/gordunni/Example%20Guild',
    faction: null,
    last_crawled_at: null,
    members: [{
      key: { region: 'eu', realm: 'gordunni', name: 'RosterMember' },
      rank: null, class: 'Hunter', specialization: 'Beast Mastery', role: 'DPS',
    }],
    raids: null,
    rankings: {},
    coverage: { members: { state: 'partial', reason: null, source_updated_at: null } },
  },
  meta: {
    source: 'raiderio', status: 'fresh', snapshot_id: 'snapshot', job_id: null,
    fetched_at: '2026-11-03T09:00:00Z', valid_until: '2026-11-04T09:00:00Z',
    patch: null, season: null, locale: 'ru-RU', coverage: {},
  },
  request_id: 'request',
};

test('Raider.IO guild export preserves source, coverage and aggregates without roster identities', () => {
  const exported = makeRaiderGuildExport(guild, 'snapshot', now);
  assert.ok(exported);
  assert.equal(exported.schema_version, 1);
  assert.equal(exported.source, 'raiderio');
  assert.equal(exported.guild.profile_url, guild.data.profile_url);
  assert.equal(exported.snapshot.id, guild.meta.snapshot_id);
  assert.equal(exported.snapshot.valid_until, guild.meta.valid_until);
  assert.equal(exported.coverage.members?.state, 'partial');
  assert.equal(exported.roster?.members, 1);
  assert.deepEqual(exported.roster?.classes, [{ name: 'Hunter', count: 1 }]);
  assert.equal(exported.raids, null);
  assert.deepEqual(exported.rankings, {});
  assert.doesNotMatch(JSON.stringify(exported), /RosterMember/);

  const missing = { ...guild, data: { ...guild.data, members: null } };
  assert.equal(makeRaiderGuildExport(missing, 'snapshot', now)?.roster, null);
});

test('Raider.IO guild export accepts only the current usable snapshot', () => {
  const invalid: Partial<Meta>[] = [
    { status: 'stale' },
    { status: 'pending' },
    { job_id: 'refresh' },
    { valid_until: new Date(now).toISOString() },
    { fetched_at: 'invalid' },
    { source: 'fixture' },
    { snapshot_id: 'replaced' },
  ];
  for (const override of invalid) {
    assert.equal(makeRaiderGuildExport({ ...guild, meta: { ...guild.meta, ...override } }, 'snapshot', now), null);
  }
  assert.equal(makeRaiderGuildExport(null, 'snapshot', now), null);
  assert.equal(makeRaiderGuildExport(guild, null, now), null);
  const unsafeLink = { ...guild, data: { ...guild.data, profile_url: 'https://raider.io.example/guilds/eu/realm/example' } };
  assert.equal(makeRaiderGuildExport(unsafeLink, 'snapshot', now), null);
});
