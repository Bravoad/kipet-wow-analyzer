import { useEffect, useState } from 'react';
import { useRaiderGuild, useRaiderProfile } from '../features/useRaiderProfile.ts';
import { dictionary, formatDate, number } from '../i18n.ts';
import type { ApiError } from '../shared/api.ts';
import { raiderComparisonFromURL, raiderComparisonHref, raiderComparisonKeys, raiderGuildName, raiderHref, raiderKeyFromURL, raiderRealm, raiderSourceLink } from '../shared/raider.ts';
import type { RaiderGuildProfile } from '../shared/raider.ts';
import { characterKeyID } from '../shared/social.ts';
import { observationStatus, socialMessages } from '../shared/socialMessages.ts';
import type { CharacterKey, Locale, Region, Runtime } from '../shared/types.ts';
import { Icon } from './Icon.tsx';
import { RaiderComparisonSummary } from './RaiderComparisonSummary.tsx';
import { RaiderDungeonComparison } from './RaiderDungeonComparison.tsx';
import { RaiderGuildSummary } from './RaiderGuildSummary.tsx';
import { RaiderRaids } from './RaiderWorkspace.tsx';

const copyByLocale = {
  'ru-RU': {
    guildTitle: 'Гильдии Raider.IO',
    guildIntro: 'Публичный состав, рейдовый прогресс и места гильдии по данным Raider.IO.',
    guildName: 'Название гильдии',
    realm: 'Название или slug сервера',
    invalidGuild: 'Укажите сервер и название гильдии.',
    memberSearch: 'Поиск по имени',
    roleFilter: 'Фильтр по роли',
    role: 'Роль',
    previous: 'Назад',
    next: 'Далее',
    partialRoster: 'Raider.IO предоставил часть состава.',
    openGuildSource: 'Гильдия на Raider.IO',
    ranking: 'Рейдовые места',
    difficulty: 'Сложность',
    world: 'Мир',
    compareTitle: 'Сравнение Raider.IO',
    compareIntro: 'До пяти публичных профилей. Рейтинг Mythic+ показан с сезоном каждого персонажа.',
    openProfile: 'Открыть профиль',
    disabled: 'Источник Raider.IO не подключён',
    connecting: 'Проверяем подключение Raider.IO…',
  },
  'en-US': {
    guildTitle: 'Raider.IO guilds',
    guildIntro: 'Public guild rosters, raid progression and rankings from Raider.IO.',
    guildName: 'Guild name',
    realm: 'Realm name or slug',
    invalidGuild: 'Enter a realm and guild name.',
    memberSearch: 'Search by name',
    roleFilter: 'Role filter',
    role: 'Role',
    previous: 'Previous',
    next: 'Next',
    partialRoster: 'Raider.IO provided part of the roster.',
    openGuildSource: 'Guild on Raider.IO',
    ranking: 'Raid rankings',
    difficulty: 'Difficulty',
    world: 'World',
    compareTitle: 'Raider.IO comparison',
    compareIntro: 'Up to five public profiles. Mythic+ ratings include each character’s season.',
    openProfile: 'Open profile',
    disabled: 'Raider.IO is not connected',
    connecting: 'Checking the Raider.IO connection…',
  },
};

const emptyKey: CharacterKey = { region: 'eu', realm: '', name: '' };
const pageSize = 25;
type Resource = Pick<ReturnType<typeof useRaiderGuild>, 'busy' | 'paused' | 'error' | 'reload'>;
type WorkspaceProps = { runtime: Runtime | null; locale: Locale };

function RaiderHero({ title, intro }: { title: string; intro: string }) {
  return <section className="hero hero-compact"><div className="hero-copy">
    <div className="eyebrow"><span className="gold-line"/>RAIDER.IO · RETAIL</div>
    <h1>{title}</h1><p>{intro}</p>
  </div></section>;
}

