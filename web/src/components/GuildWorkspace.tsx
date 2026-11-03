import { useEffect, useRef, useState } from 'react';
import { useCombatResource } from '../features/useCombatResource.ts';
import { dictionary, formatDate, number } from '../i18n.ts';
import { ApiError, get, nextPollDelay, request, wait } from '../shared/api.ts';
import { characterHref, characterKeyID, downloadJSON, exportable, guildMatches, guildPath, sameSpecialization, validGuildSlug } from '../shared/social.ts';
import type { Distribution, GuildAnalysis, GuildMemberSummary, GuildProfile, GuildRoster } from '../shared/social.ts';
import { observationStatus, socialMessages } from '../shared/socialMessages.ts';
import type { CharacterKey, Envelope, Job, Locale, Region, Runtime } from '../shared/types.ts';
import { Icon } from './Icon.tsx';
import { RealmPicker } from './RealmPicker.tsx';
import type { RealmSelection } from './RealmPicker.tsx';

function initialGuild(): CharacterKey | null { const p = new URLSearchParams(location.search); const region = p.get('region'); const realm = p.get('realm'); const name = p.get('guild'); return (region === 'eu' || region === 'us') && realm && name && validGuildSlug(name) ? { region, realm, name } : null; }
function errorText(error: ApiError, locale: Locale) { const t = socialMessages(locale); return error.status === 0 ? t.network : ['GUILD_UNAVAILABLE', 'GUILD_NOT_FOUND'].includes(error.detail.code) ? t.notFound : error.detail.code === 'PROVIDER_ACCESS_DENIED' ? t.denied : error.status === 429 ? t.cooldown : t.error; }

