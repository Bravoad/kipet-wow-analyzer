import type { Dictionary } from '../i18n.ts';
import { number } from '../i18n.ts';
import type { ApiError } from '../shared/api.ts';
import { formatProgressDate, formatRunDuration, raidDifficulty, raidKillState, raidStatus } from '../shared/progress.ts';
import type { Envelope, Locale, MythicPlus, ProgressData, ProgressSection, RaidInstance, Raids } from '../shared/types.ts';
import { Icon } from './Icon.tsx';
import { SectionStamp, SectionState } from './Details.tsx';

interface Props {
  section: ProgressSection; result: Envelope<ProgressData[ProgressSection] | null> | null;
  loading: boolean; error: ApiError | null; outdated: boolean; paused: boolean;
  locale: Locale; t: Dictionary; onRetry: () => void; onReloadProfile: () => void;
}

export function ProgressPanel({ section, result, loading, error, outdated, paused, locale, t, onRetry, onReloadProfile }: Props) {
  const mythic = section === 'mythic_plus';
  const unavailable = !loading && !error && !outdated && !paused && !result?.data;
  return <div className="progress-panel">
    <section className="panel detail-panel progress-intro"><div className="panel-heading"><div><span className="eyebrow">{mythic ? '05 / MYTHIC+' : `06 / ${t.raids}`}</span><h2>{mythic ? t.mythicTitle : t.raidsTitle}</h2><p>{mythic ? t.mythicSubtitle : t.raidsSubtitle}</p></div><Icon name={mythic ? 'clock' : 'shield'} size={27}/></div><SectionStamp meta={result?.meta} section={section} locale={locale} t={t}/></section>
    {loading && <div className="analysis-state panel" role="status"><Icon name="refresh" className="spinning" size={20}/><div>{t.progressLoading}</div></div>}
    {(error || outdated || paused) && <div className="analysis-state panel" role={error ? 'alert' : 'status'}><Icon name="info" size={20}/><div>{outdated ? t.progressOutdated : paused ? t.progressPaused : t.progressError}{error?.detail.retry_after_seconds ? <p className="analysis-retry-time">{t.retryIn} {error.detail.retry_after_seconds} {t.seconds}</p> : null}</div><button type="button" className="button-secondary" onClick={outdated ? onReloadProfile : onRetry}>{outdated ? t.progressReloadProfile : t.progressRetry}</button></div>}
    {unavailable && <section className="panel"><SectionState t={t}/></section>}
    {result?.data && !loading && !error && !outdated && !paused && (mythic ? <MythicContent data={result.data as MythicPlus} locale={locale} t={t} synthetic={result.meta.source === 'fixture'}/> : <RaidsContent data={result.data as Raids} locale={locale} t={t}/>)}
  </div>;
}

