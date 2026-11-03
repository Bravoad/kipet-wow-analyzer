import { useEffect, useState } from 'react';
import { useCombatResource } from '../features/useCombatResource.ts';
import { dictionary, number } from '../i18n.ts';
import type { ApiError } from '../shared/api.ts';
import { combatDate, combatDuration, combatMatches, combatOffset, combatPath, fightPlayers, positiveActorID, reportCode, reportPath } from '../shared/combat.ts';
import type { CombatAnalysis, CombatantStatKey, CombatEvent, CombatReport } from '../shared/combat.ts';
import { combatMessages, eventText, findingText } from '../shared/combatMessages.ts';
import type { Locale, Runtime } from '../shared/types.ts';
import { Icon } from './Icon.tsx';

function initialSelection() {
  const params = new URLSearchParams(location.search);
  return { code: reportCode(params.get('report') ?? '') ?? '', fight: positiveActorID(params.get('fight')), actor: positiveActorID(params.get('actor')) };
}
function writeReportURL(code: string, fight: number | null, actor: number | null, push = false) {
  const params = new URLSearchParams({ view: 'reports' });
  if (code) params.set('report', code);
  if (fight) params.set('fight', String(fight));
  if (actor) params.set('actor', String(actor));
  history[push ? 'pushState' : 'replaceState'](null, '', `?${params}`);
}
function useRetrySeconds(error: ApiError | null) {
  const [seconds, setSeconds] = useState(0);
  useEffect(() => {
    const retry = error?.detail.retry_after_seconds;
    if (!retry) { setSeconds(0); return; }
    const until = Date.now() + retry * 1000; setSeconds(retry);
    const timer = setInterval(() => setSeconds(Math.max(0, Math.ceil((until - Date.now()) / 1000))), 1000);
    return () => clearInterval(timer);
  }, [error]);
  return seconds;
}

