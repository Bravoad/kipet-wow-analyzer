import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { isRaiderSocialRoute, raiderComparisonFromURL, raiderComparisonHref, raiderHref, raiderKeyFromURL, raiderPath, raiderSourceLink } from './raider.ts';
import type { RaiderGuildMember } from './raider.ts';
import { summarizeRaiderRoster } from './raiderRoster.ts';
import type { CharacterKey, Runtime } from './types.ts';

test('Raider.IO routes accept realm titles and keep legacy routes separate', () => {
  const search = new URLSearchParams({ view: 'raiderio', region: 'eu', realm: 'Гордунни', name: 'Кипет' });
  const key = raiderKeyFromURL(search.toString());
  assert.deepEqual(key, { region: 'eu', realm: 'Гордунни', name: 'Кипет' });
  assert.equal(raiderPath({ region: 'us', realm: 'Area 52', name: 'Example' }, 'en-US'), '/api/v1/raiderio/characters/us/Area%2052/Example?locale=en-US');
  search.set('view', 'character');
  assert.equal(raiderKeyFromURL(search.toString()), null);
  search.set('view', 'raiderio');
  search.set('realm', 'realm/name');
  assert.equal(raiderKeyFromURL(search.toString()), null);
});

test('Raider.IO source links require the expected HTTPS host and resource kind', () => {
  assert.equal(raiderSourceLink('https://raider.io/characters/eu/gordunni/Kipet', 'characters'), 'https://raider.io/characters/eu/gordunni/Kipet');
  assert.equal(raiderSourceLink('https://raider.io/mythic-plus-runs/season/123', 'characters'), undefined);
  assert.equal(raiderSourceLink('https://raider.io.example/characters/eu/gordunni/Kipet', 'characters'), undefined);
  assert.equal(raiderSourceLink('http://raider.io/characters/eu/gordunni/Kipet', 'characters'), undefined);
  assert.equal(raiderSourceLink('https://test@raider.io/characters/eu/gordunni/Kipet', 'characters'), undefined);
});

test('Raider.IO social routes preserve saved demo links and honor explicit source selection', () => {
  const guild: CharacterKey = { region: 'eu', realm: 'гордунни', name: 'kipet-lab' };
  const demo: CharacterKey[] = [{ ...guild, name: 'Кипет' }, { ...guild, name: 'Ледник' }];
  const runtime: Runtime = { provider_mode: 'fixture', demo_characters: [demo[0]], demo_guilds: [guild], demo_comparisons: [demo], raiderio_enabled: true };
  const legacyGuild = `?${new URLSearchParams({ view: 'guilds', region: guild.region, realm: guild.realm, guild: guild.name })}`;
  const legacyComparison = `?${new URLSearchParams({ view: 'comparison', characters: JSON.stringify(demo) })}`;
  assert.equal(isRaiderSocialRoute(legacyGuild, runtime), false);
  assert.equal(isRaiderSocialRoute(legacyComparison, runtime), false);
  assert.equal(isRaiderSocialRoute('?view=guilds', runtime), true);
  assert.equal(isRaiderSocialRoute(`${legacyGuild}&source=raiderio`, runtime), true);
  assert.equal(isRaiderSocialRoute(`${legacyComparison}&source=raiderio`, runtime), true);
  assert.equal(isRaiderSocialRoute('?view=comparison&source=fixture', runtime), false);

  const liveGuild: CharacterKey = { region: 'eu', realm: 'gordunni', name: 'Авантюрное агентство' };
  assert.deepEqual(raiderKeyFromURL(raiderHref(liveGuild, 'guilds'), 'guilds'), liveGuild);
  assert.equal(raiderPath(liveGuild, 'ru-RU', 'guilds'), `/api/v1/raiderio/guilds/eu/gordunni/${encodeURIComponent(liveGuild.name)}?locale=ru-RU`);
  assert.deepEqual(raiderComparisonFromURL(raiderComparisonHref(demo)), demo);
  assert.deepEqual(raiderComparisonFromURL(raiderComparisonHref([demo[0], { ...demo[0], name: 'кипет' }])), []);
});

test('Raider.IO roster summary keeps an unavailable roster distinct from a confirmed empty roster', () => {
  assert.equal(summarizeRaiderRoster(null), null);
  assert.deepEqual(summarizeRaiderRoster([]), {
    members: 0,
    realms: 0,
    classes: [],
    specializations: [],
    roles: [],
    unknown: { class: 0, specialization: 0, role: 0 },
  });
});

test('Raider.IO roster summary counts observed fields and groups specializations within their class', () => {
  const roster: RaiderGuildMember[] = [
    { key: { region: 'eu', realm: 'E\u0301lan', name: 'Arc' }, rank: 0, class: 'Mage', specialization: 'Frost', role: 'DPS' },
    { key: { region: 'eu', realm: 'Élan', name: 'Rune' }, rank: 1, class: 'Death Knight', specialization: 'Frost', role: 'DPS' },
    { key: { region: 'eu', realm: 'élan', name: 'Snow' }, rank: 2, class: 'Mage', specialization: 'Frost', role: null },
    { key: { region: 'us', realm: 'élan', name: 'Sun' }, rank: null, class: null, specialization: 'Frost', role: 'HEALING' },
    { key: { region: 'eu', realm: 'gordunni', name: 'Fox' }, rank: null, class: 'Hunter', specialization: null, role: null },
  ];

  assert.deepEqual(summarizeRaiderRoster(roster), {
    members: 5,
    realms: 3,
    classes: [{ name: 'Mage', count: 2 }, { name: 'Death Knight', count: 1 }, { name: 'Hunter', count: 1 }],
    specializations: [{ className: 'Mage', name: 'Frost', count: 2 }, { className: 'Death Knight', name: 'Frost', count: 1 }],
    roles: [{ name: 'DPS', count: 2 }, { name: 'HEALING', count: 1 }],
    unknown: { class: 1, specialization: 2, role: 2 },
  });
});
