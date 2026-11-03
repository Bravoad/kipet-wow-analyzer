import { useEffect, useState } from 'react';
import { dictionary, number } from '../i18n.ts';
import type { Dictionary } from '../i18n.ts';
import { useRaiderProfile } from '../features/useRaiderProfile.ts';
import type { ApiError } from '../shared/api.ts';
import { formatProgressDate, formatRunDuration } from '../shared/progress.ts';
import { raiderHref, raiderKeyFromURL, raiderName, raiderRealm, raiderSourceLink } from '../shared/raider.ts';
import type { RaiderCoverage, RaiderItem, RaiderProfile, RaiderSection } from '../shared/raider.ts';
import type { CharacterKey, Envelope, Locale, Region, Runtime } from '../shared/types.ts';
import { Icon } from './Icon.tsx';

const ru = {
  title: 'Профиль Raider.IO',
  subtitle: 'Экипировка, таланты и прогресс публичного персонажа по данным Raider.IO.',
  connecting: 'Проверяем подключение Raider.IO…',
  disabled: 'Источник Raider.IO не подключён',
  realm: 'Название или slug сервера',
  enterRealm: 'Введите название или slug сервера длиной до 80 символов.',
  openSource: 'Профиль на Raider.IO',
  crawled: 'Обновлено на Raider.IO',
  fetched: 'Получено приложением',
  cached: 'Сохранённые данные',
  rating: 'Рейтинг Raider.IO',
  role: 'Роль',
  race: 'Раса',
  notProvided: 'Raider.IO не предоставил эти данные.',
  noStats: 'Raider.IO не предоставил боевые характеристики для этого профиля.',
  reportStats: 'Посмотреть характеристики в боевом отчёте',
  noTalentCode: 'Строка экспорта талантов не предоставлена.',
  specializationID: 'ID специализации',
  expansionID: 'ID дополнения',
  runLink: 'Прохождение на Raider.IO',
  parTime: 'Лимит времени',
  enchantIDs: 'ID зачарований',
};
const en: typeof ru = {
  title: 'Raider.IO profile',
  subtitle: 'Equipment, talents and progress for a public character from Raider.IO.',
  connecting: 'Checking the Raider.IO connection…',
  disabled: 'Raider.IO is not connected',
  realm: 'Realm name or slug',
  enterRealm: 'Enter a realm name or slug up to 80 characters long.',
  openSource: 'Profile on Raider.IO',
  crawled: 'Updated on Raider.IO',
  fetched: 'Fetched by the app',
  cached: 'Cached data',
  rating: 'Raider.IO rating',
  role: 'Role',
  race: 'Race',
  notProvided: 'Raider.IO did not provide this data.',
  noStats: 'Raider.IO did not provide combat statistics for this profile.',
  reportStats: 'View stats in a combat report',
  noTalentCode: 'The talent export string was not provided.',
  specializationID: 'Specialization ID',
  expansionID: 'Expansion ID',
  runLink: 'Run on Raider.IO',
  parTime: 'Time limit',
  enchantIDs: 'Enchantment IDs',
};

const sections = [
  { id: 'overview', icon: 'shield' },
  { id: 'equipment', icon: 'sword' },
  { id: 'statistics', icon: 'chart' },
  { id: 'talents', icon: 'talents' },
  { id: 'mythic_plus', icon: 'clock' },
  { id: 'raids', icon: 'book' },
] as const;
type Section = typeof sections[number]['id'];

function errorMessage(error: ApiError, t: Dictionary, copy: typeof ru) {
  if (error.status === 0) return t.networkError;
  if (error.detail.code === 'RAIDERIO_NOT_CONFIGURED') return copy.disabled;
  if (error.detail.code === 'CHARACTER_UNAVAILABLE') return t.notFound;
  if (error.detail.code === 'PROVIDER_ACCESS_DENIED') return t.accessDenied;
  if (error.status === 429 || error.detail.code === 'PROVIDER_RATE_LIMITED') return t.rateLimited;
  if (error.status === 400) return t.invalidArgument;
  if (error.detail.code === 'CANCELLED') return t.jobCancelled;
  return t.providerError;
}