export function CombatWorkspace({ runtime, locale }: { runtime: Runtime | null; locale: Locale }) {
  const t = combatMessages(locale);
  const [selection, setSelection] = useState(initialSelection);
  const [input, setInput] = useState(selection.code);
  const [invalid, setInvalid] = useState(false);
  const [filter, setFilter] = useState('all');
  const enabled = runtime?.combat_mode === 'fixture' || runtime?.combat_mode === 'warcraftlogs';
  const report = useCombatResource<CombatReport>(enabled && selection.code ? reportPath(selection.code, locale) : null);
  const metadata = report.result;
  const data = metadata?.data.visibility === 'public' ? metadata.data : null;
  const fight = data?.fights?.find(item => item.id === selection.fight) ?? null;
  const players = fightPlayers(data?.actors ?? null, fight);
  const actor = players.find(item => item.id === selection.actor) ?? null;
  const analysis = useCombatResource<CombatAnalysis>(data && fight && actor ? combatPath(data.code, fight.id, actor.id, locale) : null, report.invalidate, metadata?.meta.snapshot_id ?? '');
  const matching = !!metadata && !!fight && !!actor && !!analysis.result && combatMatches(analysis.result, metadata, fight.id, actor.id);
  const changed = !!analysis.result && !matching;
  const error = report.error ?? analysis.error;
  const retrySeconds = useRetrySeconds(error);
  useEffect(() => {
    const pop = () => { const next = initialSelection(); setSelection(next); setInput(next.code); setInvalid(false); setFilter('all'); };
    window.addEventListener('popstate', pop); return () => window.removeEventListener('popstate', pop);
  }, []);
  const open = (code: string) => { const next = { code, fight: null, actor: null }; setSelection(next); setInput(code); setInvalid(false); setFilter('all'); writeReportURL(code, null, null, true); };
  const select = (part: 'fight' | 'actor', value: string) => {
    const id = positiveActorID(value); const next = { ...selection, [part]: id };
    if (part === 'fight' && data) {
      const selected = data.fights?.find(item => item.id === id) ?? null;
      if (!fightPlayers(data.actors, selected).some(item => item.id === next.actor)) next.actor = null;
    }
    setSelection(next); setFilter('all'); writeReportURL(next.code, next.fight, next.actor);
  };
  const errorText = !error ? '' : error.status === 0 ? t.network : ['REPORT_UNAVAILABLE', 'REPORT_NOT_FOUND'].includes(error.detail.code) ? t.unavailable : error.detail.code === 'PROVIDER_ACCESS_DENIED' ? t.denied : error.detail.code === 'COMBAT_NOT_CONFIGURED' ? t.disabled : error.status === 400 ? t.invalidRequest : t.error;
  const analysisData = matching ? analysis.result!.data : null;
  return <div className="combat-workspace">
    <section className="hero hero-compact"><div className="hero-copy"><div className="eyebrow"><span className="gold-line"/>{t.top}</div><h1>{t.title}</h1><p>{t.subtitle}</p></div><div className="combat-hero-mark" aria-hidden="true"><Icon name="chart" size={85}/></div></section>
    <section className="search-panel panel combat-search" aria-label={t.workspace}><form onSubmit={event => { event.preventDefault(); const code = reportCode(input); if (!code) { setInvalid(true); return; } open(code); }} noValidate><div className="field"><label htmlFor="combat-report-code">{t.input}</label><input id="combat-report-code" value={input} onChange={event => { setInput(event.target.value); setInvalid(false); }} placeholder={t.placeholder} autoComplete="off" spellCheck={false} aria-invalid={invalid} aria-describedby={invalid ? 'combat-validation' : undefined}/></div><button type="submit" className="button-primary" disabled={!enabled || report.loading || retrySeconds > 0}><Icon name={report.loading ? 'refresh' : 'search'} size={17} className={report.loading ? 'spinning' : ''}/>{t.open}</button></form>{invalid && <p className="validation-message" id="combat-validation" role="alert">{t.invalid}</p>}</section>
    {runtime?.combat_mode === 'fixture' && <div className="demo-banner combat-demo"><div><span className="demo-pill">{t.demo}</span><p>{t.demoText}</p></div><div className="demo-actions">{(runtime.demo_reports ?? []).map(code => <button type="button" key={code} className="demo-button" disabled={report.loading} onClick={() => open(code)}>{t.demoOpen}<Icon name="arrow" size={13}/></button>)}</div></div>}
    {!enabled && <section className="panel combat-state" role="status"><Icon name="info" size={22}/><div><h2>{runtime ? t.disabled : t.connecting}</h2>{runtime && <p>{runtime.combat_setup_required ? t.setupRequired : t.disabledText}</p>}</div></section>}
    {enabled && (report.loading || analysis.loading) && <div className="progress-banner" role="status" aria-live="polite"><Icon name="refresh" size={19} className="spinning"/><strong>{report.loading ? t.loading : t.calculating}</strong></div>}
    {error && <div className="error-banner" role="alert"><Icon name="info" size={20}/><div><strong>{errorText}</strong>{retrySeconds > 0 && <p>{t.retryIn} {retrySeconds} {t.seconds}</p>}</div>{error.detail.retryable && <button type="button" className="button-secondary" disabled={retrySeconds > 0} onClick={report.error ? report.reload : analysis.reload}>{t.retry}</button>}</div>}
    {(report.paused || analysis.paused) && <div className="progress-banner" role="status"><Icon name="clock" size={20}/><strong>{t.paused}</strong><button type="button" className="button-secondary" onClick={report.paused ? report.reload : analysis.reload}>{t.retry}</button></div>}
    {changed && <div className="analysis-notice" role="status"><Icon name="info" size={20}/><div><p>{t.changed}</p><button type="button" className="button-secondary" onClick={report.reload}>{t.reload}</button></div></div>}
    {enabled && !selection.code && <section className="combat-empty panel"><Icon name="book" size={33}/><h2>{t.emptyTitle}</h2><p>{t.emptyText}</p></section>}
    {data && metadata && <>
      <section className="panel combat-report-header"><div><span className="eyebrow">{t.public} · {data.code}</span><h2>{data.title ?? data.code}</h2><p>{combatDate(data.start_time, locale)} — {combatDate(data.end_time, locale)}</p><div className="combat-source"><span>{t.source}: <strong>{metadata.meta.source === 'fixture' ? t.fixture : t.live}</strong></span><span>{t.fetched}: {combatDate(metadata.meta.fetched_at, locale)}</span></div></div><div className="combat-report-actions"><span className={`badge ${metadata.meta.status === 'stale' ? 'amber' : 'teal'}`}>{metadata.meta.status === 'stale' ? t.stale : t.fresh}</span><button type="button" className="button-secondary" disabled={report.loading || retrySeconds > 0} onClick={report.reload}><Icon name="refresh" size={15}/>{t.reload}</button></div></section>
      <section className="panel combat-selection"><div className="field"><label htmlFor="combat-fight">{t.fight}</label><select id="combat-fight" value={fight?.id ?? ''} onChange={event => select('fight', event.target.value)}><option value="">{t.chooseFight}</option>{(data.fights ?? []).map(item => <option key={item.id} value={item.id}>{item.name ?? `#${item.id}`} · #{item.id} · {combatDuration(item.end_ms - item.start_ms)} · {item.kill === null ? t.outcomeUnknown : item.kill ? t.kill : t.wipe}</option>)}</select></div><div className="field"><label htmlFor="combat-actor">{t.actor}</label><select id="combat-actor" value={actor?.id ?? ''} onChange={event => select('actor', event.target.value)}><option value="">{t.chooseActor}</option>{players.map(item => <option key={item.id} value={item.id}>{item.name ?? `#${item.id}`} · #{item.id}{item.sub_type ? ` · ${item.sub_type}` : ''}</option>)}</select></div>{data.fights === null || data.actors === null ? <p>{t.listsMissing}</p> : !data.fights.length ? <p>{t.noFights}</p> : !players.length ? <p>{fight ? t.noFightPlayers : t.noActors}</p> : !fight || !actor ? <p>{t.selectPrompt}</p> : <p>{t.duration}: {combatDuration(fight.end_ms - fight.start_ms)} · {t.difficulty}: {number(fight.difficulty, locale)}</p>}{fight && fight.participant_ids === null && <p>{t.membershipUnknown}</p>}</section>
    </>}
    {analysisData && data && !report.loading && !analysis.loading && !error && <CombatResults data={analysisData} report={data} locale={locale} filter={filter} onFilter={setFilter}/>}
  </div>;
}

function CombatResults({ data, report, locale, filter, onFilter }: { data: CombatAnalysis; report: CombatReport; locale: Locale; filter: string; onFilter: (value: string) => void }) {
  const t = combatMessages(locale);
  const actorAmountsComplete = data.actor_amount_events_complete === true;
  const name = (id: number | null) => id === null ? '—' : report.actors?.find(actor => actor.id === id)?.name ?? `#${id}`;
  const timeline = data.timeline ?? [];
  const types = [...new Set(timeline.map(event => event.kind))];
  const filtered = timeline.map((event, index) => ({ event, index })).filter(({ event }) => filter === 'all' || event.kind === filter);
  const metrics = [
    { label:t.dps, value:data.metrics.dps, note:t.damage }, { label:t.hps, value:data.metrics.hps, note:t.healing },
    { label:t.damage, value:data.metrics.damage_total, note:t.duration }, { label:t.healing, value:data.metrics.healing_total, note:t.duration },
    { label:t.overheal, value:data.metrics.overheal_total, note:`${t.overhealPercent}: ${number(data.metrics.overheal_percent, locale)}${data.metrics.overheal_percent === null ? '' : '%'}` },
    { label:t.casts, value:data.metrics.casts, note:`${t.interrupts}: ${number(data.metrics.interrupts, locale)} · ${t.deaths}: ${number(data.metrics.deaths, locale)}` },
  ];
  return <div className="combat-results">
    <CombatantStatsPanel data={data} locale={locale}/>
    <section className="combat-completeness"><span className={`badge ${data.events_complete ? 'teal' : 'amber'}`}>{data.events_complete ? t.complete : t.incomplete}</span><span>{t.duration}: {combatDuration(data.duration_ms)}</span>{!data.events_complete && <p>{actorAmountsComplete ? t.actorAmountsText : t.incompleteText}</p>}</section>
    <div className="combat-metrics">{metrics.map(metric => <article className="panel combat-metric" key={metric.label}><span>{metric.label}</span><strong>{number(metric.value, locale)}</strong><p>{metric.note}</p></article>)}</div>
    <details className="panel combat-method"><summary><Icon name="info" size={17}/>{t.method}<Icon name="chevron" size={15}/></summary><div><p>{t.methodText}</p><p>{t.healingText}</p><code>{data.method}</code></div></details>
    <section className="panel detail-panel combat-findings"><div className="panel-heading"><h2>{t.findings}</h2><Icon name="spark" size={22}/></div>{!data.findings?.length ? <p className="empty-copy">{t.noFindings}</p> : <ul>{data.findings.map((finding, index) => <li key={`${finding.code}-${index}`}><span className={`badge ${finding.outcome === 'Info' ? 'teal' : 'muted'}`}>{finding.outcome === 'Info' ? t.info : t.noRules}</span><p>{findingText(finding.code, t, actorAmountsComplete)}</p>{!!finding.event_indexes?.length && <details><summary>{t.evidence} ({finding.event_indexes.length})</summary><ul>{finding.event_indexes.map(eventIndex => <li key={eventIndex}>{timeline[eventIndex] ? <EventLine event={timeline[eventIndex]} index={eventIndex} start={data.fight_start_ms} name={name} locale={locale}/> : t.unknown}</li>)}</ul></details>}</li>)}</ul>}</section>
    <section className="panel detail-panel"><div className="panel-heading"><h2>{t.abilities}</h2><Icon name="talents" size={22}/></div><p className="combat-note">{t.abilityNote}</p>{!data.abilities?.length ? <p className="empty-copy">{t.noAbilities}</p> : <div className="combat-abilities">{data.abilities.map(ability => <article key={ability.id}><h3>{t.ability} <span>#{ability.id}</span></h3><dl><div><dt>{t.casts}</dt><dd>{number(ability.casts, locale)}</dd></div><div><dt>{t.damage}</dt><dd>{number(ability.damage, locale)}</dd></div><div><dt>{t.healing}</dt><dd>{number(ability.healing, locale)}</dd></div></dl></article>)}</div>}</section>
    <section className="panel detail-panel"><div className="panel-heading combat-timeline-heading"><div><h2>{t.timeline}</h2><p>{t.timelineNote}</p></div><div className="field"><label htmlFor="combat-event-filter">{t.eventFilter}</label><select id="combat-event-filter" value={filter} onChange={event => onFilter(event.target.value)}><option value="all">{t.allEvents}</option>{types.map(kind => <option key={kind} value={kind}>{eventText(kind, t)}</option>)}</select></div></div>{data.timeline_truncated && <div className="analysis-notice"><Icon name="info" size={18}/><div><strong>{t.truncated}</strong><p>{t.truncatedText}</p></div></div>}{!filtered.length ? <p className="empty-copy">{t.noEvents}</p> : <ol className="combat-timeline">{filtered.map(({ event, index }) => <li key={index}><EventLine event={event} index={index} start={data.fight_start_ms} name={name} locale={locale}/></li>)}</ol>}</section>
  </div>;
}

function CombatantStatsPanel({ data, locale }: { data: CombatAnalysis; locale: Locale }) {
  const t = dictionary(locale);
  const combat = combatMessages(locale);
  const source = data.source === 'fixture' ? combat.fixture : data.source === 'warcraftlogs' ? combat.live : combat.unknown;
  const snapshot = data.combatant_stats;
  const values = snapshot?.values;
  const format = new Intl.NumberFormat(locale, { maximumFractionDigits: 20 });
  const display = (key: CombatantStatKey) => {
    const value = values?.[key];
    return value == null ? '—' : format.format(value);
  };
  const groups: { label: string; icon: string; rows: [CombatantStatKey, string][] }[] = [
    { label: t.critical_strike, icon: 'gem', rows: [
      ['critical_strike', combat.amount], ['critical_strike_melee', t.melee],
      ['critical_strike_ranged', t.ranged], ['critical_strike_spell', t.spell],
    ] },
    { label: t.haste, icon: 'clock', rows: [
      ['haste', combat.amount], ['haste_melee', t.melee], ['haste_ranged', t.ranged], ['haste_spell', t.spell],
    ] },
    { label: t.mastery, icon: 'talents', rows: [['mastery', combat.amount]] },
    { label: t.versatility, icon: 'shield', rows: [
      ['versatility', combat.amount], ['versatility_damage_done', t.damageBonus],
      ['versatility_healing_done', t.healingBonus], ['versatility_damage_reduction', t.damageReduction],
    ] },
  ];
  return <section className="panel detail-panel">
    <div className="panel-heading">
      <div><span className="eyebrow">{source}</span><h2>{combat.statsTitle}</h2><p>{combat.statsScope}</p></div>
      <Icon name="chart" size={22}/>
    </div>
    {snapshot && values && Object.values(values).some(value => value != null) ? <>
      <p className="combat-note">{combat.statsObserved} {combatOffset(snapshot.observed_at_ms, data.fight_start_ms)} · {combat.statsValues}</p>
      <h3 className="subheading">{t.primary}</h3>
      <div className="primary-stats combat-primary-stats">
        {(['strength', 'agility', 'intellect', 'stamina'] as const).map(key =>
          <div className="primary-stat" key={key}><span>{t[key]}</span><strong>{display(key)}</strong></div>)}
      </div>
      <h3 className="subheading">{t.secondary}</h3>
      <div className="secondary-stats">
        {groups.map(group => {
          const known = group.rows.filter(([key]) => values[key] != null);
          return <article className="stat-card" key={group.label}>
            <h4><Icon name={group.icon} size={17}/>{group.label}</h4>
            {(known.length ? known : group.rows.slice(0, 1)).map(([key, label]) =>
              <div className="stat-pair" key={key}><span>{label}</span><strong>{display(key)}</strong></div>)}
          </article>;
        })}
      </div>
    </> : <p className="empty-copy">{combat.statsMissing}</p>}
  </section>;
}

function EventLine({ event, index, start, name, locale }: { event: CombatEvent; index: number; start: number; name: (id:number|null) => string; locale:Locale }) {
  const t = combatMessages(locale);
  return <div className={`combat-event combat-event-${event.kind}`}><div className="combat-event-time"><strong>{combatOffset(event.timestamp_ms, start)}</strong><span>{t.eventIndex} #{index + 1}</span></div><div className="combat-event-main"><strong>{eventText(event.kind, t)}{event.ability_id ? ` · #${event.ability_id}` : ''}</strong><span>{name(event.source_id)} → {name(event.target_id)}</span></div><div className="combat-event-amount"><strong>{number(event.amount, locale)}</strong>{event.overheal !== null && <span>{t.overheal}: {number(event.overheal, locale)}</span>}</div></div>;
}