export function GuildWorkspace({ runtime, locale, onCompare }: { runtime: Runtime | null; locale: Locale; onCompare: (keys: CharacterKey[]) => void }) {
  const t = socialMessages(locale); const standard = dictionary(locale);
  const initial = useRef(initialGuild());
  const [key, setKey] = useState<CharacterKey | null>(initial.current);
  const [region, setRegion] = useState<Region>(initial.current?.region ?? 'eu');
  const [realm, setRealm] = useState<RealmSelection | null>(initial.current ? { slug: initial.current.realm, name: initial.current.realm } : null);
  const [slug, setSlug] = useState(initial.current?.name ?? '');
  const [invalid, setInvalid] = useState(false);
  const [classFilter, setClassFilter] = useState(''); const [specFilter, setSpecFilter] = useState(''); const [selected, setSelected] = useState<string[]>([]);
  const [action, setAction] = useState<'refresh' | 'export' | null>(null); const [actionError, setActionError] = useState<ApiError | null>(null); const [notice, setNotice] = useState('');
  const [retryUntil, setRetryUntil] = useState(0); const [retrySeconds, setRetrySeconds] = useState(0);
  const actions = useRef<AbortController | null>(null);
  const binding = key ? JSON.stringify([key, locale]) : ''; const activeBinding = useRef(binding); activeBinding.current = binding;
  const profile = useCombatResource<GuildProfile>(key ? guildPath(key, locale) : null);
  const metadata = profile.result;
  const roster = useCombatResource<GuildRoster>(key && metadata ? guildPath(key, locale, 'roster') : null, profile.invalidate, metadata?.meta.snapshot_id ?? '');
  const analysis = useCombatResource<GuildAnalysis>(key && metadata ? guildPath(key, locale, 'analysis') : null, profile.invalidate, metadata?.meta.snapshot_id ?? '');
  const matchingRoster = guildMatches(roster.result, metadata) ? roster.result : null;
  const matchingAnalysis = guildMatches(analysis.result, metadata) ? analysis.result : null;
  const changed = !!metadata && (!!roster.result && !matchingRoster || !!analysis.result && !matchingAnalysis);
  const data = matchingAnalysis?.data;
  const rows: GuildMemberSummary[] = data?.members ?? matchingRoster?.data.members?.map(member => ({ member, status: 'pending', snapshot_id: null, specialization: null, role: null, item_level: null, mythic_rating: null, mythic_season: null, notices: null })) ?? [];
  const selectedRows = rows.filter(row => selected.includes(characterKeyID(row.member.key)));
  const filtered = rows.filter(row => (!classFilter || row.member.character_class?.id === classFilter) && (!specFilter || row.specialization?.id === specFilter));
  const busy = profile.loading || !!action; const error = actionError ?? profile.error ?? roster.error ?? analysis.error;

  useEffect(() => { const pop = () => { const next = initialGuild(); setKey(next); if (next) { setRegion(next.region); setRealm({ slug: next.realm, name: next.realm }); setSlug(next.name); } }; window.addEventListener('popstate', pop); return () => window.removeEventListener('popstate', pop); }, []);
  useEffect(() => { setSelected([]); setClassFilter(''); setSpecFilter(''); setAction(null); setActionError(null); setNotice(''); actions.current?.abort(); return () => { actions.current?.abort(); if (activeBinding.current === binding) activeBinding.current = ''; }; }, [binding]);
  useEffect(() => { setSelected([]); }, [metadata?.meta.snapshot_id]);
  useEffect(() => { if (!metadata || metadata.data.region !== region || metadata.data.realm.slug !== realm?.slug) return; if (realm.name !== metadata.data.realm.name) setRealm({ slug: realm.slug, name: metadata.data.realm.name }); }, [metadata, region, realm]);
  useEffect(() => { const seconds = error?.detail.retry_after_seconds; if (seconds) setRetryUntil(Date.now() + seconds * 1000); }, [error]);
  useEffect(() => { const update = () => setRetrySeconds(Math.max(0, Math.ceil((retryUntil - Date.now()) / 1000))); update(); if (!retryUntil) return; const timer = setInterval(update, 1000); return () => clearInterval(timer); }, [retryUntil]);

  const open = (next: CharacterKey) => {
    setKey(next); setRegion(next.region); setRealm({ slug: next.realm, name: next.realm }); setSlug(next.name); setInvalid(false);
    const params = new URLSearchParams({ view: 'guilds', region: next.region, realm: next.realm, guild: next.name });
    if (runtime?.raiderio_enabled || new URLSearchParams(location.search).get('source') === 'fixture') params.set('source', 'fixture');
    history.pushState(null, '', `?${params}`);
  };
  const reload = () => { profile.reload(); roster.reload(); analysis.reload(); setNotice(''); };
  const refresh = async () => {
    if (!key || action || retrySeconds > 0) return;
    const operation = binding; const controller = new AbortController(); actions.current?.abort(); actions.current = controller;
    setAction('refresh'); setActionError(null); setNotice('');
    try {
      const response = await request<Envelope<unknown>>('/api/v1/refresh/guilds', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...key, locale }), signal: controller.signal });
      const began = Date.now(); let attempt = 0; let suggestion = response.retryAfter;
      if (response.status === 202 && response.body.meta.job_id) {
        for (;;) {
          const delay = nextPollDelay(attempt++, suggestion);
          if (Date.now() - began + delay > 120000) { if (activeBinding.current === operation) setNotice(t.paused); return; }
          await wait(delay, controller.signal);
          let job;
          try { job = await get<Job>(`/api/v1/jobs/${encodeURIComponent(response.body.meta.job_id)}`, controller.signal); }
          catch (failure) { if (failure instanceof ApiError && failure.detail.code === 'JOB_NOT_FOUND') { await get<GuildProfile>(guildPath(key, locale), controller.signal); break; } throw failure; }
          if (job.body.data.status === 'succeeded') break;
          if (['failed', 'cancelled'].includes(job.body.data.status)) throw new ApiError(503, { code: job.body.data.error_code ?? 'PROVIDER_UNAVAILABLE', message: '', retryable: true, retry_after_seconds: job.retryAfter, job_id: response.body.meta.job_id, fields: [] });
          suggestion = Math.max(job.retryAfter ?? 0, job.body.data.next_poll_after_seconds ?? 0);
        }
      }
      if (activeBinding.current === operation) reload();
    } catch (failure) { if (!controller.signal.aborted && activeBinding.current === operation) { const failureValue = failure instanceof ApiError ? failure : new ApiError(0, { code: 'NETWORK_ERROR', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] }); setActionError(failureValue); if (['GUILD_UNAVAILABLE', 'PROVIDER_ACCESS_DENIED'].includes(failureValue.detail.code)) profile.invalidate(failureValue); } }
    finally { if (activeBinding.current === operation) setAction(null); }
  };
  const exportResult = async () => {
    if (!key || action || !metadata) return;
    const operation = binding; const controller = new AbortController(); actions.current?.abort(); actions.current = controller; setAction('export'); setActionError(null); setNotice('');
    try { const fresh = await get<GuildAnalysis>(guildPath(key, locale, 'analysis'), controller.signal); if (activeBinding.current !== operation) return; if (fresh.status !== 200 || !exportable(fresh.body.meta) || !guildMatches(fresh.body, metadata)) { reload(); setNotice(t.exportUnavailable); return; } downloadJSON(fresh.body, `kipet-guild-${key.name}.json`); }
    catch (failure) { if (!controller.signal.aborted && activeBinding.current === operation) { const failureValue = failure instanceof ApiError ? failure : new ApiError(0, { code: 'NETWORK_ERROR', message: '', retryable: true, retry_after_seconds: null, job_id: null, fields: [] }); setActionError(failureValue); if (['GUILD_UNAVAILABLE', 'PROVIDER_ACCESS_DENIED'].includes(failureValue.detail.code)) profile.invalidate(failureValue); } }
    finally { if (activeBinding.current === operation) setAction(null); }
  };
  return <div className="social-workspace">
    <section className="hero hero-compact"><div className="hero-copy"><div className="eyebrow"><span className="gold-line"/>{t.guildTop}</div><h1>{t.guildTitle}</h1><p>{t.guildSubtitle}</p></div></section>
    <section className="search-panel panel social-search"><form onSubmit={event => { event.preventDefault(); if (!realm || !validGuildSlug(slug)) { setInvalid(true); return; } open({ region, realm: realm.slug, name: slug.trim().normalize('NFC') }); }} noValidate><div className="field"><label htmlFor="guild-region">{standard.region}</label><div className="select-wrap"><select id="guild-region" value={region} onChange={event => { setRegion(event.target.value as Region); setRealm(null); }}><option value="eu">Europe · EU</option><option value="us">Americas · US</option></select></div></div><div className="field"><label htmlFor="guild-realm">{standard.realm}</label><RealmPicker id="guild-realm" errorID="guild-validation" region={region} locale={locale} selected={realm} onChange={setRealm} t={standard} invalid={invalid && !realm}/></div><div className="field"><label htmlFor="guild-slug">{t.slug}</label><input id="guild-slug" value={slug} onChange={event => { setSlug(event.target.value); setInvalid(false); }} placeholder="kipet-lab" autoComplete="off" aria-invalid={invalid} aria-describedby="guild-slug-hint"/></div><button type="submit" className="button-primary" disabled={busy || retrySeconds > 0}><Icon name="search" size={17}/>{t.findGuild}</button></form><p className="social-note" id="guild-slug-hint">{t.slugHint}</p>{invalid && <p className="validation-message" role="alert" id="guild-validation">{t.invalidGuild}</p>}</section>
    {runtime?.provider_mode === 'fixture' && <div className="demo-banner"><div><span className="demo-pill">{t.demo}</span><p>{t.demoText}</p></div><div className="demo-actions">{runtime.demo_guilds?.map(demo => <button type="button" className="demo-button" key={characterKeyID(demo)} onClick={() => open(demo)} disabled={busy}>{t.demoGuild}<Icon name="arrow" size={13}/></button>)}</div></div>}
    {(profile.loading || action === 'refresh') && <div className="progress-banner" role="status" aria-live="polite"><Icon name="refresh" className="spinning" size={18}/>{t.loading}</div>}
    {error && <div className="error-banner" role="alert"><Icon name="info" size={20}/><div><strong>{errorText(error, locale)}</strong>{retrySeconds > 0 && <p>{retrySeconds} {t.seconds}</p>}</div><button type="button" className="button-secondary" onClick={reload} disabled={retrySeconds > 0}>{t.retry}</button></div>}
    {(notice || profile.paused || roster.paused || analysis.paused || changed) && <div className="analysis-notice" role="status"><Icon name="info" size={20}/><div><p>{changed ? t.changed : notice || t.paused}</p><button type="button" className="button-secondary" onClick={reload}>{t.reload}</button></div></div>}
    {metadata && <>
      <section className="panel social-header"><div><span className="eyebrow">{metadata.data.region.toUpperCase()} · {metadata.data.realm.name}</span><h2>{metadata.data.name}</h2><p>{t.source}: {metadata.meta.source === 'fixture' ? t.fixture : t.live} · {t.fetched}: {formatDate(metadata.meta.fetched_at, locale)}</p><p>{t.reported}: {number(metadata.data.member_count, locale)}</p></div><div className="social-actions"><button type="button" className="button-secondary" disabled={busy || retrySeconds > 0} onClick={() => void refresh()}><Icon name="refresh" size={15}/>{t.refresh}</button><button type="button" className="button-secondary" disabled={!!action} onClick={reload}>{t.reload}</button><button type="button" className="button-secondary" disabled={!data || !!action || !matchingAnalysis || !exportable(matchingAnalysis.meta)} onClick={() => void exportResult()}>{action === 'export' ? t.exporting : t.export}</button></div></section>
      {(roster.loading || analysis.loading) && <p className="social-note" role="status">{t.loading}</p>}
      {matchingRoster?.data.members === null && <section className="panel social-panel"><p className="social-note">{t.rosterUnknown}</p></section>}
      {data && !changed && data.coverage.roster_known && <>
        <div className="social-metrics"><SocialMetric label={t.profileCoverage} value={data.coverage.roster_known ? `${data.coverage.profiles} / ${data.coverage.roster}` : t.unknown} note={`${t.fresh}: ${data.coverage.fresh} · ${t.stale}: ${data.coverage.stale}`}/><SocialMetric label={t.mean} value={number(data.item_level.mean, locale)} note={`${t.sample}: ${data.item_level.count}`}/><SocialMetric label={t.median} value={number(data.item_level.median, locale)} note={`${t.pending}: ${data.coverage.pending} · ${t.unavailable}: ${data.coverage.unavailable}`}/></div>
        <p className="social-note">{t.background}</p>
        <div className="social-distributions"><DistributionPanel title={t.classes} values={data.classes} locale={locale}/><DistributionPanel title={t.specs} values={data.specializations} locale={locale}/><DistributionPanel title={t.roles} values={data.roles} locale={locale} note={t.rolesNote}/></div>
        <div className="social-summary-grid"><section className="panel social-panel"><h2>{t.gear}</h2><dl className="social-facts"><div><dt>{t.enchanted}</dt><dd>{number(data.gear.enchanted, locale)}</dd></div><div><dt>{t.emptyEnchant}</dt><dd>{number(data.gear.empty_enchantments, locale)}</dd></div><div><dt>{t.unknownEnchant}</dt><dd>{number(data.gear.unknown_enchantments, locale)}</dd></div><div><dt>{t.sets}</dt><dd>{number(data.gear.known_sets, locale)}</dd></div></dl><p className="social-note">{t.gearNote}</p></section><section className="panel social-panel"><h2>{t.progress}</h2><dl className="social-facts"><div><dt>{t.mythic}</dt><dd>{data.progress.mythic_profiles} / {data.coverage.roster}</dd></div><div><dt>{t.raids}</dt><dd>{data.progress.raid_profiles} / {data.coverage.roster}</dd></div></dl><p className="social-note">{t.progressNote}</p></section></div>
      </>}
      {matchingRoster && !changed && <section className="panel social-panel"><div className="social-section-heading"><h2>{t.roster}</h2><div className="social-filter field"><label htmlFor="guild-class-filter">{t.filterClass}</label><select id="guild-class-filter" value={classFilter} onChange={event => setClassFilter(event.target.value)}><option value="">{t.all}</option>{uniqueLabels(rows.map(row => row.member.character_class)).map(value => <option key={value.id} value={value.id}>{value.name ?? `#${value.id}`}</option>)}</select></div><div className="social-filter field"><label htmlFor="guild-spec-filter">{t.filterSpec}</label><select id="guild-spec-filter" value={specFilter} onChange={event => setSpecFilter(event.target.value)}><option value="">{t.all}</option>{uniqueLabels(rows.map(row => row.specialization)).map(value => <option key={value.id} value={value.id}>{value.name ?? `#${value.id}`}</option>)}</select></div></div><p className="social-note">{t.sameSpecNote}</p><div className="social-table-scroll"><table className="social-table"><thead><tr><th>{t.select}</th><th>{t.name}</th><th>{t.class}</th><th>{t.spec}</th><th>{t.status}</th><th>{t.ilvl}</th><th>{t.mythic}</th><th>{t.notices}</th></tr></thead><tbody>{filtered.map(row => { const id = characterKeyID(row.member.key); const checked = selected.includes(id); const eligible = !!row.snapshot_id && !!row.specialization && (!selectedRows.length || sameSpecialization([...selectedRows, row])); return <tr key={id}><td><input type="checkbox" aria-label={`${t.select}: ${row.member.key.name}`} checked={checked} disabled={!checked && (!eligible || selected.length >= 5)} onChange={() => setSelected(previous => checked ? previous.filter(value => value !== id) : [...previous, id])}/></td><th scope="row"><a href={characterHref(row.member.key)}>{row.member.key.name}</a><small>{row.member.key.realm} · {row.member.key.region.toUpperCase()}</small></th><td>{row.member.character_class?.name ?? row.member.character_class?.id ?? '—'}</td><td>{row.specialization?.name ?? row.specialization?.id ?? '—'}</td><td><span className={`badge ${row.status === 'fresh' ? 'teal' : row.status === 'stale' ? 'amber' : 'muted'}`}>{observationStatus(row.status, t)}</span></td><td>{number(row.item_level, locale)}</td><td>{number(row.mythic_rating, locale)}<small>{t.season}: {row.mythic_season ?? '—'}</small></td><td>{number(row.notices, locale)}</td></tr>; })}</tbody></table></div>{!filtered.length && <p className="social-note">{matchingRoster.data.members === null ? t.rosterUnknown : rows.length ? t.filterEmpty : t.rosterEmpty}</p>}<div className="social-selection-footer"><span>{t.selected}: {selectedRows.length} / 5</span><button type="button" className="button-secondary" disabled={selectedRows.length < 2 || !sameSpecialization(selectedRows)} onClick={() => onCompare(selectedRows.map(row => row.member.key))}>{t.compareSelected}<Icon name="arrow" size={15}/></button></div><p className="social-note">{t.noticesNote}</p></section>}
      <section className="analysis-notice"><Icon name="info" size={20}/><div><strong>{t.limits}</strong><p>{t.limitsText}</p><p>{t.logsUnlinked}</p><p>{t.exportNote}</p></div></section>
    </>}
  </div>;
}
function uniqueLabels(values: ({ id: string; name: string | null } | null)[]) { return [...new Map(values.filter((value): value is { id: string; name: string | null } => value !== null).map(value => [value.id, value])).values()]; }
export function SocialMetric({ label, value, note }: { label: string; value: string; note: string }) { return <article className="panel social-metric"><span>{label}</span><strong>{value}</strong><p>{note}</p></article>; }
function DistributionPanel({ title, values, locale, note }: { title: string; values: Distribution[]; locale: Locale; note?: string }) { const t = socialMessages(locale); return <section className="panel social-panel"><h2>{title}</h2>{values.length ? <ul className="social-distribution">{values.map(value => <li key={value.id}><span>{value.name ?? (value.id === 'unknown' ? t.unknown : `#${value.id}`)}</span><strong>{number(value.count, locale)}</strong></li>)}</ul> : <p className="social-note">{t.none}</p>}{note && <p className="social-note">{note}</p>}</section>; }