function RaiderIdentity({ id, value, locale, onChange, guild = false, index }: {
  id: string; value: CharacterKey; locale: Locale; onChange: (key: CharacterKey) => void; guild?: boolean; index?: number;
}) {
  const t = dictionary(locale);
  const copy = copyByLocale[locale];
  const suffix = index === undefined ? '' : ` · ${index + 1}`;
  return <>
    <div className="field">
      <label htmlFor={`${id}-region`}>{t.region}{suffix}</label>
      <select id={`${id}-region`} value={value.region} onChange={event => onChange({ ...value, region: event.target.value as Region })}>
        <option value="eu">Europe · EU</option><option value="us">Americas · US</option>
      </select>
    </div>
    <div className="field">
      <label htmlFor={`${id}-realm`}>{copy.realm}{suffix}</label>
      <input id={`${id}-realm`} value={value.realm} onChange={event => onChange({ ...value, realm: event.target.value })} placeholder="gordunni" autoComplete="off" spellCheck={false}/>
    </div>
    <div className="field">
      <label htmlFor={`${id}-name`}>{guild ? copy.guildName : t.name}{suffix}</label>
      <input id={`${id}-name`} value={value.name} onChange={event => onChange({ ...value, name: event.target.value })} autoComplete="off" spellCheck={false}/>
    </div>
  </>;
}

function resourceMessage(error: ApiError, locale: Locale) {
  const t = dictionary(locale);
  if (error.detail.code === 'GUILD_UNAVAILABLE') return socialMessages(locale).notFound;
  if (error.detail.code === 'CHARACTER_UNAVAILABLE') return t.notFound;
  if (error.status === 0) return t.networkError;
  if (error.status === 400) return t.invalidArgument;
  if (error.status === 429 || error.detail.code === 'PROVIDER_RATE_LIMITED') return t.rateLimited;
  if (error.detail.code === 'PROVIDER_ACCESS_DENIED') return t.accessDenied;
  if (error.detail.code === 'CANCELLED') return t.jobCancelled;
  return t.providerError;
}

function RaiderStatus({ resource, locale }: { resource: Resource; locale: Locale }) {
  const t = dictionary(locale);
  const retryAfter = resource.error?.detail.retry_after_seconds;
  const [remaining, setRemaining] = useState(0);
  useEffect(() => {
    const seconds = retryAfter ?? 0;
    setRemaining(seconds);
    if (!seconds) return;
    const until = Date.now() + seconds * 1000;
    const timer = setInterval(() => {
      const next = Math.max(0, Math.ceil((until - Date.now()) / 1000));
      setRemaining(next);
      if (!next) clearInterval(timer);
    }, 1000);
    return () => clearInterval(timer);
  }, [resource.error, retryAfter]);

  if (resource.error) return <div className="error-banner" role="alert">
    <Icon name="info" size={18}/>
    <div><strong>{resourceMessage(resource.error, locale)}</strong>{remaining > 0 && <p>{t.retryIn} {remaining} {t.seconds}</p>}</div>
    {resource.error.detail.retryable && <button className="button-secondary" type="button" onClick={resource.reload} disabled={remaining > 0}>{t.progressRetry}</button>}
  </div>;
  if (resource.busy || resource.paused) return <div className="progress-banner" role="status">
    <Icon name={resource.busy ? 'refresh' : 'clock'} className={resource.busy ? 'spinning' : undefined} size={18}/>
    <strong>{resource.paused ? t.timeout : t.pending}</strong>
    {resource.paused && <button className="button-secondary" type="button" onClick={resource.reload}>{t.checkStatus}</button>}
  </div>;
  return null;
}

function RaiderConnection({ runtime, locale }: WorkspaceProps) {
  const copy = copyByLocale[locale];
  if (runtime?.raiderio_enabled) return null;
  return <section className="panel combat-state" role="status"><Icon name="info" size={22}/><h2>{runtime ? copy.disabled : copy.connecting}</h2></section>;
}

