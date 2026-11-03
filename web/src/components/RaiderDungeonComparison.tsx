import { dictionary, number } from '../i18n.ts';
import { formatRunDuration } from '../shared/progress.ts';
import { raiderSourceLink } from '../shared/raider.ts';
import type { RaiderProfile } from '../shared/raider.ts';
import { raiderDungeonComparison } from '../shared/raiderDungeonComparison.ts';
import type { Envelope, Locale } from '../shared/types.ts';

const copy = {
  'ru-RU': {
    title: 'Ключи Mythic+ по подземельям',
    dungeon: 'Подземелье',
    required: 'Для таблицы нужны два или больше разных профилей с подтверждённым общим сезоном.',
    scope: 'Показаны прохождения из списка лучших результатов Raider.IO с подтверждённым сезоном.',
    noRuns: 'В выбранных снимках нет подтверждённых записей для этого сезона.',
    noRecord: 'Нет записи в списке',
    unknown: 'Нет подтверждённых данных',
    coverage: {
      available: 'Список получен',
      partial: 'Частичный охват',
      unavailable: 'Список недоступен',
      empty: 'Пустой список',
    },
  },
  'en-US': {
    title: 'Mythic+ keys by dungeon',
    dungeon: 'Dungeon',
    required: 'This table needs at least two distinct profiles with a confirmed shared season.',
    scope: 'These are runs from Raider.IO’s best-results list with a confirmed season.',
    noRuns: 'The selected snapshots have no verified entries for this season.',
    noRecord: 'No entry in list',
    unknown: 'No verified data',
    coverage: {
      available: 'List available',
      partial: 'Partial coverage',
      unavailable: 'List unavailable',
      empty: 'Empty list',
    },
  },
} as const;

export function RaiderDungeonComparison({ profiles, locale }: {
  profiles: readonly (Envelope<RaiderProfile> | null)[];
  locale: Locale;
}) {
  if (profiles.length < 2) return null;
  const t = dictionary(locale);
  const labels = copy[locale];
  const comparison = raiderDungeonComparison(profiles);

  return <section className="panel social-panel">
    <details className="raider-dungeon-comparison">
      <summary>{labels.title}</summary>
      {comparison ? <>
        <p className="social-note">{t.season} {comparison.season} · {labels.scope}</p>
        <div className="social-table-scroll raider-dungeon-scroll" role="region" aria-label={labels.title} tabIndex={0}>
          <table className="social-table raider-social-table raider-dungeon-table">
            <thead><tr>
              <th scope="col">{labels.dungeon}</th>
              {comparison.columns.map(column => <th scope="col" key={column.id}>
                <strong>{column.name}</strong>
                <small>{column.region.toUpperCase()} · {column.realm}</small>
                <small>{labels.coverage[column.coverage]}</small>
              </th>)}
            </tr></thead>
            <tbody>
              {comparison.rows.map(row => <tr key={row.dungeon}>
                <th scope="row">{row.dungeon}</th>
                {row.cells.map((runs, index) => {
                  const column = comparison.columns[index];
                  return <td key={column.id}>
                    {runs.length > 0 ? <ul className="raider-dungeon-runs">
                      {runs.map(run => {
                        const link = raiderSourceLink(run.url, 'mythic-plus-runs');
                        return <li key={run.url}>
                          <div className="raider-dungeon-result">
                            {link ? <a href={link} target="_blank" rel="noreferrer" aria-label={`${column.name} · ${row.dungeon} · +${run.level} · Raider.IO`}>+{run.level}</a> : <strong>+{run.level}</strong>}
                            <span className={`badge ${run.timed === null ? 'muted' : run.timed ? 'teal' : 'amber'}`}>
                              {run.timed === null ? t.progressTimeUnknown : run.timed ? t.progressTimed : t.progressOvertime}
                            </span>
                          </div>
                          <small>{t.progressDuration} {formatRunDuration(run.duration_ms)} · {t.progressRunRating} {number(run.score, locale)}</small>
                        </li>;
                      })}
                    </ul> : column.coverage === 'unavailable' || column.coverage === 'partial' ? labels.unknown : labels.noRecord}
                  </td>;
                })}
              </tr>)}
              {comparison.rows.length === 0 && <tr><td colSpan={comparison.columns.length + 1}>{labels.noRuns}</td></tr>}
            </tbody>
          </table>
        </div>
      </> : <p className="social-note">{labels.required}</p>}
    </details>
  </section>;
}
