import { characterPath } from './api.ts';
import type { CharacterKey, Envelope, Locale, ProgressData, ProgressSection } from './types.ts';
import type { Dictionary } from '../i18n.ts';

export function progressPath(key: CharacterKey, locale: Locale, section: ProgressSection) {
  return characterPath(key, locale, section === 'mythic_plus' ? 'mythic-plus' : 'raids');
}

export function progressBinding(characterID: string, snapshotID: string, section: ProgressSection, locale: Locale) {
  return JSON.stringify([characterID, snapshotID, section, locale]);
}

// An unavailable section (data:null) still belongs to one specific snapshot.
export function matchesProgress(response: Envelope<ProgressData[ProgressSection] | null>, characterID: string, snapshotID: string) {
  return response.meta.snapshot_id === snapshotID && (response.data === null || response.data?.character_id === characterID);
}

export function progressNeedsPoll(status: number, response: Envelope<ProgressData[ProgressSection] | null>) {
  return status === 202 || (status === 200 && response.data === null && !!response.meta.job_id);
}

export function formatProgressDate(value: string | null, locale: Locale) {
  if (!value || Number.isNaN(Date.parse(value))) return '—';
  return new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(value));
}

export function formatRunDuration(milliseconds: number | null) {
  if (milliseconds === null || !Number.isFinite(milliseconds) || milliseconds < 0) return '—';
  const seconds = Math.floor(milliseconds / 1000);
  const minutes = Math.floor(seconds / 60);
  if (minutes >= 60) return `${Math.floor(minutes / 60)}:${String(minutes % 60).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`;
  return `${minutes}:${String(seconds % 60).padStart(2, '0')}`;
}

export function raidDifficulty(value: string, t: Dictionary) {
  const labels: Record<string, string> = { lfr: t.progressLFR, raid_finder: t.progressLFR, normal: t.progressNormal, heroic: t.progressHeroic, mythic: t.progressMythic };
  return labels[value.toLowerCase()] ?? (value === 'unknown' ? t.progressModeUnknown : value);
}

export function raidStatus(value: string, t: Dictionary) {
  const labels: Record<string, string> = { complete: t.progressComplete, completed: t.progressComplete, in_progress: t.progressInProgress, incomplete: t.progressIncomplete };
  return labels[value.toLowerCase()] ?? (value === 'unknown' ? t.progressStatusUnknown : value);
}

export function raidKillState(count: number | null, t: Dictionary) {
  return count === null ? t.progressKillsUnknown : count === 0 ? t.progressNoKillsRecorded : t.progressKillsRecorded;
}
