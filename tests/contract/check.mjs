import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import SwaggerParser from '@apidevtools/swagger-parser';
import Ajv from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

const base = process.env.API_BASE_URL || 'http://127.0.0.1:8080';
const document = await SwaggerParser.validate(fileURLToPath(new URL('../../api/openapi/mvp-v1.yaml', import.meta.url)));
const ajv = new Ajv({ strict: false, allErrors: true, allowUnionTypes: true });
addFormats(ajv);
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
let requests = 0;
async function call(path, contractPath, method = 'get', body, limitedAttempt = 0) {
  const response = await fetch(base + path, { method: method.toUpperCase(), headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined });
  const payload = await response.json();
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert(response.headers.get('x-request-id'));
  const definition = document.paths[contractPath][method].responses[String(response.status)];
  assert(definition, `Undocumented response ${response.status} ${path}`);
  const validate = ajv.compile(definition.content['application/json'].schema);
  assert(validate(payload), `${path}: ${JSON.stringify(validate.errors)}`);
  requests++;
  if (response.status === 429 && payload.error?.code === 'USER_RATE_LIMITED' && limitedAttempt < 3) {
    const delay = Number(response.headers.get('retry-after'));
    assert(delay > 0 && delay <= 30, 'Unexpected limiter delay in local fixture check');
    await sleep(delay * 1000);
    return call(path, contractPath, method, body, limitedAttempt + 1);
  }
  return { response, payload };
}