export function RaiderGuildWorkspace({ runtime, locale }: WorkspaceProps) {
  const t = dictionary(locale);
  const social = socialMessages(locale);
  const copy = copyByLocale[locale];
  const [key, setKey] = useState(() => raiderKeyFromURL(location.search, 'guilds'));
  const [input, setInput] = useState(key ?? emptyKey);
  const [invalid, setInvalid] = useState(false);
  const enabled = runtime?.raiderio_enabled === true;
  const resource = useRaiderGuild(enabled ? key : null, locale);

  useEffect(() => {
    const pop = () => {
      const next = raiderKeyFromURL(location.search, 'guilds');
      setKey(next);
      setInput(next ?? emptyKey);
      setInvalid(false);
    };
    window.addEventListener('popstate', pop);
    return () => window.removeEventListener('popstate', pop);
  }, []);

  function submit(event: React.SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!enabled || resource.busy) return;
    const realm = raiderRealm(input.realm);
    const name = raiderGuildName(input.name);
    if (!realm || !name) { setInvalid(true); return; }
    const next = { region: input.region, realm, name };
    setInvalid(false);
    if (key && characterKeyID(next) === characterKeyID(key)) { resource.reload(); return; }
    setKey(next);
    history.pushState(null, '', raiderHref(next, 'guilds'));
  }

  const profile = resource.profile;
  const guild = profile?.data;
  const source = guild && raiderSourceLink(guild.profile_url, 'guilds');
  return <div className="social-workspace raider-social-workspace">
    <RaiderHero title={copy.guildTitle} intro={copy.guildIntro}/>
    <section className="search-panel panel social-search" aria-label={social.findGuild}>
      <form onSubmit={submit} noValidate>
        <RaiderIdentity id="raider-guild" value={input} locale={locale} guild onChange={next => { setInput(next); setInvalid(false); }}/>
        <button className="button-primary search-button" type="submit" disabled={!enabled || resource.busy}><Icon name="search" size={18}/>{social.findGuild}</button>
      </form>
      {invalid && <p className="validation-message" role="alert">{copy.invalidGuild}</p>}
    </section>
    <RaiderConnection runtime={runtime} locale={locale}/>
    <RaiderStatus resource={resource} locale={locale}/>
    {guild && profile && <>
      <section className="panel social-header">
        <div>
          <div className="eyebrow">{guild.region.toUpperCase()} · {guild.realm.name}{guild.faction && ` · ${guild.faction}`}</div>
          <h2>{guild.name}</h2>
          <p>Raider.IO · {observationStatus(profile.meta.status, social)} · {social.fetched} {formatDate(profile.meta.fetched_at, locale)}</p>
        </div>
        <div className="social-actions">
          {source && <a className="button-secondary" href={source} target="_blank" rel="noreferrer">{copy.openGuildSource}<Icon name="arrow" size={15}/></a>}
          <button className="button-secondary" type="button" onClick={resource.refresh} disabled={resource.busy}><Icon name="refresh" size={15}/>{t.refresh}</button>
        </div>
      </section>
      {key && <RaiderGuildSummary key={JSON.stringify([characterKeyID(key), locale, profile.meta.snapshot_id])} identity={key} profile={profile} locale={locale} reload={resource.reload}/>}
      <RaiderRoster key={key && characterKeyID(key)} guild={guild} locale={locale}/>
      <section className="panel social-panel"><h2>{t.raids}</h2><RaiderRaids data={guild.raids} locale={locale}/></section>
      {guild.rankings && <RaidRankings rankings={guild.rankings} locale={locale}/>}
    </>}
  </div>;
}

