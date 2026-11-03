import type { Character, CharacterKey, Envelope, Equipment, Locale, Meta, MythicPlus, NamedID, Raids, Realm, Statistics, Talents } from './types.ts';

export interface GuildProfile { id: string; provider_id: string; region: 'eu' | 'us'; realm: Realm; slug: string; name: string; faction: string; member_count: number | null; created_at: string | null }
export interface GuildMember { key: CharacterKey; provider_id: string; character_class: NamedID | null; level: number | null; rank: number | null }
export interface GuildRoster { guild_id: string; members: GuildMember[] | null; reported_count: number | null }
export interface Distribution { id: string; name: string | null; count: number }
export interface GuildMemberSummary { member: GuildMember; status: string; snapshot_id: string | null; specialization: NamedID | null; role: string | null; item_level: number | null; mythic_rating: number | null; mythic_season: string | null; notices: number | null }
export interface GuildAnalysis {
  guild_id: string;
  coverage: { roster_known: boolean; reported: number | null; roster: number; profiles: number; fresh: number; stale: number; unavailable: number; pending: number; sections: Record<string, number> };
  classes: Distribution[]; specializations: Distribution[]; roles: Distribution[];
  item_level: { count: number; mean: number | null; median: number | null };
  gear: { known_enchantments: number; enchanted: number; empty_enchantments: number; unknown_enchantments: number; known_sets: number };
  progress: { mythic_profiles: number; raid_profiles: number };
  members: GuildMemberSummary[]; limitations: string[];
}
export interface CharacterBundle { profile: Character | null; equipment: Equipment | null; statistics: Statistics | null; talents: Talents | null; mythic_plus: MythicPlus | null; raids: Raids | null }
export interface ComparisonEntry { key: CharacterKey; status: 'pending' | 'fresh' | 'stale' | 'unavailable'; reason: string | null; meta: Meta; data: CharacterBundle | null }
export interface CharacterComparison { generated_at: string; entries: ComparisonEntry[]; compatibility: { same_patch: boolean | null; same_specialization: boolean | null; mythic_plus_same_season: boolean | null; combat_comparable: false; reasons: string[] } }

export function characterKeyID(key: CharacterKey) { return `${key.region}\u0000${key.realm.normalize('NFC').toLowerCase()}\u0000${key.name.normalize('NFC').toLowerCase()}`; }
export function validGuildSlug(value: string) { const text = value.trim().normalize('NFC'); return Array.from(text).length >= 2 && Array.from(text).length <= 100 && /^[\p{L}\p{N}][\p{L}\p{M}\p{N}]*(?:-[\p{L}\p{M}\p{N}]+)*$/u.test(text); }
export function comparisonKeys(input: CharacterKey[]): { keys: CharacterKey[]; error: 'count' | 'identity' | 'duplicate' | null } {
  if (input.length < 1 || input.length > 5) return { keys: [], error: 'count' };
  const keys = input.map(key => ({ region: key.region, realm: key.realm.trim().normalize('NFC'), name: key.name.trim().normalize('NFC') }));
  if (keys.some(key => !['eu', 'us'].includes(key.region) || !key.realm || Array.from(key.realm).length > 80 || /[\s/\\?#%]/u.test(key.realm) || !/^\p{L}[\p{L}\p{M}]+$/u.test(key.name) || Array.from(key.name).length > 24)) return { keys: [], error: 'identity' };
  if (new Set(keys.map(characterKeyID)).size !== keys.length) return { keys: [], error: 'duplicate' };
  return { keys, error: null };
}
export function comparisonFromSearch(search: string): CharacterKey[] {
  const value = new URLSearchParams(search).get('characters');
  if (!value || value.length > 3000) return [];
  try { const parsed: unknown = JSON.parse(value); if (!Array.isArray(parsed) || parsed.some(key => !key || typeof key !== 'object' || !['region', 'realm', 'name'].every(field => typeof key[field] === 'string'))) return []; return comparisonKeys(parsed as CharacterKey[]).keys; } catch { return []; }
}
export function guildPath(key: CharacterKey, locale: Locale, resource = '') { return `/api/v1/guilds/${encodeURIComponent(key.region)}/${encodeURIComponent(key.realm)}/${encodeURIComponent(key.name)}${resource ? `/${resource}` : ''}?locale=${locale}`; }
export function guildMatches(response: Envelope<{ guild_id: string }> | null, profile: Envelope<GuildProfile> | null) { return !!response && !!profile && !!profile.meta.snapshot_id && response.meta.snapshot_id === profile.meta.snapshot_id && response.data.guild_id === profile.data.id; }
export function characterHref(key: CharacterKey) { return `?${new URLSearchParams({ region: key.region, realm: key.realm, name: key.name })}`; }
export function sameSpecialization(members: GuildMemberSummary[]) { const first = members[0]; return members.length > 0 && !!first.specialization && !!first.member.character_class && members.every(member => !!member.snapshot_id && member.specialization?.id === first.specialization?.id && member.member.character_class?.id === first.member.character_class?.id); }
export function exportable(meta: Meta, now = Date.now()) { return ['fresh', 'stale'].includes(meta.status) && !!meta.valid_until && Date.parse(meta.valid_until) > now; }
export function downloadJSON(data: unknown, filename: string) { const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json;charset=utf-8' })); const link = document.createElement('a'); link.href = url; link.download = filename; document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 0); }
