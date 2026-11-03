import { useEffect, useRef, useState } from 'react';
import { dictionary, formatDate, number } from './i18n.ts';
import { Icon } from './components/Icon.tsx';
import { RealmPicker } from './components/RealmPicker.tsx';
import type { RealmSelection } from './components/RealmPicker.tsx';
import { CoverageBadge, EquipmentPanel, StatisticsPanel, TalentsPanel } from './components/Details.tsx';
import { useCharacter } from './features/useCharacter.ts';
import { useAnalysis } from './features/useAnalysis.ts';
import { useProgress } from './features/useProgress.ts';
import { ProgressPanel } from './components/Progress.tsx';
import { AnalysisPanel } from './components/Analysis.tsx';
import { CombatWorkspace } from './components/CombatWorkspace.tsx';
import { GuildWorkspace } from './components/GuildWorkspace.tsx';
import { ComparisonWorkspace } from './components/ComparisonWorkspace.tsx';
import { RaiderWorkspace } from './components/RaiderWorkspace.tsx';
import { RaiderComparisonWorkspace, RaiderGuildWorkspace } from './components/RaiderSocialWorkspace.tsx';
import { socialMessages } from './shared/socialMessages.ts';
import { combatMessages } from './shared/combatMessages.ts';
import { request } from './shared/api.ts';
import type { ApiError } from './shared/api.ts';
import { isRaiderSocialRoute } from './shared/raider.ts';
import type { AnalysisContent, CharacterKey, Locale, Region, Runtime, SourceSection, Tab } from './shared/types.ts';

const tabs: { id: Tab; icon: string; index: string }[] = [{ id: 'overview', icon: 'shield', index: '00' }, { id: 'equipment', icon: 'sword', index: '01' }, { id: 'statistics', icon: 'chart', index: '02' }, { id: 'talents', icon: 'talents', index: '03' }, { id: 'analysis', icon: 'spark', index: '04' }, { id: 'mythic_plus', icon: 'clock', index: '05' }, { id: 'raids', icon: 'shield', index: '06' }];
const sectionNames: SourceSection[] = ['profile', 'equipment', 'statistics', 'talents', 'mythic_plus', 'raids'];
const classColors: Record<string, string> = { '1': '#c69b6d', '2': '#f48cba', '3': '#aad372', '4': '#fff468', '5': '#f0efed', '6': '#e7697c', '7': '#72a8eb', '8': '#69c4df', '9': '#b4a0e5', '10': '#65d6ae', '11': '#e59c58', '12': '#ba83ed', '13': '#68bdab' };

function initialLocale(): Locale { try { return localStorage.getItem('kipet-locale') === 'en-US' ? 'en-US' : 'ru-RU'; } catch { return 'ru-RU'; } }
function keyFromURL(): CharacterKey | null {
  const params = new URLSearchParams(location.search);
  const region = params.get('region'); const realm = params.get('realm'); const name = params.get('name');
  return (region === 'eu' || region === 'us') && realm && name ? { region, realm, name } : null;
}
function contentFromURL(): AnalysisContent { const content = new URLSearchParams(location.search).get('content'); return content === 'raid' || content === 'mythic_plus' ? content : 'general_pve'; }
type Workspace = 'character' | 'reports' | 'guilds' | 'comparison' | 'raiderio';
function workspaceFromURL(): Workspace {
  const value = new URLSearchParams(location.search).get('view');
  return value === 'reports' || value === 'guilds' || value === 'comparison' || value === 'raiderio' ? value : 'character';
}
function writeURL(key: CharacterKey, tab: Tab, replace = false, content: AnalysisContent = 'general_pve') {
  const params = new URLSearchParams({ region: key.region, realm: key.realm, name: key.name });
  if (tab !== 'overview') params.set('tab', tab);
  if (tab === 'analysis' && content !== 'general_pve') params.set('content', content);
  history[replace ? 'replaceState' : 'pushState'](null, '', `?${params}`);
}