function RaiderRoster({ guild, locale }: { guild: RaiderGuildProfile; locale: Locale }) {
  const t = dictionary(locale);
  const social = socialMessages(locale);
  const copy = copyByLocale[locale];
  const [query, setQuery] = useState('');
  const [classFilter, setClassFilter] = useState('');
  const [specFilter, setSpecFilter] = useState('');
  const [roleFilter, setRoleFilter] = useState('');
  const [page, setPage] = useState(0);
  const [selection, setSelection] = useState<string[]>([]);
  const roster = guild.members ?? [];
  const selected = roster.filter(member => selection.includes(characterKeyID(member.key))).map(member => member.key);
  const search = query.trim().normalize('NFC').toLowerCase();
  const filtered = roster.filter(member =>
    (!classFilter || member.class === classFilter) &&
    (!specFilter || member.specialization === specFilter) &&
    (!roleFilter || member.role === roleFilter) &&
    member.key.name.normalize('NFC').toLowerCase().includes(search)
  );
  const pages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, pages - 1);
  const visible = filtered.slice(currentPage * pageSize, (currentPage + 1) * pageSize);

  function options(field: 'class' | 'specialization' | 'role') {
    const members = field === 'specialization' && classFilter ? roster.filter(member => member.class === classFilter) : roster;
    const values = new Set(members.map(member => member[field]).filter(value => value !== null));
    return [...values].sort((a, b) => a.localeCompare(b, locale)).map(value => <option key={value} value={value}>{value}</option>);
  }

  return <section className="panel social-panel">
    <div className="social-section-heading">
      <h2>{social.roster}{guild.members !== null && ` · ${number(roster.length, locale)}`}</h2>
      {selected.length > 0 && <a className="button-primary" href={raiderComparisonHref(selected)}>{social.compareSelected} ({selected.length})<Icon name="arrow" size={15}/></a>}
    </div>
    {guild.coverage.members?.state === 'partial' && <p className="social-note">{copy.partialRoster}</p>}
    {!roster.length ? <p className="empty-copy">{guild.members === null ? social.rosterUnknown : social.rosterEmpty}</p> : <>
      <div className="raider-roster-filters">
        <div className="field"><label htmlFor="raider-roster-query">{copy.memberSearch}</label><input id="raider-roster-query" value={query} onChange={event => { setQuery(event.target.value); setPage(0); }} autoComplete="off"/></div>
        <div className="field"><label htmlFor="raider-roster-class">{social.filterClass}</label><select id="raider-roster-class" value={classFilter} onChange={event => { setClassFilter(event.target.value); setSpecFilter(''); setPage(0); }}><option value="">{social.all}</option>{options('class')}</select></div>
        <div className="field"><label htmlFor="raider-roster-spec">{social.filterSpec}</label><select id="raider-roster-spec" value={specFilter} onChange={event => { setSpecFilter(event.target.value); setPage(0); }}><option value="">{social.all}</option>{options('specialization')}</select></div>
        <div className="field"><label htmlFor="raider-roster-role">{copy.roleFilter}</label><select id="raider-roster-role" value={roleFilter} onChange={event => { setRoleFilter(event.target.value); setPage(0); }}><option value="">{social.all}</option>{options('role')}</select></div>
      </div>
      {visible.length ? <div className="social-table-scroll"><table className="social-table raider-social-table">
        <thead><tr>{[social.select, social.name, t.realm, social.class, social.spec, copy.role, social.rank].map(label => <th key={label} scope="col">{label}</th>)}</tr></thead>
        <tbody>{visible.map(member => {
          const id = characterKeyID(member.key);
          const checked = selection.includes(id);
          return <tr key={id}>
            <td><input type="checkbox" aria-label={`${social.select} ${member.key.name}`} checked={checked} disabled={!checked && selected.length >= 5} onChange={event => {
              const next = event.target.checked;
              setSelection(next ? [...selected.map(characterKeyID), id] : selection.filter(value => value !== id));
            }}/></td>
            <th scope="row"><a href={raiderHref(member.key)}>{member.key.name}</a></th>
            <td>{member.key.realm}</td><td>{member.class ?? '—'}</td><td>{member.specialization ?? '—'}</td><td>{member.role ?? '—'}</td><td>{number(member.rank, locale)}</td>
          </tr>;
        })}</tbody>
      </table></div> : <p className="empty-copy">{social.filterEmpty}</p>}
      <div className="social-selection-footer">
        <span role="status">{social.selected} {selected.length} / 5</span>
        {pages > 1 && <div className="social-actions">
          <button type="button" className="button-secondary" disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>{copy.previous}</button>
          <span role="status">{number(currentPage + 1, locale)} / {number(pages, locale)}</span>
          <button type="button" className="button-secondary" disabled={currentPage + 1 === pages} onClick={() => setPage(currentPage + 1)}>{copy.next}</button>
        </div>}
      </div>
    </>}
  </section>;
}

