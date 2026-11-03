import { useLayoutEffect, useRef, useState } from 'react';
import { ApiError, get } from '../shared/api.ts';
import { raiderPath } from '../shared/raider.ts';
import type { RaiderProfile } from '../shared/raider.ts';
import { makeRaiderComparisonExport, raiderComparisonContext } from '../shared/raiderComparison.ts';
import { downloadJSON, exportable } from '../shared/social.ts';
import { socialMessages } from '../shared/socialMessages.ts';
import type { CharacterKey, Envelope, Locale } from '../shared/types.ts';

const copy = {
  'ru-RU': {
    sameSpecialization: 'Совпадают класс и специализация',
    download: 'Скачать сравнение JSON',
    saved: 'Сравнение скачано.',
    unavailable: 'Для выгрузки нужны актуальные снимки всех выбранных профилей.',
    changed: 'Данные изменились или устарели. Перечитайте профили и повторите.',
    failed: 'Не удалось проверить профили. Повторите попытку.',
  },
  'en-US': {
    sameSpecialization: 'Same class and specialization',
    download: 'Download comparison JSON',
    saved: 'Comparison downloaded.',
    unavailable: 'Export requires a fresh snapshot for each selected profile.',
    changed: 'Profile data changed or expired. Reload and try again.',
    failed: 'The profiles could not be checked. Try again.',
  },
} as const;

type ExportState = 'idle' | 'checking' | 'saved' | 'changed' | 'failed';

export function RaiderComparisonSummary({ identities, profiles, locale, reload }: {
  identities: readonly CharacterKey[];
  profiles: readonly (Envelope<RaiderProfile> | null)[];
  locale: Locale;
  reload: () => void;
}) {
  const t = copy[locale];
  const social = socialMessages(locale);
  const [state, setState] = useState<ExportState>('idle');
  const operation = useRef<AbortController | null>(null);
  const context = raiderComparisonContext(profiles);
  const canExport = identities.length > 0 && identities.length <= 5 && profiles.length === identities.length &&
    !context.duplicateProfiles && profiles.every(profile => {
      const meta = profile?.meta;
      return meta?.source === 'raiderio' && meta.status === 'fresh' && meta.job_id === null &&
        !!meta.snapshot_id && exportable(meta);
    });
  const match = (value: boolean | null) => value === null ? social.unknown : value ? social.yes : social.no;

  useLayoutEffect(() => () => operation.current?.abort(), []);

  async function exportComparison() {
    if (!canExport || operation.current) return;
    const controller = new AbortController();
    operation.current = controller;
    setState('checking');
    try {
      const reads = await Promise.all(identities.map(identity =>
        get<RaiderProfile | null>(raiderPath(identity, locale), controller.signal)));
      if (controller.signal.aborted) return;
      const result = makeRaiderComparisonExport(
        reads.map(read => read.status === 200 ? read.body : null),
        profiles.map(profile => profile?.meta.snapshot_id ?? null),
      );
      if (result) {
        downloadJSON(result, 'kipet-raiderio-comparison.json');
        setState('saved');
      } else {
        setState('changed');
      }
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error instanceof ApiError && (
        error.status === 403 || error.status === 404 || error.detail.code === 'PROVIDER_ACCESS_DENIED'
      )) {
        setState('changed');
        reload();
        return;
      }
      setState('failed');
    } finally {
      controller.abort();
      operation.current = null;
    }
  }

  return <section className="panel social-panel">
    <div className="social-section-heading">
      <h2>{social.context}</h2>
      <div className="social-actions">
        <button type="button" className="button-secondary" disabled={state === 'checking'} onClick={reload}>{social.reload}</button>
        <button type="button" className="button-secondary" disabled={!canExport || state === 'checking'} onClick={exportComparison}>
          {state === 'checking' ? social.exporting : t.download}
        </button>
      </div>
    </div>
    <dl className="social-facts">
      <div><dt>{t.sameSpecialization}</dt><dd>{match(context.sameSpecialization)}</dd></div>
      <div><dt>{social.sameSeason}</dt><dd>{match(context.sameMythicSeason)}</dd></div>
    </dl>
    {context.duplicateProfiles && <p className="validation-message" role="alert">{social.compareDuplicate}</p>}
    {!canExport && !context.duplicateProfiles && <p className="social-note">{t.unavailable}</p>}
    {state === 'changed' && <p className="validation-message" role="alert">{t.changed}</p>}
    {state === 'failed' && <p className="validation-message" role="alert">{t.failed}</p>}
    {state === 'saved' && <p className="social-note" role="status">{t.saved}</p>}
  </section>;
}
