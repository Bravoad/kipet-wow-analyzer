import assert from 'node:assert/strict';
import test from 'node:test';
import { analysisBinding, analysisPath, matchesAnalysis } from './analysis.ts';
import { analysisMessage } from './analysisMessages.ts';
import type { AnalysisReport, Envelope } from './types.ts';

test('analysis context is explicit and encoded without changing character identity', () => {
  assert.equal(analysisPath({ region: 'eu', realm: 'гордунни', name: 'Кипет' }, 'ru-RU', 'mythic_plus'), '/api/v1/characters/eu/%D0%B3%D0%BE%D1%80%D0%B4%D1%83%D0%BD%D0%BD%D0%B8/%D0%9A%D0%B8%D0%BF%D0%B5%D1%82/analysis?locale=ru-RU&content=mythic_plus');
});
test('a report is never displayed against another snapshot or content context', () => {
  const report = { data: { snapshot_id: 'current', context: { content: 'raid' } }, meta: { snapshot_id: 'current' } } as Envelope<AnalysisReport>;
  assert(matchesAnalysis(report, 'current', 'raid'));
  assert(!matchesAnalysis(report, 'old', 'raid'));
  assert(!matchesAnalysis(report, 'current', 'general_pve'));
  assert(!matchesAnalysis({ ...report, meta: { ...report.meta, snapshot_id: 'different' } }, 'current', 'raid'));
  assert(!matchesAnalysis({ ...report, data: null }, 'current', 'raid'));
  assert.notEqual(analysisBinding('first-character', 'same-snapshot', 'raid', 'ru-RU'), analysisBinding('second-character', 'same-snapshot', 'raid', 'ru-RU'));
  assert.notEqual(analysisBinding('first', 'snapshot', 'raid', 'ru-RU'), analysisBinding('first', 'snapshot', 'raid', 'en-US'));
});
test('unknown and incomplete data are localized without asserting a player error', () => {
  assert.match(analysisMessage('ENCHANT_DATA_INCOMPLETE', 'en-US'), /does not establish/);
  assert.match(analysisMessage('TALENT_DIFFERENT', 'ru-RU'), /сами по себе не доказывают ошибку/);
  assert.match(analysisMessage('FUTURE_RULE_REASON', 'en-US'), /FUTURE_RULE_REASON$/);
});