export default function App() {
  const [locale, setLocale] = useState<Locale>(initialLocale);
  const [view, setView] = useState<Workspace>(workspaceFromURL);
  const [routeSearch, setRouteSearch] = useState(() => location.search);
  const socialText=socialMessages(locale);
  const combatText = combatMessages(locale);
  const reportRoute = useRef(new URLSearchParams(location.search).get('view') === 'reports' ? location.search : '?view=reports');
  const extraRoutes = useRef({
    guilds: workspaceFromURL() === 'guilds' ? location.search : '?view=guilds',
    comparison: workspaceFromURL() === 'comparison' ? location.search : '?view=comparison',
    raiderio: workspaceFromURL() === 'raiderio' ? location.search : '?view=raiderio',
  });
  const t = dictionary(locale);
  const initialKey = useRef(workspaceFromURL() === 'character' ? keyFromURL() : null);
  const [region, setRegion] = useState<Region>(initialKey.current?.region ?? 'eu');
  const [realm, setRealm] = useState<RealmSelection | null>(initialKey.current ? { slug: initialKey.current.realm, name: initialKey.current.realm } : null);
  const [name, setName] = useState(initialKey.current?.name ?? '');
  const [validation, setValidation] = useState<'realm' | 'name' | null>(null);
  const [runtime, setRuntime] = useState<Runtime | null>(null);
  const [tab, setTab] = useState<Tab>(() => tabs.some(item => item.id === new URLSearchParams(location.search).get('tab')) ? new URLSearchParams(location.search).get('tab') as Tab : 'overview');
  const [cooldown, setCooldown] = useState(0);
  const [analysisContent, setAnalysisContent] = useState<AnalysisContent>(contentFromURL);
  const character = useCharacter(locale);
  const analysis = useAnalysis(character.key, character.profile, locale, analysisContent, view === 'character' && tab === 'analysis', character.invalidate);
  const progress = useProgress(character.key, character.profile, locale, view === 'character' && (tab === 'mythic_plus' || tab === 'raids') ? tab : null, character.invalidate);
  const profile = character.profile?.data;
  const profileMeta = character.profile?.meta;
  const demo = runtime?.provider_mode === 'fixture' || profileMeta?.source === 'fixture';
  const raiderVisible = runtime?.raiderio_enabled || view === 'raiderio';
  const raiderSocial = isRaiderSocialRoute(routeSearch, runtime);
  const socialWorkspace = view === 'guilds' || view === 'comparison';
  const legacyCharacterLabel = runtime?.raiderio_enabled && demo ? (locale === 'ru-RU' ? 'Демопрофиль' : 'Demo profile') : combatText.character;
  const sourceLabel = demo ? t.sourceFixture : runtime || profileMeta?.source === 'blizzard' ? t.sourceBlizzard : t.sourceUnknown;

  useEffect(() => {
    document.documentElement.lang = locale === 'ru-RU' ? 'ru' : 'en';
    try { localStorage.setItem('kipet-locale', locale); } catch { /* Storage is optional. */ }
  }, [locale]);
  useEffect(() => {
    const controller = new AbortController();
    void request<Runtime | { data: Runtime }>('/api/v1/runtime', { signal: controller.signal }).then(result => {
      const next = 'data' in result.body ? result.body.data : result.body;
      setRuntime(next);
      if (next.raiderio_enabled && !location.search) {
        history.replaceState(null, '', '?view=raiderio');
        setView('raiderio');
      }
    }).catch(() => {});
    return () => controller.abort();
  }, []);
  useEffect(() => {
    if (initialKey.current) void character.load(initialKey.current);
    const pop = () => {
      const nextWorkspace = workspaceFromURL();
      setView(nextWorkspace);
      setRouteSearch(location.search);
      const key = nextWorkspace === 'character' ? keyFromURL() : null;
      const nextTab = new URLSearchParams(location.search).get('tab');
      setTab(tabs.some(item => item.id === nextTab) ? nextTab as Tab : 'overview'); setAnalysisContent(contentFromURL());
      if (key) { setRegion(key.region); setRealm({ slug: key.realm, name: key.realm }); setName(key.name); void character.load(key); }
    };
    window.addEventListener('popstate', pop);
    return () => window.removeEventListener('popstate', pop);
    // The hook handles locale changes and cancellation independently.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    if (profile && profile.realm.slug === realm?.slug && profile.region === region) setRealm(previous => previous?.name === profile.realm.name ? previous : { slug: profile.realm.slug, name: profile.realm.name });
  }, [profile, realm?.slug, region]);
  useEffect(() => {
    const seconds = character.error?.detail.retry_after_seconds;
    if (!seconds) { setCooldown(0); return; }
    const deadline = Date.now() + seconds * 1000;
    setCooldown(seconds);
    const timer = setInterval(() => setCooldown(Math.max(0, Math.ceil((deadline - Date.now()) / 1000))), 1000);
    return () => clearInterval(timer);
  }, [character.error]);

  const open = (key: CharacterKey) => {
    setView('character'); setRegion(key.region); setName(key.name); setValidation(null); setTab('overview');
    if (key.realm !== realm?.slug || key.region !== region) setRealm({ slug: key.realm, name: key.realm });
    writeURL(key, 'overview'); void character.load(key);
  };
  const search = (event: React.SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!realm) { setValidation('realm'); return; }
    const normalized = name.trim().normalize('NFC');
    if (!/^\p{L}[\p{L}\p{M}]+$/u.test(normalized) || Array.from(normalized).length > 24) { setValidation('name'); return; }
    open({ region, realm: realm.slug, name: normalized });
  };
  const rememberWorkspace = () => {
    if (view === 'reports') reportRoute.current = location.search;
    if (view === 'guilds' || view === 'comparison' || view === 'raiderio') extraRoutes.current[view] = location.search;
  };
  const selectWorkspace = (next: Workspace) => {
    if (next === view) return;
    rememberWorkspace();
    setView(next);
    if (next === 'reports') history.pushState(null, '', reportRoute.current);
    else if (next !== 'character') history.pushState(null, '', extraRoutes.current[next]);
    else if (character.key) writeURL(character.key, tab, false, analysisContent);
    else history.pushState(null, '', runtime?.raiderio_enabled ? '?view=character' : location.pathname);
    setRouteSearch(location.search);
  };
  const compareMembers = (keys: CharacterKey[]) => {
    rememberWorkspace();
    const params = new URLSearchParams({ view: 'comparison', characters: JSON.stringify(keys) });
    if (runtime?.raiderio_enabled || new URLSearchParams(location.search).get('source') === 'fixture') params.set('source', 'fixture');
    const target = `?${params}`;
    extraRoutes.current.comparison = target;
    history.pushState(null, '', target);
    setRouteSearch(location.search);
    setView('comparison');
  };
  const selectTab = (next: Tab) => {
    rememberWorkspace();
    setView('character');
    setTab(next);
    if (character.key) writeURL(character.key, next, true, analysisContent);
    else history.replaceState(null, '', runtime?.raiderio_enabled ? '?view=character' : location.pathname);
  };
  const selectContent = (next: AnalysisContent) => { setAnalysisContent(next); if (character.key) writeURL(character.key, 'analysis', true, next); };
  const errorText = (error: ApiError) => {
    if (error.status === 0) return t.networkError;
    if (error.detail.code === 'CHARACTER_UNAVAILABLE' || error.detail.code === 'REALM_NOT_FOUND') return t.notFound;
    if (error.detail.code === 'PROVIDER_ACCESS_DENIED') return t.accessDenied;
    if (error.status === 429) return t.rateLimited;
    if (error.status === 400) return t.invalidArgument;
    if (error.detail.code === 'CANCELLED') return t.jobCancelled;
    return t.providerError;
  };
  const sectionMeta = (section: SourceSection) => section === 'mythic_plus' || section === 'raids' ? (tab === section ? progress.result?.meta : undefined) ?? profileMeta : section === 'profile' ? profileMeta : character.sections[section]?.meta ?? profileMeta;
  const selectedCoverage = tab === 'overview' || tab === 'analysis' ? profileMeta?.coverage.profile : sectionMeta(tab)?.coverage[tab];

  return <div className="app-shell">
    <aside className="sidebar"><a className="brand" href="/" aria-label="Kipet WoW Analyzer"><BrandMark/><span><strong>KIPET<span className="brand-dot">.</span></strong><small>WOW ANALYZER</small></span></a><div className="sidebar-rule"/><div className="nav-label">{t.workspace}</div><nav aria-label={t.workspace}>{raiderVisible && <button type="button" className={`sidebar-link ${view === 'raiderio' ? 'selected' : ''}`} onClick={() => selectWorkspace('raiderio')} aria-label="Raider.IO"><Icon name="globe" size={19}/><span>Raider.IO</span></button>}<button type="button" className={`sidebar-link ${view === 'reports' ? 'selected' : ''}`} onClick={() => selectWorkspace('reports')}><Icon name="chart" size={19}/><span>{combatText.workspace}</span><span className="nav-index">07</span></button>{tabs.map(item => <button key={item.id} type="button" className={`sidebar-link ${view === 'character' && tab === item.id ? 'selected' : ''}`} aria-label={t[item.id]} title={t[item.id]} disabled={!profile && item.id !== 'overview'} onClick={() => selectTab(item.id)}><Icon name={item.icon} size={19}/><span>{t[item.id]}</span><span className="nav-index">{item.index}</span></button>)}</nav><div className="sidebar-bottom"><span className="retail-orbit"><Icon name="globe" size={20}/></span><span>WORLD OF WARCRAFT<small>RETAIL · EU / US</small></span><span className="online-dot"/></div></aside>
    <div className="page-shell"><header className="topbar"><div className="breadcrumb"><span>KIPET</span><span className="breadcrumb-slash">/</span><span>{view === 'raiderio' ? 'Raider.IO' : view === 'reports' ? combatText.workspace : view === 'guilds' ? socialText.guilds : view === 'comparison' ? socialText.comparison : t.character}</span></div><div className="topbar-actions"><span className="topbar-retail">RETAIL</span><div className="language-switch" aria-label="Language"><button className={locale === 'ru-RU' ? 'active' : ''} aria-pressed={locale === 'ru-RU'} type="button" onClick={() => setLocale('ru-RU')}>RU</button><span>/</span><button className={locale === 'en-US' ? 'active' : ''} aria-pressed={locale === 'en-US'} type="button" onClick={() => setLocale('en-US')}>EN</button></div></div></header>
      <main>
        <nav className="workspace-switch" aria-label={t.workspace}>{raiderVisible && <button type="button" className={view === 'raiderio' ? 'active' : ''} aria-current={view === 'raiderio' ? 'page' : undefined} onClick={() => selectWorkspace('raiderio')}><Icon name="globe" size={16}/>Raider.IO</button>}<button type="button" className={view === 'character' ? 'active' : ''} aria-current={view === 'character' ? 'page' : undefined} onClick={() => selectWorkspace('character')}><Icon name="shield" size={16}/>{legacyCharacterLabel}</button><button type="button" className={view === 'reports' ? 'active' : ''} aria-current={view === 'reports' ? 'page' : undefined} onClick={() => selectWorkspace('reports')}><Icon name="chart" size={16}/>{combatText.workspace}</button>{(['guilds','comparison'] as const).map(workspace=><button type="button" key={workspace} className={view===workspace?'active':''} aria-current={view===workspace?'page':undefined} onClick={()=>selectWorkspace(workspace)}><Icon name="book" size={16}/>{socialText[workspace]}</button>)}</nav>
        {socialWorkspace && runtime?.raiderio_enabled && (runtime.provider_mode === 'fixture' || !raiderSocial) && <div className="raider-source-switch">
          <a className="button-secondary" href={`?${new URLSearchParams({ view, source: raiderSocial ? 'fixture' : 'raiderio' })}`}>
            {raiderSocial ? (locale === 'ru-RU' ? 'Демоданные' : 'Demo data') : (locale === 'ru-RU' ? 'Данные Raider.IO' : 'Raider.IO data')}
            <Icon name="arrow" size={14}/>
          </a>
        </div>}
        {view === 'raiderio' ? <RaiderWorkspace runtime={runtime} locale={locale}/>
          : view === 'reports' ? <CombatWorkspace runtime={runtime} locale={locale}/>
          : view === 'guilds' ? (raiderSocial ? <RaiderGuildWorkspace runtime={runtime} locale={locale}/> : <GuildWorkspace runtime={runtime} locale={locale} onCompare={compareMembers}/>)
          : view === 'comparison' ? (raiderSocial ? <RaiderComparisonWorkspace runtime={runtime} locale={locale}/> : <ComparisonWorkspace runtime={runtime} locale={locale}/>)
          : <>
        <section className={`hero ${profile ? 'hero-compact' : ''}`}><div className="hero-copy"><div className="eyebrow"><span className="gold-line"/>{t.heroTop}</div><h1>{profile ? t.profileTitle : t.heroTitle}</h1><p>{profile ? t.profileSubtitle : t.heroDescription}</p></div><div className="hero-symbol" aria-hidden="true"><Compass/></div></section>

        <section className="search-panel panel" aria-label={t.search}><form onSubmit={search} noValidate><div className="search-region field"><label htmlFor="region">{t.region}</label><div className="select-wrap"><select id="region" value={region} onChange={event => { setRegion(event.target.value as Region); setRealm(null); setValidation(null); }}><option value="eu">Europe · EU</option><option value="us">Americas · US</option></select><Icon name="chevron" size={14}/></div></div><div className="search-realm field"><label htmlFor="realm">{t.realm}</label><RealmPicker region={region} locale={locale} selected={realm} onChange={setRealm} t={t} invalid={validation === 'realm'}/></div><div className="search-name field"><label htmlFor="character-name">{t.name}</label><div className="input-icon"><Icon name="shield" size={17}/><input id="character-name" value={name} onChange={event => { setName(event.target.value); setValidation(null); }} autoComplete="off" placeholder={t.namePlaceholder} aria-invalid={validation === 'name'} aria-describedby={validation === 'name' ? 'search-validation' : undefined}/></div></div><button type="submit" className="button-primary search-button" disabled={character.busy || cooldown > 0}><Icon name={character.busy ? 'refresh' : 'search'} size={18} className={character.busy ? 'spinning' : ''}/><span>{character.busy ? t.searching : t.search}</span><Icon name="arrow" size={16}/></button></form>{validation && <p className="validation-message" id="search-validation" role="alert">{validation === 'realm' ? t.chooseRealm : t.enterName}</p>}</section>

        <div className="under-search"><span><Icon name="shield" size={13}/>{t.publicData}</span><span className="under-search-dot">·</span><span>{t.noAccount}</span><div className="provider-label"><span className={`status-dot ${runtime ? 'connected' : ''}`}/>{sourceLabel}</div></div>
        {demo && <div className="demo-banner"><div><span className="demo-pill">{t.demoLabel}</span><p>{t.demoText}</p></div><div className="demo-actions">{(runtime?.demo_characters ?? []).map(key => <button type="button" className="demo-button" disabled={character.busy} key={`${key.region}/${key.realm}/${key.name}`} onClick={() => open(key)}>{key.name}<span>{key.region.toUpperCase()}</span><Icon name="arrow" size={13}/></button>)}</div></div>}

        {character.error && <div className="error-banner" role="alert"><Icon name="info" size={20}/><div><strong>{errorText(character.error)}</strong>{cooldown > 0 && <p>{t.retryIn} {cooldown} {t.seconds}</p>}</div><button type="button" aria-label={t.dismiss} onClick={character.dismissError}><Icon name="close" size={17}/></button></div>}
        {(character.busy || character.paused) && <div className="progress-banner" role="status" aria-live="polite"><span className={`progress-symbol ${character.busy ? 'spinning' : ''}`}><Icon name={character.busy ? 'refresh' : 'clock'} size={19}/></span><div><strong>{character.paused ? t.timeout : character.job && ['queued', 'running', 'retry_wait'].includes(character.job.status) ? t[character.job.status as 'queued' | 'running' | 'retry_wait'] : t.pending}</strong>{!profile && character.busy && <p>{t.pendingText}</p>}</div>{character.paused && <button type="button" className="button-secondary" onClick={() => void character.check()}>{t.checkStatus}</button>}</div>}

        {profile ? <>
          <section className="character-header panel" style={{ '--class-color': classColors[profile.character_class.id] ?? '#bca779' } as React.CSSProperties}><div className="character-emblem" aria-hidden="true"><span className="emblem-ring"/><Icon name={profile.character_class.id === '8' || profile.character_class.id === '9' ? 'spark' : 'sword'} size={36}/><span className="emblem-diamond"/></div><div className="character-heading"><div className="character-subline">{profile.region.toUpperCase()}<span>·</span>{profile.realm.name}{demo && <span className="synthetic-tag">{t.demoNotReal}</span>}</div><h2>{profile.name}</h2><div className="character-description"><span className="class-name">{profile.character_class.name ?? `#${profile.character_class.id}`}</span><span>·</span><span>{profile.active_specialization?.name ?? t.notProvided}</span><span>·</span><span>{profile.level} {t.level}</span></div></div><div className="character-actions"><span className={`badge ${profileMeta?.status === 'stale' ? 'amber' : 'teal'}`}><span className="status-dot"/>{profileMeta?.status === 'stale' ? t.stale : t.fresh}</span><button type="button" className="button-secondary" onClick={() => void character.refresh()} disabled={character.busy || cooldown > 0}><Icon name="refresh" size={15} className={character.busy ? 'spinning' : ''}/>{character.busy ? t.refreshing : t.refresh}</button></div></section>
          <div className="profile-tabs" role="tablist" aria-label={t.character}>{tabs.map(item => <button type="button" key={item.id} role="tab" id={`tab-${item.id}`} aria-controls={`panel-${item.id}`} aria-selected={tab === item.id} tabIndex={tab === item.id ? 0 : -1} className={tab === item.id ? 'active' : ''} onClick={() => selectTab(item.id)} onKeyDown={event => { if (['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) { event.preventDefault(); const position = tabs.findIndex(candidate => candidate.id === item.id); const next = tabs[event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (position + (event.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length].id; selectTab(next); document.getElementById(`tab-${next}`)?.focus(); } }}><Icon name={item.icon} size={16}/><span>{t[item.id]}</span></button>)}<div className="tabs-end"><CoverageBadge coverage={selectedCoverage} t={t}/></div></div>
          <div id={`panel-${tab}`} role="tabpanel" aria-labelledby={`tab-${tab}`}>
            {tab === 'overview' && <><div className="metrics-grid"><Metric label={t.equippedLevel} value={number(profile.equipped_item_level, locale)} note={t.equipment} icon="sword" accent/><Metric label={t.averageLevel} value={number(profile.average_item_level, locale)} note={sourceLabel} icon="shield"/><Metric label={t.availableSections} value={`${sectionNames.filter(section => { const state = sectionMeta(section)?.coverage[section]?.state; return state && state !== 'unavailable'; }).length} / ${sectionNames.length}`} note={t.publicData} icon="book"/></div>
              <div className="overview-grid"><section className="panel transparency-panel"><div className="panel-heading"><div><span className="eyebrow">{t.source}</span><h2>{t.dataTitle}</h2><p>{t.dataDescription}</p></div><Icon name="clock" size={25}/></div><div className="coverage-list">{sectionNames.map(section => { const meta = sectionMeta(section); return <div className="coverage-row" key={section}><div><Icon name={section === 'profile' ? 'shield' : tabs.find(item => item.id === section)?.icon ?? 'book'} size={18}/><span>{section === 'profile' ? t.character : t[section]}</span></div><div><span className="coverage-date">{formatDate(meta?.coverage[section]?.fetched_at, locale)}</span><CoverageBadge coverage={meta?.coverage[section]} t={t}/></div></div>; })}</div><p className="source-footnote"><Icon name="info" size={14}/>{demo ? t.sourceFixtureNote : t.sourcesNote}</p></section><aside className="profile-facts panel"><span className="eyebrow">{t.overview}</span><h3>{t.character}</h3><dl><div><dt>{t.faction}</dt><dd>{profile.faction === 'alliance' ? t.alliance : profile.faction === 'horde' ? t.horde : profile.faction === 'neutral' ? t.neutral : t.unknown}</dd></div><div><dt>{t.lastLogin}</dt><dd>{formatDate(profile.source_last_login_at, locale)}</dd></div><div><dt>{t.patch}</dt><dd>{profileMeta?.patch ?? t.unknown}</dd></div><div><dt>{t.season}</dt><dd>{profileMeta?.season ?? t.unknown}</dd></div></dl><div className="facts-decoration" aria-hidden="true"><Compass/></div></aside></div><div className="recommendation-note"><Icon name="spark" size={20}/><div><strong>{t.recommendationsTitle}</strong><p>{t.recommendationsText}</p></div></div></>}
            {tab === 'equipment' && <EquipmentPanel result={character.sections.equipment} t={t} locale={locale} loading={character.busy} error={!!character.sectionErrors.equipment}/>}
            {tab === 'statistics' && <StatisticsPanel result={character.sections.statistics} t={t} locale={locale} loading={character.busy} error={!!character.sectionErrors.statistics}/>}
            {tab === 'talents' && <TalentsPanel result={character.sections.talents} t={t} locale={locale} loading={character.busy} error={!!character.sectionErrors.talents}/>}
            {(tab === 'mythic_plus' || tab === 'raids') && <ProgressPanel section={tab} result={progress.result} loading={progress.loading} error={progress.error} outdated={progress.outdated} paused={progress.paused} locale={locale} t={t} onRetry={progress.reload} onReloadProfile={() => { if (character.key) void character.load(character.key); }}/> }
            {tab === 'analysis' && <AnalysisPanel result={analysis.result} loading={analysis.loading} error={analysis.error} outdated={analysis.outdated} paused={analysis.paused} content={analysisContent} locale={locale} t={t} onContent={selectContent} onRetry={analysis.reload} onReloadProfile={() => { if (character.key) void character.load(character.key); }}/>}
          </div>
        </> : !character.busy && <section className="empty-state"><div className="empty-orbit" aria-hidden="true"><span/><BrandMark/></div><div className="eyebrow">CHARACTER INTELLIGENCE</div><h2>{t.noProfileTitle}</h2><p>{t.noProfileText}</p><div className="feature-grid">{[{ icon: 'sword', title: t.featureEquipment, text: t.featureEquipmentText }, { icon: 'chart', title: t.featureStats, text: t.featureStatsText }, { icon: 'talents', title: t.featureTalents, text: t.featureTalentsText }].map((feature, index) => <div className="feature-card" key={feature.icon}><div className="feature-top"><span className="feature-icon"><Icon name={feature.icon} size={22}/></span><span className="feature-index">0{index + 1}</span></div><h3>{feature.title}</h3><p>{feature.text}</p></div>)}</div></section>}
        </>}
      </main><footer><div><span className="footer-brand">KIPET<span>.</span></span><span>{t.footer}</span></div><span>{t.footerNote}</span></footer>
    </div>
  </div>;
}

function Metric({ label, value, note, icon, accent }: { label: string; value: string; note: string; icon: string; accent?: boolean }) { return <article className={`metric panel ${accent ? 'accent' : ''}`}><div className="metric-label">{label}<Icon name={icon} size={19}/></div><strong>{value}</strong><span>{note}</span></article>; }
function BrandMark() { return <svg width="42" height="47" viewBox="0 0 42 47" fill="none" aria-hidden="true"><path d="M21 2 39 12v23L21 45 3 35V12Z" stroke="currentColor" strokeWidth="1.3"/><path d="M14 13v22m15-22L16 25l14 10M16 25h8" stroke="currentColor" strokeWidth="2"/><path d="m21 6 14 8v19l-14 8" stroke="currentColor" strokeOpacity=".2"/></svg>; }
function Compass() { return <svg viewBox="0 0 230 230" fill="none" aria-hidden="true"><circle cx="115" cy="115" r="94"/><circle cx="115" cy="115" r="76" strokeDasharray="1 8"/><circle cx="115" cy="115" r="58"/><path d="M115 5v35m0 150v35M5 115h35m150 0h35M37 37l25 25m106 106 25 25M37 193l25-25M168 62l25-25"/><path d="m115 35 18 62 62 18-62 18-18 62-18-62-62-18 62-18Z"/><path d="m115 62 10 43 43 10-43 10-10 43-10-43-43-10 43-10Z"/><circle cx="115" cy="115" r="9"/><path d="m70 31 8 18m74 132 8 18M31 70l18 8m132 74 18 8M31 160l18-8m132-74 18-8M70 199l8-18m74-132 8-18" strokeOpacity=".5"/></svg>; }