const runtime = await call('/api/v1/runtime', '/api/v1/runtime');
assert.equal(runtime.payload.provider_mode, 'fixture', 'This isolated synthetic check requires PROVIDER_MODE=fixture');
const demos = runtime.payload.demo_characters;
assert.equal(runtime.payload.combat_mode, 'fixture');
assert.deepEqual(runtime.payload.demo_reports, ['DEMOCombat000001']);
for (const demo of demos) {
  const catalog = await call(`/api/v1/meta/realms?region=${demo.region}&locale=en-US`, '/api/v1/meta/realms');
  assert.equal(catalog.response.status, 200);
  assert(catalog.payload.data.items.some(r => r.slug === demo.realm));
  const path = `/api/v1/characters/${demo.region}/${encodeURIComponent(demo.realm)}/${encodeURIComponent(demo.name)}`;
  let read = await call(path, '/api/v1/characters/{region}/{realm}/{name}');
  if (read.response.status === 202) {
    assert(read.response.headers.get('location'));
    const jobID = read.payload.meta.job_id;
    for (let i = 0; i < 8; i++) {
      await sleep(3000);
      const job = await call(`/api/v1/jobs/${jobID}`, '/api/v1/jobs/{id}');
      if (job.payload.data.status === 'succeeded') break;
      assert(!['failed', 'cancelled'].includes(job.payload.data.status), 'Fixture refresh failed');
    }
    read = await call(path, '/api/v1/characters/{region}/{realm}/{name}');
  }
  assert.equal(read.response.status, 200);
  assert.equal(read.payload.data.region, demo.region);
  assert.equal(read.payload.meta.source, 'fixture', 'Synthetic data must never claim Blizzard provenance');
  // Fixture revisions carry an explicitly synthetic patch; never infer a live patch from date.
  assert.equal(read.payload.meta.patch, 'synthetic-demo-v1');
  for (const section of ['equipment', 'statistics', 'talents']) {
    const result = await call(`${path}/${section}`, `/api/v1/characters/{region}/{realm}/{name}/${section}`);
    assert.equal(result.response.status, 200);
    assert.equal(result.payload.data.character_id, read.payload.data.id);
  }
  for (const section of ['mythic-plus', 'raids']) {
    await sleep(500);
    let result = await call(`${path}/${section}`, `/api/v1/characters/{region}/{realm}/{name}/${section}`);
    // A legacy snapshot is backfilled as never_fetched. Read schedules its first progress collection.
    if (result.payload.data === null && result.payload.meta.job_id) {
      for (let i = 0; i < 8; i++) {
        await sleep(3000);
        const job = await call(`/api/v1/jobs/${result.payload.meta.job_id}`, '/api/v1/jobs/{id}');
        assert(!['failed', 'cancelled'].includes(job.payload.data.status), 'Progress refresh failed');
        if (job.payload.data.status === 'succeeded') break;
      }
      result = await call(`${path}/${section}`, `/api/v1/characters/{region}/{realm}/{name}/${section}`);
    }
    assert.equal(result.response.status, 200);
    assert.equal(result.payload.data.character_id, read.payload.data.id);
    assert.equal(result.payload.meta.source, 'fixture');
    assert.equal(Object.keys(result.payload.meta.coverage).length, 6);
    if (section === 'mythic-plus') {
      assert.equal(result.payload.meta.season, result.payload.data.season.id);
      assert.equal(result.payload.data.run_scope, 'season_best');
      assert(result.payload.data.best_runs.some(run => run.within_time === true));
      assert(result.payload.data.best_runs.some(run => run.within_time === false));
    } else {
      assert.equal(result.payload.meta.season, null, 'Raid history must not inherit a Mythic+ season');
      const bosses = result.payload.data.expansions.flatMap(expansion => expansion.instances ?? []).flatMap(instance => instance.modes ?? []).flatMap(mode => mode.encounters ?? []);
      assert(bosses.some(boss => boss.completed_count === 0), 'A known zero must survive serialization');
      assert(bosses.some(boss => boss.completed_count === null), 'Unknown kills must remain null');
    }
  }
  await sleep(500);
  const analyzed = await call(`${path}/analysis?content=general_pve`, '/api/v1/characters/{region}/{realm}/{name}/analysis');
  assert.equal(analyzed.response.status, 200);
  assert.equal(analyzed.payload.data.snapshot_id, analyzed.payload.meta.snapshot_id);
  assert.equal(analyzed.payload.data.source, 'fixture');
  assert.equal(analyzed.payload.data.ruleset.synthetic, true);
  assert.equal(analyzed.payload.data.context.patch, 'synthetic-demo-v1');
  assert(analyzed.payload.data.results.some(r => r.rule_id === 'GEAR_MISSING_ENCHANT' && r.outcome === 'Warning'));
  assert(analyzed.payload.data.results.some(r => r.rule_id === 'GEAR_MISSING_ENCHANT' && r.outcome === 'Unknown'));
  assert(analyzed.payload.data.results.some(r => r.rule_id === 'GEAR_SET_BONUS' && r.outcome === 'Info'));
  assert(analyzed.payload.data.results.some(r => r.rule_id === 'TALENT_BUILD_DIFF' && r.outcome === 'Unknown'));
  const localized = await call(`${path}/analysis?content=general_pve&locale=en-US`, '/api/v1/characters/{region}/{realm}/{name}/analysis');
  assert.deepEqual(localized.payload.data, analyzed.payload.data, 'Analysis content must not change with locale or repeated reads');
  await sleep(500);
  const unavailableRules = await call(`${path}/analysis?content=raid`, '/api/v1/characters/{region}/{realm}/{name}/analysis');
  assert.equal(unavailableRules.response.status, 200);
  assert.equal(unavailableRules.payload.data.ruleset, null);
  assert(unavailableRules.payload.data.results.every(r => r.outcome === 'Unknown'));
}

async function completeResource(path, contractPath) {
  let result = await call(path, contractPath);
  for (let attempt = 0; result.response.status === 202 && attempt < 12; attempt++) {
    assert(result.response.headers.get('location'));
    await sleep(3000);
    const job = await call(`/api/v1/jobs/${result.payload.meta.job_id}`, '/api/v1/jobs/{id}');
    assert(!['failed', 'cancelled'].includes(job.payload.data.status), 'Synthetic combat job failed');
    if (job.payload.data.status === 'succeeded') result = await call(path, contractPath);
  }
  assert.equal(result.response.status, 200);
  assert.equal(result.payload.meta.source, 'fixture');
  return result;
}

