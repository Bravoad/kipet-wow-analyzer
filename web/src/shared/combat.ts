import type { Envelope, Locale } from './types.ts';

export interface CombatActor { id: number; name: string | null; type: string; sub_type: string | null }
export interface CombatFight { id: number; name: string | null; encounter_id: string | null; difficulty: number | null; kill: boolean | null; start_ms: number; end_ms: number; participant_ids: number[] | null }
export interface CombatReport { code: string; title: string | null; start_time: string | null; end_time: string | null; visibility: string; fights: CombatFight[] | null; actors: CombatActor[] | null }
export interface CombatEvent { timestamp_ms: number; kind: string; source_id: number | null; target_id: number | null; ability_id: string | null; amount: number | null; overheal: number | null }
export interface CombatMetrics { damage_total: number | null; healing_total: number | null; dps: number | null; hps: number | null; overheal_total: number | null; overheal_percent: number | null; casts: number | null; interrupts: number | null; deaths: number | null }
export interface CombatAbility { id: string; casts: number; damage: number | null; healing: number | null }
export interface CombatFinding { code: string; outcome: string; event_indexes: number[] | null }
export type CombatantStatKey = 'strength' | 'agility' | 'intellect' | 'stamina'
  | 'critical_strike' | 'critical_strike_melee' | 'critical_strike_ranged' | 'critical_strike_spell'
  | 'haste' | 'haste_melee' | 'haste_ranged' | 'haste_spell' | 'mastery'
  | 'versatility' | 'versatility_damage_done' | 'versatility_healing_done' | 'versatility_damage_reduction';
export interface CombatantStats { observed_at_ms: number; values: Partial<Record<CombatantStatKey, number>> }
export interface CombatAnalysis { report_code: string; fight_id: number; actor_id: number; fight_start_ms: number; fight_end_ms: number; duration_ms: number; method: string; source: string; events_complete: boolean; timeline_truncated: boolean; metrics: CombatMetrics; abilities: CombatAbility[] | null; timeline: CombatEvent[] | null; findings: CombatFinding[] | null; combatant_stats?: CombatantStats | null }

export function reportCode(input: string): string | null {
  const text = input.trim();
  if (/^[A-Za-z0-9]{16}$/.test(text)) return text;
  try {
    const url = new URL(text);
    if (url.protocol !== 'https:' || url.hostname !== 'www.warcraftlogs.com' || url.port || url.username || url.password) return null;
    return /^\/reports\/([A-Za-z0-9]{16})\/?$/.exec(url.pathname)?.[1] ?? null;
  } catch { return null; }
}
export function positiveActorID(value: string | null): number | null {
  if (!value || !/^[1-9][0-9]{0,9}$/.test(value)) return null;
  const id = Number(value); return id <= 2147483647 ? id : null;
}
export function reportPath(code: string, locale: Locale) { return `/api/v1/reports/${encodeURIComponent(code)}?locale=${locale}`; }
export function combatPath(code: string, fight: number, actor: number, locale: Locale) { return `/api/v1/reports/${encodeURIComponent(code)}/fights/${fight}/analysis?actor_id=${actor}&locale=${locale}`; }
export function combatMatches(result: Envelope<CombatAnalysis | null>, report: Envelope<CombatReport>, fight: number, actor: number) {
  return !!result.data && !!report.meta.snapshot_id && result.meta.snapshot_id === report.meta.snapshot_id && result.data.report_code === report.data.code && result.data.fight_id === fight && result.data.actor_id === actor;
}
export function fightPlayers(actors: CombatActor[] | null, fight: CombatFight | null): CombatActor[] {
  return (actors ?? []).filter(actor => actor.type === 'player' && (!fight || fight.participant_ids === null || fight.participant_ids.includes(actor.id)));
}
export function combatOffset(timestamp: number, start: number): string {
  if (!Number.isFinite(timestamp) || !Number.isFinite(start) || timestamp < start) return '—';
  const milliseconds = Math.floor(timestamp - start);
  const seconds = Math.floor(milliseconds / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}.${String(milliseconds % 1000).padStart(3, '0')}`;
}
export function combatDuration(milliseconds: number): string {
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return '—';
  const seconds = Math.floor(milliseconds / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}
export function combatDate(value: string | null, locale: Locale): string {
  if (!value || Number.isNaN(Date.parse(value))) return '—';
  return new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(value));
}
