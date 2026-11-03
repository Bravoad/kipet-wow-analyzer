import assert from 'node:assert/strict';
import test from 'node:test';
import { combatDate, combatDuration, combatMatches, combatOffset, combatPath, fightPlayers, positiveActorID, reportCode, reportPath } from './combat.ts';
import type { CombatActor, CombatAnalysis, CombatFight, CombatReport } from './combat.ts';
import { combatMessages, findingText } from './combatMessages.ts';
import type { Envelope } from './types.ts';

test('public report input accepts exact codes and official HTTPS report URLs only', () => {
  const code = 'DEMOCombat000001';
  assert.equal(reportCode(` ${code} `), code);
  assert.equal(reportCode(`https://www.warcraftlogs.com/reports/${code}#fight=1`), code);
  for (const input of [`http://www.warcraftlogs.com/reports/${code}`, `https://www.warcraftlogs.com.evil.test/reports/${code}`, `https://evil.test/${code}`, `https://user:pass@www.warcraftlogs.com/reports/${code}`, `https://www.warcraftlogs.com:444/reports/${code}`, `${code}/graphql`, 'short', `${code}%2f`]) assert.equal(reportCode(input), null, input);
  assert.equal(reportPath(code, 'ru-RU'), `/api/v1/reports/${code}?locale=ru-RU`);
  assert.match(combatPath(code, 1, 2, 'en-US'), /\/fights\/1\/analysis\?actor_id=2&locale=en-US$/);
});
test('fight and participant parameters are canonical positive bounded integers', () => {
  for (const value of [null, '', '0', '-1', '01', '1.5', '1e2', '2147483648']) assert.equal(positiveActorID(value), null);
  assert.equal(positiveActorID('1'), 1); assert.equal(positiveActorID('2147483647'), 2147483647);
});
test('analysis must match displayed report version, fight and participant independently', () => {
  const report = { data:{code:'DEMOCombat000001'},meta:{snapshot_id:'version-1'} } as Envelope<CombatReport>;
  const analysis = { data:{report_code:'DEMOCombat000001',fight_id:1,actor_id:2},meta:{snapshot_id:'version-1'} } as Envelope<CombatAnalysis>;
  assert(combatMatches(analysis, report, 1, 2));
  assert(!combatMatches(analysis, report, 2, 2)); assert(!combatMatches(analysis, report, 1, 1));
  assert(!combatMatches({...analysis,meta:{...analysis.meta,snapshot_id:'version-2'}}, report, 1, 2));
  assert(!combatMatches({...analysis,data:{...analysis.data,report_code:'OTHERCombat00001'}},report,1,2));
  assert(!combatMatches({...analysis,data:null},report,1,2));
});
test('timeline offsets use report-relative fight start and preserve milliseconds', () => {
  assert.equal(combatOffset(122345,60000), '1:02.345'); assert.equal(combatOffset(60000,60000),'0:00.000');
  assert.equal(combatOffset(59999,60000),'—'); assert.equal(combatOffset(Number.NaN,0),'—');
  assert.equal(combatDuration(125000),'2:05'); assert.equal(combatDuration(0),'0:00');
  assert.equal(combatDate(null,'en-US'),'—'); assert.match(combatDate('2020-01-01T01:00:00Z','ru-RU'),/2020/);
});
test('death observations and unavailable rules have distinct explanations in both locales', () => {
  for (const locale of ['ru-RU','en-US'] as const) {
    const t = combatMessages(locale);
    assert.equal(findingText('DEATHS_OBSERVED',t),t.deathFinding);
    assert.equal(findingText('ROTATION_RULES_UNAVAILABLE',t),t.rotationFinding);
    assert.equal(findingText('INCOMPLETE_EVENTS',t),t.incompleteFinding);
  }
});
test('fight membership distinguishes explicit empty, known members, and unknown while excluding pets', () => {
  const actors = [{id:1,type:'player'}, {id:2,type:'player'}, {id:3,type:'pet'}, {id:4,type:'npc'}] as CombatActor[];
  assert.deepEqual(fightPlayers(actors,{participant_ids:[2,3]} as CombatFight).map(actor => actor.id),[2]);
  assert.deepEqual(fightPlayers(actors,{participant_ids:[]} as CombatFight),[]);
  assert.deepEqual(fightPlayers(actors,{participant_ids:null} as CombatFight).map(actor => actor.id),[1,2]);
  assert.deepEqual(fightPlayers(null,{participant_ids:[1]} as CombatFight),[]);
});