function MythicContent({ data, locale, t, synthetic }: { data: MythicPlus; locale: Locale; t: Dictionary; synthetic: boolean }) {
  const scope = data.run_scope === 'season_best' ? t.progressRunsSeason : data.run_scope === 'current_period_best' ? t.progressRunsPeriod : t.progressRunsUnknown;
  return <>
    <div className="progress-metrics">
      <article className="panel progress-metric"><span>{synthetic ? t.progressFixtureRating : t.progressRating}</span><strong>{number(data.rating, locale)}</strong><p>{t.progressRatingNote}</p></article>
      <article className="panel progress-metric"><span>{t.season}</span><strong className="progress-season-name">{data.season ? data.season.name ?? `#${data.season.id}` : t.progressSeasonUnknown}</strong><p>{data.season?.is_current === true ? t.progressCurrentSeason : data.season?.is_current === false ? t.progressOtherSeason : t.progressSeasonUnknown}</p></article>
      <article className="panel progress-metric"><span>{t.progressRunsCount}</span><strong>{data.best_runs === null ? '—' : number(data.best_runs.length, locale)}</strong><p>{scope}</p></article>
    </div>
    {(!data.season || data.season.is_current === null) && <div className="analysis-notice"><Icon name="info" size={19}/><div><strong>{t.progressSeasonUnknown}</strong><p>{t.progressSeasonUnknownText}</p></div></div>}
    <section className="panel detail-panel"><div className="progress-list-heading"><h3>{t.progressRuns}</h3><span>{scope}</span></div><p className="progress-note">{t.progressScopeNote}</p>
      {data.best_runs === null ? <p className="empty-copy">{t.progressRunsNotProvided}</p> : data.best_runs.length === 0 ? <p className="empty-copy">{t.progressNoRuns}</p> : <div className="progress-runs">{data.best_runs.map((run, index) => <article className="progress-run" key={`${run.dungeon.id}-${run.keystone_level}-${run.completed_at ?? 'unknown'}-${index}`}><div className="progress-run-title"><span className="progress-key">+{run.keystone_level}</span><h4>{run.dungeon.name ?? `#${run.dungeon.id}`}</h4><span className={`badge ${run.within_time === true ? 'teal' : run.within_time === false ? 'amber' : 'muted'}`}>{run.within_time === null ? t.progressTimeUnknown : run.within_time ? t.progressTimed : t.progressOvertime}</span></div><dl className="progress-run-facts"><div><dt>{t.progressDuration}</dt><dd>{formatRunDuration(run.duration_ms)}</dd></div><div><dt>{t.progressRunRating}</dt><dd>{number(run.rating, locale)}</dd></div><div><dt>{t.progressCompleted}</dt><dd>{formatProgressDate(run.completed_at, locale)}</dd></div></dl></article>)}</div>}
    </section>
    <section className="panel detail-panel"><div className="progress-list-heading"><h3>{t.progressAvailableSeasons}</h3></div><p className="progress-note">{t.progressSeasonsNote}</p>{data.available_seasons === null ? <p className="empty-copy">{t.progressSeasonsNotProvided}</p> : !data.available_seasons.length ? <p className="empty-copy">{t.progressNoSeasons}</p> : <ul className="progress-seasons">{data.available_seasons.map(season => <li key={season.id}><strong>{season.name ?? `#${season.id}`}</strong><span>{season.is_current === null ? t.progressSeasonUnknown : season.is_current ? t.progressCurrentSeason : t.progressOtherSeason}</span></li>)}</ul>}</section>
  </>;
}

function RaidsContent({ data, locale, t }: { data: Raids; locale: Locale; t: Dictionary }) {
  return <section className="panel detail-panel"><div className="progress-list-heading"><h3>{t.progressRaidCatalog}</h3></div><p className="progress-note">{t.progressRaidCatalogNote}</p>
    {data.expansions === null ? <p className="empty-copy">{t.progressRaidsNotProvided}</p> : !data.expansions.length ? <p className="empty-copy">{t.progressNoRaids}</p> : <div className="raid-expansions">{data.expansions.map(expansion => <section className="raid-expansion" key={expansion.id}><h3>{expansion.name ?? `#${expansion.id}`}</h3>{expansion.instances === null ? <p className="empty-copy">{t.progressRaidsNotProvided}</p> : !expansion.instances.length ? <p className="empty-copy">{t.progressNoRaids}</p> : expansion.instances.map(instance => <Raid key={instance.id} data={instance} locale={locale} t={t}/>)}</section>)}</div>}
  </section>;
}

function Raid({ data, locale, t }: { data: RaidInstance; locale: Locale; t: Dictionary }) {
  return <article className="raid-instance"><h4><Icon name="shield" size={18}/>{data.name ?? `#${data.id}`}</h4>{data.modes === null ? <p className="empty-copy">{t.progressNoModes}</p> : !data.modes.length ? <p className="empty-copy">{t.progressModesEmpty}</p> : <div className="raid-modes">{data.modes.map((mode, index) => <details className="raid-mode" key={`${mode.difficulty}-${index}`} open><summary><span className="raid-mode-title">{raidDifficulty(mode.difficulty, t)}</span><strong>{number(mode.completed, locale)} / {number(mode.total, locale)} <span>{t.progressBosses}</span></strong><span className="raid-mode-status">{raidStatus(mode.status, t)}</span><Icon name="chevron" size={15}/></summary>{mode.encounters === null ? <p className="empty-copy">{t.progressNoEncounters}</p> : !mode.encounters.length ? <p className="empty-copy">{t.progressEncountersEmpty}</p> : <ul className="raid-encounters">{mode.encounters.map(encounter => <li key={encounter.id}><div><strong>{encounter.name ?? `#${encounter.id}`}</strong><span>{raidKillState(encounter.completed_count, t)}</span></div><div className="raid-kill-data"><span>{t.progressKills}: <strong>{number(encounter.completed_count, locale)}</strong></span><span>{t.progressLastKill}: {formatProgressDate(encounter.last_kill_at, locale)}</span></div></li>)}</ul>}</details>)}</div>}</article>;
}
