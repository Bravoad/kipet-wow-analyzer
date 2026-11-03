export type Locale = 'ru-RU' | 'en-US';
export type Region = 'eu' | 'us';
export type Tab = 'overview' | 'equipment' | 'statistics' | 'talents' | 'analysis' | 'mythic_plus' | 'raids';
export type Section = 'profile' | 'equipment' | 'statistics' | 'talents';
export type ProgressSection = 'mythic_plus' | 'raids';
export type SourceSection = Section | ProgressSection;
export interface CharacterKey { region: Region; realm: string; name: string }
export interface Runtime { provider_mode: 'fixture' | 'blizzard'; demo_characters: CharacterKey[]; combat_mode?: 'fixture' | 'warcraftlogs' | 'disabled'; combat_setup_required?: boolean; raiderio_enabled?: boolean; demo_reports?: string[]; demo_guilds?: CharacterKey[]; demo_comparisons?: CharacterKey[][] }
export interface Coverage {
  state: 'complete' | 'partial' | 'unavailable';
  fetched_at: string | null; fresh_until: string | null; valid_until: string | null; reason: string | null;
}
export interface Meta {
  source: 'blizzard' | 'fixture' | 'system' | 'warcraftlogs' | 'raiderio'; fetched_at: string | null; status: 'fresh' | 'stale' | 'pending' | 'unavailable';
  snapshot_id: string | null; valid_until: string | null; patch: string | null; season: string | null;
  locale: Locale; coverage: Partial<Record<SourceSection | 'realms' | 'report' | 'combat' | 'guild' | 'roster', Coverage>>; job_id: string | null;
}
export interface Envelope<T> { data: T; meta: Meta; request_id: string }
export interface NamedID { id: string; name: string | null }
export interface Realm { id: string; region: Region; slug: string; name: string; connected_realm_id: string | null }
export interface Character {
  id: string; provider_id: string; region: Region; realm: Realm; name: string; name_normalized: string;
  level: number; race: NamedID; character_class: NamedID; active_specialization: NamedID | null;
  faction: string; average_item_level: number | null; equipped_item_level: number | null; source_last_login_at: string | null;
}
export interface EquipmentItem {
  slot: string; item_id: string; name: string | null; item_level: number | null; quality: string;
  inventory_type: string | null; bonus_ids: string[];
  enchantments: { id: string; display: string | null }[] | null;
  sockets: { type: string; item_id: string | null }[] | null; set_id: string | null;
}
export interface Equipment { character_id: string; items: EquipmentItem[] }
export interface Stat { rating: number | null; percent: number | null }
export interface Statistics {
  character_id: string; health: number | null;
  primary: Record<'strength' | 'agility' | 'intellect' | 'stamina', number | null>;
  secondary: {
    critical_strike: Record<'melee' | 'ranged' | 'spell', Stat>;
    haste: Record<'melee' | 'ranged' | 'spell', Stat>;
    mastery: Stat;
    versatility: { rating: number | null; damage_done_percent: number | null; healing_done_percent: number | null; damage_taken_reduction_percent: number | null };
  };
}
export interface TalentLoadout {
  provider_id: string | null; specialization: NamedID; is_active: boolean | null;
  export_code: string | null; tree_hash: string | null;
  nodes: { node_id: string; entry_id: string | null; spell_id: string | null; rank: number; tree: string }[] | null;
}
export interface Talents { character_id: string; loadouts: TalentLoadout[] }
export interface Job {
  id: string; kind: string; status: 'queued' | 'running' | 'retry_wait' | 'succeeded' | 'failed' | 'cancelled';
  created_at: string; updated_at: string; next_poll_after_seconds: number | null; error_code: string | null;
}
export interface ApiFailure {
  code: string; message: string; retryable: boolean; retry_after_seconds: number | null;
  job_id: string | null; fields: { field: string; reason: string }[];
}
export type SectionData = { equipment: Equipment; statistics: Statistics; talents: Talents };

export type AnalysisContent = 'general_pve' | 'raid' | 'mythic_plus';
export type AnalysisOutcome = 'OK' | 'Info' | 'Warning' | 'Critical' | 'Unknown';
export interface AnalysisEvidence { section: string; path: string; value: unknown; fetched_at: string | null; reference: string | null }
export interface AnalysisResult {
  rule_id: string; rule_version: string; applicability: 'applicable' | 'not_applicable' | 'unknown';
  outcome: AnalysisOutcome | null; confidence: 'high' | 'medium' | 'unknown'; confidence_reason: string; reason_code: string;
  message_key: string; params: Record<string, unknown>; evidence: AnalysisEvidence[];
}
export interface AnalysisDiagnostic { code: string; message_key: string; params: Record<string, unknown>; section: string | null; evidence: AnalysisEvidence[] }
export interface AnalysisReference { id: string; title: string; url: string }
export interface AnalysisReport {
  snapshot_id: string;
  source: string; evaluated_at: string;
  context: { patch: string | null; season: string | null; class_id: string | null; specialization_id: string | null; content: AnalysisContent };
  ruleset: { version: string; digest: string; synthetic: boolean; reviewed_at: string; reviewed_by: string; references: AnalysisReference[] } | null;
  summary: { ok: number; info: number; warning: number; critical: number; unknown: number; not_applicable: number };
  results: AnalysisResult[]; diagnostics: AnalysisDiagnostic[];
}

export interface ProgressSeason { id: string; name: string | null; is_current: boolean | null }
export interface MythicRun {
  dungeon: NamedID; keystone_level: number; duration_ms: number | null;
  completed_at: string | null; within_time: boolean | null; rating: number | null;
}
export interface MythicPlus {
  character_id: string; season: ProgressSeason | null; rating: number | null;
  run_scope: 'season_best' | 'current_period_best' | 'unknown';
  best_runs: MythicRun[] | null; available_seasons: ProgressSeason[] | null;
}
export interface RaidEncounter {
  id: string; name: string | null; completed_count: number | null; last_kill_at: string | null;
}
export interface RaidMode {
  difficulty: string; status: string; completed: number | null; total: number | null;
  encounters: RaidEncounter[] | null;
}
export interface RaidInstance { id: string; name: string | null; modes: RaidMode[] | null }
export interface RaidExpansion { id: string; name: string | null; instances: RaidInstance[] | null }
export interface Raids { character_id: string; expansions: RaidExpansion[] | null }
export type ProgressData = { mythic_plus: MythicPlus; raids: Raids };