function RaidRankings({ rankings, locale }: { rankings: NonNullable<RaiderGuildProfile['rankings']>; locale: Locale }) {
  const t = dictionary(locale);
  const copy = copyByLocale[locale];
  const difficulty: Record<string, string> = { normal: t.progressNormal, heroic: t.progressHeroic, mythic: t.progressMythic };
  const rows = Object.entries(rankings).flatMap(([raid, modes]) => Object.entries(modes).map(([mode, rank]) => ({ raid, mode, rank })));
  if (!rows.length) return null;
  return <section className="panel social-panel"><h2>{copy.ranking}</h2>
    <div className="social-table-scroll"><table className="social-table raider-social-table">
      <thead><tr>{[t.raids, copy.difficulty, copy.world, t.region, t.realm].map(label => <th key={label} scope="col">{label}</th>)}</tr></thead>
      <tbody>{rows.map(({ raid, mode, rank }) => <tr key={`${raid}/${mode}`}>
        <th scope="row">{raid}</th><td>{difficulty[mode] ?? mode}</td><td>{number(rank.world || null, locale)}</td><td>{number(rank.region || null, locale)}</td><td>{number(rank.realm || null, locale)}</td>
      </tr>)}</tbody>
    </table></div>
  </section>;
}

export function RaiderComparisonWorkspace({ runtime, locale }: WorkspaceProps) {
  const social = socialMessages(locale);
  const copy = copyByLocale[locale];
  const [keys, setKeys] = useState(() => raiderComparisonFromURL(location.search));
  const [rows, setRows] = useState(keys.length ? keys : [emptyKey]);
  const [invalid, setInvalid] = useState<ReturnType<typeof raiderComparisonKeys>['error']>(null);
  const enabled = runtime?.raiderio_enabled === true;

  useEffect(() => {
    const pop = () => {
      const next = raiderComparisonFromURL(location.search);
      setKeys(next);
      setRows(next.length ? next : [emptyKey]);
      setInvalid(null);
    };
    window.addEventListener('popstate', pop);
    return () => window.removeEventListener('popstate', pop);
  }, []);

  function submit(event: React.SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!enabled) return;
    const next = raiderComparisonKeys(rows);
    setInvalid(next.error);
    if (next.error) return;
    setRows(next.keys);
    setKeys(next.keys);
    const target = raiderComparisonHref(next.keys);
    if (target !== location.search) history.pushState(null, '', target);
  }

  return <div className="social-workspace raider-social-workspace">
    <RaiderHero title={copy.compareTitle} intro={copy.compareIntro}/>
    <form className="panel social-panel comparison-form" onSubmit={submit} noValidate>
      {rows.map((row, index) => <div className="comparison-input-row" key={index}>
        <RaiderIdentity id={`raider-compare-${index}`} value={row} locale={locale} index={index} onChange={next => {
          setRows(previous => previous.map((value, position) => index === position ? next : value));
          setInvalid(null);
        }}/>
        <button type="button" className="button-secondary" aria-label={`${social.remove} ${index + 1}`} disabled={rows.length === 1} onClick={() => {
          setRows(previous => previous.filter((_, position) => position !== index));
          setInvalid(null);
        }}><Icon name="close" size={15}/></button>
      </div>)}
      <div className="social-actions">
        <button type="button" className="button-secondary" disabled={rows.length >= 5} onClick={() => setRows(previous => [...previous, emptyKey])}>{social.add}</button>
        <button type="submit" className="button-primary" disabled={!enabled}>{social.compare}</button>
      </div>
      {invalid && <p className="validation-message" role="alert">{invalid === 'count' ? social.compareCount : invalid === 'duplicate' ? social.compareDuplicate : social.compareIdentity}</p>}
    </form>
    <RaiderConnection runtime={runtime} locale={locale}/>
    {enabled && keys.length > 0 && <RaiderComparisonResults identities={keys} locale={locale}/>}
  </div>;
}