const code = runtime.payload.demo_reports[0];
const reportPath = `/api/v1/reports/${code}`;
const report = await completeResource(reportPath, '/api/v1/reports/{code}');
assert.equal(report.payload.data.visibility, 'public');
assert.equal(report.payload.data.code, code);
const fights = await completeResource(reportPath + '/fights', '/api/v1/reports/{code}/fights');
assert.deepEqual(fights.payload.data.fights, report.payload.data.fights);
assert.deepEqual(fights.payload.data.actors, report.payload.data.actors);
for (const [fightID, actorID] of [[1, 1], [1, 2], [2, 1]]) {
  const path = `${reportPath}/fights/${fightID}/analysis?actor_id=${actorID}`;
  const analyzed = await completeResource(path, '/api/v1/reports/{code}/fights/{fightID}/analysis');
  const data = analyzed.payload.data;
  assert.equal(analyzed.payload.meta.snapshot_id, report.payload.meta.snapshot_id, 'Fight analysis must match the loaded report version');
  assert.equal(data.report_code, code);
  assert.equal(data.fight_id, fightID);
  assert.equal(data.actor_id, actorID);
  assert.equal(data.method, 'event-amounts/whole-fight-v1');
  assert(data.findings.some(finding => finding.outcome === 'Unknown'));
  if (fightID === 1) {
    assert.equal(data.events_complete, true);
    assert.equal(data.metrics.dps, data.metrics.damage_total / (data.duration_ms / 1000));
    assert.equal(data.metrics.hps, data.metrics.healing_total / (data.duration_ms / 1000));
    assert(actorID === 1 ? data.metrics.dps > 0 : data.metrics.hps > 0);
    const localized = await completeResource(path + '&locale=en-US', '/api/v1/reports/{code}/fights/{fightID}/analysis');
    assert.deepEqual(localized.payload.data, data, 'Calculation and cached evidence must not change with locale');
  } else {
    assert.equal(data.events_complete, false);
    assert.equal(analyzed.payload.meta.coverage.combat.state, 'partial');
    for (const value of Object.values(data.metrics)) assert.equal(value, null, 'Incomplete stream must not pretend to be full-fight zero');
  }
}
const invalidActor = await call(reportPath + '/fights/1/analysis?actor_id=999', '/api/v1/reports/{code}/fights/{fightID}/analysis');
assert.equal(invalidActor.response.status, 400);
await sleep(1000);
const badReport = await call('/api/v1/reports/not-a-report', '/api/v1/reports/{code}');
assert.equal(badReport.response.status, 400);

const demoGuild = runtime.payload.demo_guilds[0];
assert.deepEqual(demoGuild, { region: 'eu', realm: 'гордунни', name: 'kipet-lab' });
const guildContract = '/api/v1/guilds/{region}/{realm}/{name}';
const guildPath = `/api/v1/guilds/${demoGuild.region}/${encodeURIComponent(demoGuild.realm)}/${demoGuild.name}`;
const guild = await completeResource(guildPath, guildContract);
const roster = await completeResource(guildPath + '/roster', guildContract + '/roster');
assert.equal(roster.payload.meta.snapshot_id, guild.payload.meta.snapshot_id);
assert.equal(roster.payload.data.guild_id, guild.payload.data.id);
assert.equal(roster.payload.data.members.length, 3);
let guildAnalysis;
for (let attempt = 0; attempt < 12; attempt++) {
  guildAnalysis = await completeResource(guildPath + '/analysis', guildContract + '/analysis');
  if (guildAnalysis.payload.data.coverage.pending === 0) break;
  await sleep(2000);
}
const summary = guildAnalysis.payload.data;
assert.equal(summary.guild_id, guild.payload.data.id);
assert.equal(summary.coverage.profiles, 2);
assert.equal(summary.coverage.roster, 3);
assert.equal(summary.coverage.unavailable, 1);
assert.equal(summary.item_level.count, 2);
assert.equal(summary.item_level.mean, 274);
assert.equal(summary.item_level.median, 274);
assert.equal(summary.classes.find(row => row.id === '6').count, 2);
assert.deepEqual(summary.roles, [{ id: 'unknown', name: null, count: 2 }]);
assert(summary.members.filter(row => row.status === 'fresh').every(row => row.notices === 1));
assert(summary.members.find(row => row.status === 'unavailable').item_level === null);
assert(summary.limitations.includes('GUILD_LOGS_NOT_LINKED'));
assert(guildAnalysis.payload.meta.valid_until <= guild.payload.meta.valid_until);
await sleep(1000);
const comparisonInput = { characters: runtime.payload.demo_comparisons[0], locale: 'ru-RU' };
const comparisonContract = '/api/v1/comparisons/characters';
const compared = await call(comparisonContract, comparisonContract, 'post', comparisonInput);
assert.equal(compared.response.status, 200);
assert.deepEqual(compared.payload.data.entries.map(row => row.data.profile.equipped_item_level), [278, 270]);
assert.deepEqual(compared.payload.data.compatibility, { same_patch: true, same_specialization: true, mythic_plus_same_season: true, combat_comparable: false, reasons: ['COMBAT_CONTEXT_UNAVAILABLE'] });
assert(compared.payload.data.entries.every(row => row.meta.source === 'fixture' && Object.keys(row.meta.coverage).length === 6));
const mixed = await call(comparisonContract, comparisonContract, 'post', { characters: [comparisonInput.characters[0], { ...comparisonInput.characters[0], name: 'Пропавший' }] });
assert.equal(mixed.payload.data.entries[0].status, 'fresh');
assert.equal(mixed.payload.data.entries[1].status, 'unavailable');
assert.equal(mixed.payload.data.entries[1].data, null);
assert.equal(mixed.payload.data.compatibility.same_specialization, null);
await sleep(1000);
for (const characters of [[], Array(6).fill(comparisonInput.characters[0]), [comparisonInput.characters[0], { ...comparisonInput.characters[0], name: 'кипет' }]]) {
  const invalidComparison = await call(comparisonContract, comparisonContract, 'post', { characters });
  assert.equal(invalidComparison.response.status, 400);
}

