import { characterKeyID, comparisonFromSearch } from './social.ts';
import type { CharacterKey, Locale, Region, Runtime } from './types.ts';

export type RaiderSection = 'profile' | 'gear' | 'talents' | 'mythic_plus' | 'raids';
export type RaiderResource = 'characters' | 'guilds';

export interface RaiderCoverage {
  state: 'complete' | 'partial' | 'unavailable';
  reason: string | null;
  source_updated_at: string | null;
}

export interface RaiderItem {
  slot: string;
  item_id: string;
  name: string | null;
  item_level: number | null;
  quality: number | null;
  enchant_ids: string[] | null;
  enchantments: { id: string; name: string | null }[] | null;
  gem_ids: string[] | null;
  bonus_ids: string[] | null;
}

export interface RaiderRun {
  dungeon: string;
  level: number;
  duration_ms: number | null;
  par_time_ms: number | null;
  timed: boolean | null;
  score: number | null;
  completed_at: string | null;
  url: string | null;
}

export interface RaiderRaid {
  slug: string;
  expansion_id: string | null;
  summary: string | null;
  total_bosses: number | null;
  normal_bosses_killed: number | null;
  heroic_bosses_killed: number | null;
  mythic_bosses_killed: number | null;
}

export interface RaiderProfile {
  name: string;
  region: Region;
  realm: { name: string; slug: string };
  profile_url: string;
  race: string | null;
  class: string | null;
  specialization: string | null;
  role: string | null;
  faction: string | null;
  last_crawled_at: string | null;
  gear: { equipped_item_level: number | null; total_item_level: number | null; updated_at: string | null; items: RaiderItem[] | null } | null;
  talents: { export_code: string | null; specialization_id: string | null } | null;
  mythic_plus: { season: string | null; rating: number | null; scores: Record<string, number | null> | null; best_runs: RaiderRun[] | null } | null;
  raids: RaiderRaid[] | null;
  coverage: Partial<Record<RaiderSection, RaiderCoverage>>;
  guild?: { name: string; realm: string } | null;
}

export interface RaiderGuildMember {
  key: CharacterKey;
  rank: number | null;
  class: string | null;
  specialization: string | null;
  role: string | null;
}

export interface RaiderGuildProfile {
  name: string;
  region: Region;
  realm: { name: string; slug: string };
  profile_url: string;
  faction: string | null;
  last_crawled_at: string | null;
  members: RaiderGuildMember[] | null;
  raids: RaiderRaid[] | null;
  rankings: Record<string, Record<string, { world: number | null; region: number | null; realm: number | null }>> | null;
  coverage: Partial<Record<'profile' | 'members' | 'raids' | 'rankings', RaiderCoverage>>;
}

export function raiderRealm(value: string) {
  const realm = value.trim().normalize('NFC');
  return [...realm].length <= 80 && /[\p{L}\p{Nd}]/u.test(realm) && /^[\p{L}\p{M}\p{Nd}'’ -]+$/u.test(realm) ? realm.replace(/ +/g, ' ') : null;
}

export function raiderName(value: string) {
  const name = value.trim().normalize('NFC');
  return /^\p{L}[\p{L}\p{M}]+$/u.test(name) && [...name].length <= 24 ? name : null;
}

export function raiderGuildName(value: string) {
  const name = value.trim().normalize('NFC');
  const length = [...name].length;
  return length >= 2 && length <= 100 && /[\p{L}\p{Nd}]/u.test(name) && /^[\p{L}\p{M}\p{Nd}\p{P}\p{S} ]+$/u.test(name) && !/[\\/%?#]/u.test(name) ? name : null;
}

export function raiderKeyFromURL(search: string, resource: RaiderResource = 'characters'): CharacterKey | null {
  const params = new URLSearchParams(search);
  if (params.get('view') !== (resource === 'guilds' ? 'guilds' : 'raiderio')) return null;
  const region = params.get('region');
  const realm = raiderRealm(params.get('realm') ?? '');
  const name = resource === 'guilds' ? raiderGuildName(params.get('name') ?? params.get('guild') ?? '') : raiderName(params.get('name') ?? '');
  return (region === 'eu' || region === 'us') && realm && name ? { region, realm, name } : null;
}

export function raiderPath(key: CharacterKey, locale: Locale, resource: RaiderResource = 'characters') {
  const segments = [key.region, key.realm, key.name].map(encodeURIComponent);
  return `/api/v1/raiderio/${resource}/${segments.join('/')}?locale=${locale}`;
}

export function raiderHref(key: CharacterKey, resource: RaiderResource = 'characters') {
  const view = resource === 'guilds' ? 'guilds' : 'raiderio';
  return `?${new URLSearchParams({ view, source: 'raiderio', ...key })}`;
}

export function raiderComparisonKeys(input: unknown): { keys: CharacterKey[]; error: 'count' | 'identity' | 'duplicate' | null } {
  if (!Array.isArray(input) || input.length < 1 || input.length > 5) return { keys: [], error: 'count' };
  const keys: CharacterKey[] = [];
  const seen = new Set<string>();
  for (const value of input) {
    if (!value || typeof value !== 'object' || (value.region !== 'eu' && value.region !== 'us') || typeof value.realm !== 'string' || typeof value.name !== 'string') return { keys: [], error: 'identity' };
    const realm = raiderRealm(value.realm);
    const name = raiderName(value.name);
    if (!realm || !name) return { keys: [], error: 'identity' };
    const id = `${value.region}\u0000${realm.toLowerCase()}\u0000${name.toLowerCase()}`;
    if (seen.has(id)) return { keys: [], error: 'duplicate' };
    seen.add(id);
    keys.push({ region: value.region, realm, name });
  }
  return { keys, error: null };
}

export function raiderComparisonFromURL(search: string) {
  const params = new URLSearchParams(search);
  if (params.get('view') !== 'comparison') return [];
  const value = params.get('characters');
  if (!value || value.length > 3000) return [];
  try { return raiderComparisonKeys(JSON.parse(value)).keys; } catch { return []; }
}

export function raiderComparisonHref(keys: CharacterKey[]) {
  return `?${new URLSearchParams({ view: 'comparison', source: 'raiderio', characters: JSON.stringify(keys) })}`;
}

export function isRaiderSocialRoute(search: string, runtime: Runtime | null) {
  const params = new URLSearchParams(search);
  const source = params.get('source');
  if (source === 'fixture') return false;
  if (source === 'raiderio' || runtime === null) return true;
  if (!runtime.raiderio_enabled) return false;
  if (runtime.provider_mode === 'fixture') {
    if (params.get('view') === 'guilds' && params.has('guild')) {
      const key = raiderKeyFromURL(search, 'guilds');
      if (key && runtime.demo_guilds?.some(demo => characterKeyID(demo) === characterKeyID(key))) return false;
    }
    if (params.get('view') === 'comparison') {
      const keys = comparisonFromSearch(search);
      const demos = new Set([...runtime.demo_characters, ...(runtime.demo_comparisons ?? []).flat()].map(characterKeyID));
      if (keys.length && keys.every(key => demos.has(characterKeyID(key)))) return false;
    }
  }
  return true;
}

export function raiderSourceLink(value: string | null, kind: RaiderResource | 'mythic-plus-runs') {
  if (!value) return undefined;
  try {
    const url = new URL(value);
    if (url.origin === 'https://raider.io' && !url.username && !url.password && url.pathname.startsWith(`/${kind}/`)) return url.href;
  } catch { /* Unusable source link. */ }
  return undefined;
}
