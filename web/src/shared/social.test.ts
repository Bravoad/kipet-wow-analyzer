import assert from 'node:assert/strict';
import test from 'node:test';
import { comparisonFromSearch, comparisonKeys, exportable, guildMatches, sameSpecialization, validGuildSlug } from './social.ts';
import type { GuildMemberSummary, GuildProfile } from './social.ts';
import type { CharacterKey, Envelope, Meta } from './types.ts';

const key: CharacterKey = { region: 'eu', realm: 'гордунни', name: 'Кипет' };
test('comparison bounds and normalized duplicate identities are checked before submission', () => {
  assert.equal(comparisonKeys([]).error, 'count');
  assert.equal(comparisonKeys(Array(6).fill(key)).error, 'count');
  assert.equal(comparisonKeys([key, { ...key, name: ' кипет ' }]).error, 'duplicate');
  assert.equal(comparisonKeys([{ ...key, name: 'Élan' }, { ...key, name: 'E\u0301lan' }]).error, 'duplicate');
  for (const name of ['a/b', '%2f', 'A', 'Name1']) assert.equal(comparisonKeys([{ ...key, name }]).error, 'identity');
  assert.equal(comparisonKeys([key, { ...key, name: 'Ледник' }]).error, null);
});
test('malformed or oversized route input cannot create comparison requests', () => {
  for (const value of ['broken', '{}', '[null]', '[{"region":"eu"}]', 'a'.repeat(3001)]) assert.deepEqual(comparisonFromSearch(`?characters=${encodeURIComponent(value)}`), []);
  assert.deepEqual(comparisonFromSearch(`?characters=${encodeURIComponent(JSON.stringify([key]))}`), [key]);
});
test('guild slug accepts canonical Unicode without guessing display-name transliteration', () => {
  for (const value of ['kipet-lab', 'Гильдия', 'Straße', 'e\u0301lan']) assert(validGuildSlug(value), value);
  for (const value of ['x', '-guild', 'guild-', 'two words', 'guild/roster', '\u0301guild']) assert(!validGuildSlug(value), value);
});
test('guild data must match both identity and snapshot before displaying or exporting', () => {
  const profile = { data: { id: 'guild-1' }, meta: { snapshot_id: 'version-1' } } as Envelope<GuildProfile>;
  const response = { data: { guild_id: 'guild-1' }, meta: { snapshot_id: 'version-1' } } as Envelope<{guild_id:string}>;
  assert(guildMatches(response, profile));
  assert(!guildMatches({ ...response, data: { guild_id: 'guild-2' } }, profile));
  assert(!guildMatches({ ...response, meta: { ...response.meta, snapshot_id: 'version-2' } }, profile));
  assert(!guildMatches(response, null));
});
test('same-spec selection needs confirmed snapshots and matching class as well as spec', () => {
  const member = { snapshot_id: 'v1', specialization: { id: '251' }, member: { character_class: { id: '6' } } } as GuildMemberSummary;
  assert(sameSpecialization([member, member]));
  assert(!sameSpecialization([]));
  assert(!sameSpecialization([member, { ...member, snapshot_id: null }]));
  assert(!sameSpecialization([member, { ...member, member: { ...member.member, character_class: { id: '8', name: null } } }]));
});
test('export needs currently publishable metadata and a strict future deadline', () => {
  const now = Date.parse('2026-11-01T09:00:00Z');
  const meta = { status: 'fresh', valid_until: new Date(now + 1).toISOString() } as Meta;
  assert(exportable(meta, now));
  assert(exportable({ ...meta, status: 'stale' }, now));
  for (const valid_until of [null, 'invalid', new Date(now).toISOString()]) assert(!exportable({ ...meta, valid_until }, now));
  for (const status of ['pending', 'unavailable']) assert(!exportable({ ...meta, status } as Meta, now));
});
