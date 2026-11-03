import { useLayoutEffect, useRef, useState } from 'react';
import { number } from '../i18n.ts';
import { ApiError, get } from '../shared/api.ts';
import { raiderPath } from '../shared/raider.ts';
import type { RaiderGuildProfile } from '../shared/raider.ts';
import { makeRaiderGuildExport } from '../shared/raiderGuildExport.ts';
import { summarizeRaiderRoster } from '../shared/raiderRoster.ts';
import { downloadJSON } from '../shared/social.ts';
import { socialMessages } from '../shared/socialMessages.ts';
import type { CharacterKey, Envelope, Locale } from '../shared/types.ts';

const copy = {
  'ru-RU': {
    title: 'Сводка состава',
    members: 'Участники в снимке',
    realms: 'Серверы участников',
    roles: 'Роли',
    classes: 'Классы',
    specs: 'Специализации',
    partial: 'Сводка рассчитана по доступной части состава.',
    download: 'Скачать сводку JSON',
    checking: 'Проверяем снимок…',
    saved: 'Сводка скачана.',
    unavailable: 'Для экспорта нужен актуальный снимок гильдии.',
    changed: 'Снимок изменился или устарел. Перечитайте данные и повторите.',
    failed: 'Не удалось проверить снимок. Повторите попытку.',
    reload: 'Перечитать данные',
  },
  'en-US': {
    title: 'Roster summary',
    members: 'Members in snapshot',
    realms: 'Member realms',
    roles: 'Roles',
    classes: 'Classes',
    specs: 'Specializations',
    partial: 'The summary covers the available part of the roster.',
    download: 'Download JSON summary',
    checking: 'Checking snapshot…',
    saved: 'Summary downloaded.',
    unavailable: 'A fresh guild snapshot is required for export.',
    changed: 'The snapshot changed or expired. Reload the guild and try again.',
    failed: 'The snapshot could not be checked. Try again.',
    reload: 'Reload guild data',
  },
} as const;

type ExportState = 'idle' | 'checking' | 'saved' | 'changed' | 'failed';

export function RaiderGuildSummary({ profile, identity, locale, reload }: {
  profile: Envelope<RaiderGuildProfile>;
  identity: CharacterKey;
  locale: Locale;
  reload: () => void;
}) {
  const t = copy[locale];
  const social = socialMessages(locale);
  const [state, setState] = useState<ExportState>('idle');
  const operation = useRef<AbortController | null>(null);
  const { meta, data } = profile;
  const summary = summarizeRaiderRoster(data.members);
  const canExport = meta.status === 'fresh' && !meta.job_id && !!meta.snapshot_id;
  const path = raiderPath(identity, locale, 'guilds');

  useLayoutEffect(() => () => operation.current?.abort(), []);

  async function exportSummary() {
    if (!canExport || operation.current) return;
    const controller = new AbortController();
    operation.current = controller;
    setState('checking');
    try {
      const response = await get<RaiderGuildProfile | null>(path, controller.signal);
      if (controller.signal.aborted) return;
      const result = response.status === 200 ? makeRaiderGuildExport(response.body, meta.snapshot_id) : null;
      if (result) {
        downloadJSON(result, 'kipet-raiderio-guild-summary.json');
        setState('saved');
      } else {
        setState('changed');
      }
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error instanceof ApiError && ['GUILD_UNAVAILABLE', 'PROVIDER_ACCESS_DENIED'].includes(error.detail.code)) {
        reload();
        return;
      }
      setState('failed');
    } finally {
      operation.current = null;
    }
  }

  return <section className="panel social-panel">
    <div className="social-section-heading">
      <h2>{t.title}</h2>
      <button type="button" className="button-secondary" disabled={!canExport || state === 'checking'} onClick={exportSummary}>
        {state === 'checking' ? t.checking : t.download}
      </button>
    </div>
    {state === 'saved' && <p className="social-note" role="status">{t.saved}</p>}
    {state === 'failed' && <p className="validation-message" role="alert">{t.failed}</p>}
    {state === 'changed' && <div className="social-actions">
      <p className="validation-message" role="alert">{t.changed}</p>
      <button type="button" className="button-secondary" onClick={reload}>{t.reload}</button>
    </div>}
    {!canExport && <p className="social-note">{t.unavailable}</p>}
    {data.coverage.members?.state === 'partial' && <p className="social-note">{t.partial}</p>}
    {summary === null ? <p className="empty-copy">{social.rosterUnknown}</p> : <>
      <dl className="raider-summary-totals">
        <div><dt>{t.members}</dt><dd>{number(summary.members, locale)}</dd></div>
        <div><dt>{t.realms}</dt><dd>{number(summary.realms, locale)}</dd></div>
      </dl>
      {summary.members > 0 && <div className="social-distributions">
        <Distribution title={t.roles} groups={summary.roles} unknown={summary.unknown.role} locale={locale}/>
        <Distribution title={t.classes} groups={summary.classes} unknown={summary.unknown.class} locale={locale}/>
        <Distribution title={t.specs} groups={summary.specializations.map(group => ({
          name: `${group.className} · ${group.name}`, count: group.count,
        }))} unknown={summary.unknown.specialization} locale={locale}/>
      </div>}
    </>}
  </section>;
}

function Distribution({ title, groups, unknown, locale }: {
  title: string;
  groups: { name: string; count: number }[];
  unknown: number;
  locale: Locale;
}) {
  return <details className="comparison-detail raider-roster-distribution">
    <summary>{title}</summary>
    <ul className="social-distribution">
      {groups.map(group => <li key={group.name}><span>{group.name}</span><strong>{number(group.count, locale)}</strong></li>)}
      {unknown > 0 && <li><span>{socialMessages(locale).unknown}</span><strong>{number(unknown, locale)}</strong></li>}
    </ul>
  </details>;
}