export function RaiderWorkspace({ runtime, locale }: { runtime: Runtime | null; locale: Locale }) {
  const t = dictionary(locale);
  const copy = locale === 'ru-RU' ? ru : en;
  const [key, setKey] = useState(() => raiderKeyFromURL(location.search));
  const [region, setRegion] = useState<Region>(key?.region ?? 'eu');
  const [realm, setRealm] = useState(key?.realm ?? '');
  const [name, setName] = useState(key?.name ?? '');
  const [invalid, setInvalid] = useState<'realm' | 'name' | null>(null);
  const [section, setSection] = useState<Section>('overview');
  const [cooldown, setCooldown] = useState(0);
  const enabled = runtime?.raiderio_enabled === true;
  const resource = useRaiderProfile(enabled ? key : null, locale);

  useEffect(() => {
    const pop = () => {
      const next = raiderKeyFromURL(location.search);
      setKey(next);
      setRegion(next?.region ?? 'eu');
      setRealm(next?.realm ?? '');
      setName(next?.name ?? '');
      setSection('overview');
      setInvalid(null);
    };
    window.addEventListener('popstate', pop);
    return () => window.removeEventListener('popstate', pop);
  }, []);

  useEffect(() => {
    const seconds = resource.error?.detail.retry_after_seconds ?? 0;
    setCooldown(seconds);
    if (seconds <= 0) return;
    const until = Date.now() + seconds * 1000;
    const timer = setInterval(() => {
      const remaining = Math.max(0, Math.ceil((until - Date.now()) / 1000));
      setCooldown(remaining);
      if (!remaining) clearInterval(timer);
    }, 1000);
    return () => clearInterval(timer);
  }, [resource.error]);

  function search(event: React.SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!enabled || resource.busy || cooldown > 0) return;
    const cleanRealm = raiderRealm(realm);
    const cleanName = raiderName(name);
    if (!cleanRealm || !cleanName) {
      setInvalid(cleanRealm ? 'name' : 'realm');
      return;
    }
    const next: CharacterKey = { region, realm: cleanRealm, name: cleanName };
    setInvalid(null);
    setSection('overview');
    if (key?.region === region && key.realm === cleanRealm && key.name === cleanName) {
      resource.reload();
      return;
    }
    setKey(next);
    history.pushState(null, '', `?${new URLSearchParams({ view: 'raiderio', ...next })}`);
  }

  const profile = resource.profile;
  const sourceLink = profile && raiderSourceLink(profile.data.profile_url, 'characters');
  return <div className="raider-workspace">
    <section className="hero hero-compact">
      <div className="hero-copy">
        <div className="eyebrow"><span className="gold-line"/>RAIDER.IO · RETAIL</div>
        <h1>{copy.title}</h1>
        <p>{copy.subtitle}</p>
      </div>
    </section>

    <section className="search-panel panel" aria-label={t.search}>
      <form onSubmit={search} noValidate>
        <div className="search-region field">
          <label htmlFor="raider-region">{t.region}</label>
          <div className="select-wrap">
            <select id="raider-region" value={region} onChange={event => setRegion(event.target.value as Region)}>
              <option value="eu">Europe · EU</option>
              <option value="us">Americas · US</option>
            </select>
            <Icon name="chevron" size={14}/>
          </div>
        </div>
        <div className="search-realm field">
          <label htmlFor="raider-realm">{copy.realm}</label>
          <input id="raider-realm" value={realm} onChange={event => { setRealm(event.target.value); setInvalid(null); }} placeholder="gordunni" autoComplete="off" spellCheck={false} aria-invalid={invalid === 'realm'} aria-describedby={invalid === 'realm' ? 'raider-validation' : undefined}/>
        </div>
        <div className="search-name field">
          <label htmlFor="raider-name">{t.name}</label>
          <input id="raider-name" value={name} onChange={event => { setName(event.target.value); setInvalid(null); }} autoComplete="off" spellCheck={false} aria-invalid={invalid === 'name'} aria-describedby={invalid === 'name' ? 'raider-validation' : undefined}/>
        </div>
        <button type="submit" className="button-primary search-button" disabled={!enabled || resource.busy || cooldown > 0}>
          <Icon name="search" size={18}/>{t.search}
        </button>
      </form>
      {invalid && <p className="validation-message" id="raider-validation" role="alert">{invalid === 'realm' ? copy.enterRealm : t.enterName}</p>}
    </section>

    {!enabled && <section className="panel combat-state" role="status"><Icon name="info" size={22}/><h2>{runtime ? copy.disabled : copy.connecting}</h2></section>}
    {resource.error && <div className="error-banner" role="alert">
      <Icon name="info" size={20}/>
      <div><strong>{errorMessage(resource.error, t, copy)}</strong>{cooldown > 0 && <p>{t.retryIn} {cooldown} {t.seconds}</p>}</div>
      {resource.error.detail.retryable && <button type="button" className="button-secondary" onClick={resource.reload} disabled={cooldown > 0}>{t.progressRetry}</button>}
    </div>}
    {(resource.busy || resource.paused) && <div className="progress-banner" role="status" aria-live="polite">
      <Icon name={resource.paused ? 'clock' : 'refresh'} className={resource.busy ? 'spinning' : ''} size={19}/>
      <div><strong>{resource.paused ? t.timeout : t.pending}</strong></div>
      {resource.paused && <button type="button" className="button-secondary" onClick={resource.reload}>{t.checkStatus}</button>}
    </div>}

    {profile && <>
      <section className="panel character-header raider-profile-header">
        <div className="character-emblem" aria-hidden="true"><span className="emblem-ring"/><Icon name="shield" size={36}/><span className="emblem-diamond"/></div>
        <div className="character-heading">
          <div className="character-subline">{profile.data.region.toUpperCase()}<span>·</span>{profile.data.realm.name}</div>
          <h2>{profile.data.name}</h2>
          <div className="character-description">{[profile.data.class, profile.data.specialization].filter(Boolean).join(' · ') || t.notProvided}</div>
          {profile.data.guild && <a className="raider-run-link" href={raiderHref({ region: profile.data.region, ...profile.data.guild }, 'guilds')}>
            <Icon name="book" size={15}/>{profile.data.guild.name}
          </a>}
        </div>
        <div className="character-actions">
          {sourceLink && <a className="button-secondary" href={sourceLink} target="_blank" rel="noreferrer">{copy.openSource}<Icon name="arrow" size={15}/></a>}
          <button type="button" className="button-secondary" disabled={resource.busy || cooldown > 0} onClick={resource.refresh}><Icon name="refresh" size={15}/>{t.refresh}</button>
        </div>
      </section>
      <div className="profile-tabs" role="tablist" aria-label={copy.title}>
        {sections.map((item, index) => <button type="button" key={item.id} role="tab" id={`raider-tab-${item.id}`} aria-controls="raider-section" aria-selected={item.id === section} tabIndex={item.id === section ? 0 : -1} className={item.id === section ? 'active' : ''} onClick={() => setSection(item.id)} onKeyDown={event => {
          if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
          event.preventDefault();
          const position = event.key === 'Home' ? 0 : event.key === 'End' ? sections.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : sections.length - 1)) % sections.length;
          const next = sections[position].id;
          setSection(next);
          document.getElementById(`raider-tab-${next}`)?.focus();
        }}><Icon name={item.icon} size={16}/>{t[item.id]}</button>)}
      </div>
      <div id="raider-section" role="tabpanel" aria-labelledby={`raider-tab-${section}`}>
        <RaiderDetails section={section} profile={profile} locale={locale} t={t} copy={copy}/>
      </div>
    </>}
    {enabled && !key && <section className="panel combat-empty"><Icon name="search" size={30}/><h2>{t.search}</h2><p>{copy.subtitle}</p></section>}
  </div>;
}