function RaiderComparisonResults({ identities, locale }: { identities: CharacterKey[]; locale: Locale }) {
  // Fixed slots let the context and the cards share the same bounded polling.
  const resources = [
    useRaiderProfile(identities[0] ?? null, locale),
    useRaiderProfile(identities[1] ?? null, locale),
    useRaiderProfile(identities[2] ?? null, locale),
    useRaiderProfile(identities[3] ?? null, locale),
    useRaiderProfile(identities[4] ?? null, locale),
  ].slice(0, identities.length);
  const profiles = resources.map(resource => resource.profile);
  const binding = JSON.stringify([locale, identities, profiles.map(profile =>
    profile && [profile.meta.snapshot_id, profile.meta.status, profile.meta.job_id]
  )]);

  return <>
    <RaiderComparisonSummary key={binding} identities={identities} profiles={profiles} locale={locale}
      reload={() => resources.forEach(resource => resource.reload())}/>
    <RaiderDungeonComparison profiles={profiles} locale={locale}/>
    <div className="comparison-columns">
      {identities.map((identity, index) => <RaiderComparisonCard key={characterKeyID(identity)} identity={identity} locale={locale} resource={resources[index]}/>)}
    </div>
  </>;
}

function RaiderComparisonCard({ identity, locale, resource }: {
  identity: CharacterKey;
  locale: Locale;
  resource: ReturnType<typeof useRaiderProfile>;
}) {
  const social = socialMessages(locale);
  const copy = copyByLocale[locale];
  const profile = resource.profile;
  const data = profile?.data;
  const source = data && raiderSourceLink(data.profile_url, 'characters');
  return <article className="comparison-card panel social-panel">
    <div className="eyebrow">{identity.region.toUpperCase()} · {data?.realm.name ?? identity.realm}</div>
    <h2>{data?.name ?? identity.name}</h2>
    <RaiderStatus resource={resource} locale={locale}/>
    {data && profile && <>
      <p className="social-note">Raider.IO · {observationStatus(profile.meta.status, social)} · {social.fetched} {formatDate(profile.meta.fetched_at, locale)}</p>
      <dl className="social-facts">
        <div><dt>{social.ilvl}</dt><dd>{number(data.gear?.equipped_item_level, locale)}</dd></div>
        <div><dt>{social.class}</dt><dd>{data.class ?? '—'}</dd></div>
        <div><dt>{social.spec}</dt><dd>{data.specialization ?? '—'}</dd></div>
        <div><dt>Mythic+</dt><dd>{number(data.mythic_plus?.rating, locale)}</dd></div>
        <div><dt>{social.season}</dt><dd>{data.mythic_plus?.season ?? '—'}</dd></div>
      </dl>
      <details className="comparison-detail"><summary>{social.raids}</summary><RaiderRaids data={data.raids} locale={locale}/></details>
      <div className="social-actions raider-card-links">
        <a className="button-secondary" href={raiderHref({ region: data.region, realm: data.realm.slug, name: data.name })}>{copy.openProfile}</a>
        {source && <a className="button-secondary" href={source} target="_blank" rel="noreferrer">Raider.IO<Icon name="arrow" size={15}/></a>}
      </div>
    </>}
  </article>;
}