// A never-seen fixture key must enter the ordinary durable character queue.
// The provider then confirms absence; no unsupported enqueue reason may fail SQL.
const coldName = 'Queue' + Date.now().toString(36).replace(/\d/g, digit => String.fromCharCode(97 + Number(digit)));
const coldInput = { characters: [{ region: 'us', realm: 'stormrage', name: coldName }] };
let cold = await call(comparisonContract, comparisonContract, 'post', coldInput);
assert.equal(cold.payload.data.entries[0].status, 'pending');
assert(cold.payload.data.entries[0].meta.job_id);
for (let attempt = 0; attempt < 10 && cold.payload.data.entries[0].status === 'pending'; attempt++) {
  await sleep(1000);
  cold = await call(comparisonContract, comparisonContract, 'post', coldInput);
}
assert.equal(cold.payload.data.entries[0].status, 'unavailable');
assert.equal(cold.payload.data.entries[0].reason, 'CHARACTER_UNAVAILABLE');
assert.equal(cold.payload.data.entries[0].data, null);

const invalid = await call('/api/v1/characters/kr/stormrage/Arcanis', '/api/v1/characters/{region}/{realm}/{name}');
assert.equal(invalid.response.status, 400);
await sleep(1000);
const invalidAnalysis = await call('/api/v1/characters/us/stormrage/Arcanis/analysis?content=pvp', '/api/v1/characters/{region}/{realm}/{name}/analysis');
assert.equal(invalidAnalysis.response.status, 400);
await sleep(1000);
const unknown = await call('/api/v1/characters/us/stormrage/Notavailable', '/api/v1/characters/{region}/{realm}/{name}');
if (unknown.response.status === 202) await sleep(3000);
const absent = await call('/api/v1/characters/us/stormrage/Notavailable', '/api/v1/characters/{region}/{realm}/{name}');
assert.equal(absent.response.status, 404);
assert.equal(absent.payload.error.code, 'CHARACTER_UNAVAILABLE');
const bypass = await call('/api/v1/refresh/characters', '/api/v1/refresh/characters', 'post', { region: 'us', realm: 'stormrage', name: 'Arcanis', force: true });
assert.equal(bypass.response.status, 400);
console.log(`PASS: ${requests} live HTTP responses match OpenAPI; characters/progress/rules, combat events, guild coverage and aggregates, bounded comparisons, unavailable-member isolation, safe validation.`);