type DetailsProps = { profile: Envelope<RaiderProfile>; locale: Locale; t: Dictionary; copy: typeof ru };

function RaiderDetails({ section, profile, locale, t, copy }: DetailsProps & { section: Section }) {
  const data = profile.data;
  if (section === 'overview') {
    const facts = [
      [copy.race, data.race],
      [copy.role, data.role],
      [t.faction, data.faction === 'alliance' ? t.alliance : data.faction === 'horde' ? t.horde : data.faction === 'neutral' ? t.neutral : data.faction],
      [copy.crawled, formatProgressDate(data.last_crawled_at, locale)],
      [copy.fetched, formatProgressDate(profile.meta.fetched_at, locale)],
    ];
    return <>
      <div className="metrics-grid raider-metrics">
        <article className="metric panel accent"><div className="metric-label">{t.equippedLevel}<Icon name="sword" size={19}/></div><strong>{data.gear?.equipped_item_level == null ? '—' : new Intl.NumberFormat(locale, { maximumFractionDigits: 3 }).format(data.gear.equipped_item_level)}</strong></article>
        <article className="metric panel"><div className="metric-label">{copy.rating}<Icon name="chart" size={19}/></div><strong>{number(data.mythic_plus?.rating, locale)}</strong></article>
        <article className="metric panel"><div className="metric-label">{t.season}<Icon name="clock" size={19}/></div><strong className="raider-season">{data.mythic_plus?.season ?? t.unknown}</strong></article>
      </div>
      <div className="overview-grid">
        <section className="panel transparency-panel">
          <div className="panel-heading"><div><span className="eyebrow">{t.source}</span><h2>Raider.IO</h2></div><Icon name="globe" size={25}/></div>
          <div className="coverage-list">{([
            ['profile', t.character, 'shield'], ['gear', t.equipment, 'sword'], ['talents', t.talents, 'talents'], ['mythic_plus', t.mythic_plus, 'clock'], ['raids', t.raids, 'book'],
          ] as const).map(([field, label, icon]) => <div className="coverage-row" key={field}>
            <div><Icon name={icon} size={18}/><span>{label}</span></div><div><RaiderBadge coverage={data.coverage[field]} t={t}/></div>
          </div>)}</div>
          {profile.meta.status === 'stale' && <p className="source-footnote"><Icon name="clock" size={14}/>{copy.cached}</p>}
        </section>
        <aside className="profile-facts panel"><h3>{t.character}</h3><dl>{facts.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value ?? t.notProvided}</dd></div>)}</dl></aside>
      </div>
    </>;
  }

  const coverageKey: RaiderSection | null = section === 'equipment' ? 'gear' : section === 'statistics' ? null : section;
  const coverage = coverageKey ? data.coverage[coverageKey] : undefined;
  return <section className="panel detail-panel">
    <div className="panel-heading"><div><span className="eyebrow">RAIDER.IO</span><h2>{t[section]}</h2></div><RaiderBadge coverage={coverage} t={t}/></div>
    {section === 'statistics' && <>
      <p className="empty-copy">{copy.noStats}</p>
      <a className="raider-run-link" href="?view=reports">{copy.reportStats}<Icon name="arrow" size={13}/></a>
    </>}
    {section === 'equipment' && <>
      {data.gear?.updated_at && <p className="progress-note">{copy.crawled} {formatProgressDate(data.gear.updated_at, locale)}</p>}
      <RaiderGear items={data.gear?.items ?? null} locale={locale} t={t} copy={copy}/>
    </>}
    {section === 'talents' && <RaiderTalents key={data.talents?.export_code ?? 'empty'} data={data.talents} t={t} copy={copy}/>}
    {section === 'mythic_plus' && <RaiderMythic data={data.mythic_plus} locale={locale} t={t} copy={copy}/>}
    {section === 'raids' && <RaiderRaids data={data.raids} locale={locale}/>}
  </section>;
}

