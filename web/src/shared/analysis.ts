import { characterPath } from './api.ts';
import type { AnalysisContent, AnalysisReport, CharacterKey, Envelope, Locale } from './types.ts';

export function analysisPath(key: CharacterKey, locale: Locale, content: AnalysisContent) {
  return `${characterPath(key, locale, 'analysis')}&content=${encodeURIComponent(content)}`;
}

// Both IDs must match: a correctly shaped report can still belong to an older snapshot.
export function matchesAnalysis(response: Envelope<AnalysisReport | null>, snapshotID: string, content: AnalysisContent) {
  return !!response.data && response.meta.snapshot_id === snapshotID && response.data.snapshot_id === snapshotID && response.data.context.content === content;
}

export function analysisBinding(characterID: string, snapshotID: string, content: AnalysisContent, locale: Locale) {
  return JSON.stringify([characterID, snapshotID, content, locale]);
}