function RaiderBadge({ coverage, t }: { coverage?: RaiderCoverage; t: Dictionary }) {
  const state = coverage?.state ?? 'unavailable';
  return <span className={`badge ${state === 'complete' ? 'teal' : state === 'partial' ? 'amber' : 'muted'}`}>{t[state]}</span>;
}

const slots: Record<string, keyof Dictionary> = { finger1: 'finger_1', finger2: 'finger_2', trinket1: 'trinket_1', trinket2: 'trinket_2', mainhand: 'main_hand', offhand: 'off_hand' };
const qualities: Record<number, string> = { 2: 'uncommon', 3: 'rare', 4: 'epic', 5: 'legendary', 6: 'artifact', 7: 'heirloom' };

function RaiderGear({ items, locale, t, copy }: { items: RaiderItem[] | null; locale: Locale; t: Dictionary; copy: typeof ru }) {
  if (items === null) return <p className="empty-copy">{copy.notProvided}</p>;
  if (!items.length) return <p className="empty-copy">{t.noItems}</p>;
  return <div className="equipment-list">
    <div className="equipment-head"><span>{t.slot}</span><span>{t.item}</span><span>{t.itemLevel}</span></div>
    {items.map(item => <div className={`equipment-row quality-${qualities[item.quality ?? 0] ?? 'common'}`} key={item.slot}>
      <div className="slot-label">{t[slots[item.slot] ?? (item.slot as keyof Dictionary)] ?? item.slot}</div>
      <div className="item-info">
        <span className="item-symbol"><Icon name="shield" size={21}/></span>
        <div><div className="item-name">{item.name ?? `#${item.item_id}`}</div>
          <div className="item-annotations">
            <span>{t.enchantments} · {item.enchantments === null ? t.notProvided : item.enchantments.length ? item.enchantments.map(enchant => enchant.name ?? `#${enchant.id}`).join(', ') : t.absent}</span>
            {!!item.enchant_ids?.length && <span>{copy.enchantIDs} · {item.enchant_ids.join(', ')}</span>}
            <span>{t.sockets} · {item.gem_ids === null ? t.notProvided : item.gem_ids.length ? item.gem_ids.map(id => `#${id}`).join(', ') : t.absent}</span>
          </div>
        </div>
      </div>
      <div className="item-level">{number(item.item_level, locale)}</div>
    </div>)}
  </div>;
}

function RaiderTalents({ data, t, copy }: { data: RaiderProfile['talents']; t: Dictionary; copy: typeof ru }) {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const code = data?.export_code;
  async function copyCode() {
    if (!code) return;
    try { await navigator.clipboard.writeText(code); setCopyState('copied'); } catch { setCopyState('failed'); }
  }
  if (!data) return <p className="empty-copy">{copy.notProvided}</p>;
  return <>
    {data.specialization_id && <p className="progress-note">{copy.specializationID} · #{data.specialization_id}</p>}
    {code ? <div className="export-code">
      <div><label htmlFor="raider-talent-code">{t.exportCode}</label><button type="button" className="text-button" onClick={() => void copyCode()}><Icon name={copyState === 'copied' ? 'check' : 'copy'} size={14}/>{copyState === 'copied' ? t.copied : t.copy}</button></div>
      <textarea id="raider-talent-code" readOnly value={code} rows={2}/>
      {copyState === 'failed' && <p role="status">{t.copyFailed}</p>}
    </div> : <p className="empty-copy">{copy.noTalentCode}</p>}
  </>;
}

function RaiderMythic({ data, locale, t, copy }: { data: RaiderProfile['mythic_plus']; locale: Locale; t: Dictionary; copy: typeof ru }) {
  if (!data) return <p className="empty-copy">{copy.notProvided}</p>;
  return <>
    <p className="progress-note">{copy.rating} · {number(data.rating, locale)} · {t.season} {data.season ?? t.unknown}</p>
    <div className="progress-list-heading"><h3>{t.progressRuns}</h3></div>
    {data.best_runs === null ? <p className="empty-copy">{t.progressRunsNotProvided}</p> : !data.best_runs.length ? <p className="empty-copy">{t.progressNoRuns}</p> : <div className="progress-runs">
      {data.best_runs.map((run, index) => {
        const sourceLink = raiderSourceLink(run.url, 'mythic-plus-runs');
        return <article className="progress-run" key={`${run.dungeon}-${run.completed_at ?? ''}-${index}`}>
          <div className="progress-run-title"><span className="progress-key">+{run.level}</span><h4>{run.dungeon}</h4>
            <span className={`badge ${run.timed === null ? 'muted' : run.timed ? 'teal' : 'amber'}`}>{run.timed === null ? t.progressTimeUnknown : run.timed ? t.progressTimed : t.progressOvertime}</span>
          </div>
          <dl className="progress-run-facts">
            <div><dt>{t.progressDuration} / {copy.parTime}</dt><dd>{formatRunDuration(run.duration_ms)} / {formatRunDuration(run.par_time_ms)}</dd></div>
            <div><dt>{t.progressRunRating}</dt><dd>{number(run.score, locale)}</dd></div>
            <div><dt>{t.progressCompleted}</dt><dd>{formatProgressDate(run.completed_at, locale)}</dd></div>
          </dl>
          {sourceLink && <a className="raider-run-link" href={sourceLink} target="_blank" rel="noreferrer">{copy.runLink}<Icon name="arrow" size={13}/></a>}
        </article>;
      })}
    </div>}
  </>;
}

export function RaiderRaids({ data, locale }: { data: RaiderProfile['raids']; locale: Locale }) {
  const t = dictionary(locale);
  const copy = locale === 'ru-RU' ? ru : en;
  if (data === null) return <p className="empty-copy">{t.progressRaidsNotProvided}</p>;
  if (!data.length) return <p className="empty-copy">{t.progressNoRaids}</p>;
  return <div className="raid-expansions">{data.map(raid => <article className="raid-instance" key={raid.slug}>
    <h4><Icon name="shield" size={18}/>{raid.slug.replaceAll('-', ' ')}</h4>
    <p className="progress-note">{[raid.summary, raid.expansion_id && `${copy.expansionID} #${raid.expansion_id}`].filter(Boolean).join(' · ')}</p>
    <dl className="progress-run-facts">{([
      [t.progressNormal, raid.normal_bosses_killed], [t.progressHeroic, raid.heroic_bosses_killed], [t.progressMythic, raid.mythic_bosses_killed],
    ] as const).map(([label, kills]) => <div key={label}><dt>{label}</dt><dd>{number(kills, locale)} / {number(raid.total_bosses, locale)} {t.progressBosses}</dd></div>)}</dl>
  </article>)}</div>;
}
