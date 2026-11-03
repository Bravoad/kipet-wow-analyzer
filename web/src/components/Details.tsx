import { useState } from 'react';
import type { Dictionary } from '../i18n.ts';
import { formatDate, number } from '../i18n.ts';
import type { Coverage, Envelope, Equipment, Locale, Meta, Statistics, TalentLoadout, Talents } from '../shared/types.ts';
import { Icon } from './Icon.tsx';

export function CoverageBadge({ coverage, t }: { coverage?: Coverage; t: Dictionary }) {
  if (!coverage || coverage.state === 'unavailable') return <span className="badge muted"><span className="status-dot"/>{t.unavailable}</span>;
  if (coverage.state === 'partial') return <span className="badge amber"><Icon name="info" size={12}/>{t.partial}</span>;
  const stale = coverage.fresh_until && Date.parse(coverage.fresh_until) <= Date.now();
  return <span className={`badge ${stale ? 'amber' : 'teal'}`}><span className="status-dot"/>{stale ? t.stale : t.complete}</span>;
}
export function SectionStamp({ meta, section, locale, t }: { meta?: Meta; section: string; locale: Locale; t: Dictionary }) {
  const coverage = meta?.coverage[section as keyof Meta['coverage']];
  return <div className="section-stamp"><CoverageBadge coverage={coverage} t={t}/><span><Icon name="clock" size={13}/>{coverage?.fetched_at ? `${t.updated} ${formatDate(coverage.fetched_at, locale)}` : t.neverFetched}</span></div>;
}
export function SectionState({ loading, error, t }: { loading?: boolean; error?: boolean; t: Dictionary }) {
  return <div className="section-empty"><div className={`empty-icon ${loading ? 'spinning' : ''}`}><Icon name={loading ? 'refresh' : error ? 'info' : 'book'} size={25}/></div><h3>{loading ? t.sectionLoading : error ? t.sectionError : t.sectionEmpty}</h3><p>{t.sectionEmptyText}</p></div>;
}

export function EquipmentPanel({ result, t, locale, loading, error }: { result?: Envelope<Equipment | null>; t: Dictionary; locale: Locale; loading: boolean; error?: boolean }) {
  const items = result?.data?.items;
  return <section className="panel detail-panel"><div className="panel-heading"><div><span className="eyebrow">01 / {t.equipment}</span><h2>{t.equipmentTitle}</h2><p>{t.equipmentSubtitle}</p></div><Icon name="sword" size={27}/></div><SectionStamp meta={result?.meta} section="equipment" locale={locale} t={t}/>
    {!items ? <SectionState t={t} loading={loading && !result} error={error}/> : !items.length ? <p className="empty-copy">{t.noItems}</p> : <div className="equipment-list"><div className="equipment-head"><span>{t.slot}</span><span>{t.item}</span><span>{t.itemLevel}</span></div>{items.map(item => <div className={`equipment-row quality-${item.quality}`} key={item.slot}>
      <div className="slot-label">{t[item.slot as keyof Dictionary] ?? item.slot}</div>
      <div className="item-info"><span className="item-symbol"><Icon name={item.slot.includes('hand') ? 'sword' : item.slot.includes('finger') || item.slot.includes('trinket') ? 'gem' : 'shield'} size={21}/></span><div><div className="item-name">{item.name ?? `#${item.item_id}`}</div><div className="item-annotations"><span>{t.enchantments}: {item.enchantments === null ? t.notProvided : !item.enchantments.length ? t.absent : item.enchantments.map(enchantment => enchantment.display ?? `#${enchantment.id}`).join(', ')}</span><span>{t.sockets}: {item.sockets === null ? t.notProvided : !item.sockets.length ? t.absent : item.sockets.map(socket => socket.item_id ? `#${socket.item_id}` : t.emptySocket).join(', ')}</span></div></div></div>
      <div className="item-level">{number(item.item_level, locale)}</div>
    </div>)}</div>}
  </section>;
}

export function StatisticsPanel({ result, t, locale, loading, error }: { result?: Envelope<Statistics | null>; t: Dictionary; locale: Locale; loading: boolean; error?: boolean }) {
  const stats = result?.data;
  return <section className="panel detail-panel"><div className="panel-heading"><div><span className="eyebrow">02 / {t.statistics}</span><h2>{t.statsTitle}</h2><p>{t.statsSubtitle}</p></div><Icon name="chart" size={27}/></div><SectionStamp meta={result?.meta} section="statistics" locale={locale} t={t}/>
    {!stats ? <SectionState t={t} loading={loading && !result} error={error}/> : <><h3 className="subheading">{t.primary}</h3><div className="primary-stats"><StatBox label={t.health} value={number(stats.health, locale)} icon="shield"/>{(Object.keys(stats.primary) as (keyof Statistics['primary'])[]).map(key => <StatBox label={t[key]} value={number(stats.primary[key], locale)} key={key} icon={key === 'intellect' ? 'spark' : key === 'agility' ? 'sword' : 'shield'}/>)}</div>
    <h3 className="subheading">{t.secondary}</h3><div className="secondary-stats">{(['critical_strike', 'haste'] as const).map(key => <article className="stat-card" key={key}><h4><Icon name={key === 'haste' ? 'clock' : 'gem'} size={17}/>{t[key]}</h4><div className="stat-table-header"><span/><span>{t.rating}</span><span>{t.percent}</span></div>{(['melee', 'ranged', 'spell'] as const).map(type => <div className="stat-table-row" key={type}><span>{t[type]}</span><strong>{number(stats.secondary[key]?.[type]?.rating, locale)}</strong><strong>{number(stats.secondary[key]?.[type]?.percent, locale, '%')}</strong></div>)}</article>)}
      <article className="stat-card"><h4><Icon name="talents" size={17}/>{t.mastery}</h4><div className="stat-pair"><span>{t.rating}</span><strong>{number(stats.secondary.mastery?.rating, locale)}</strong></div><div className="stat-pair"><span>{t.percent}</span><strong>{number(stats.secondary.mastery?.percent, locale, '%')}</strong></div></article>
      <article className="stat-card"><h4><Icon name="shield" size={17}/>{t.versatility}</h4><div className="stat-pair"><span>{t.rating}</span><strong>{number(stats.secondary.versatility?.rating, locale)}</strong></div>{(['damage_done_percent', 'healing_done_percent', 'damage_taken_reduction_percent'] as const).map((key, index) => <div className="stat-pair" key={key}><span>{[t.damageBonus, t.healingBonus, t.damageReduction][index]}</span><strong>{number(stats.secondary.versatility?.[key], locale, '%')}</strong></div>)}</article>
    </div></>}
  </section>;
}
function StatBox({ label, value, icon }: { label: string; value: string; icon: string }) { return <div className="primary-stat"><Icon name={icon} size={17}/><span>{label}</span><strong>{value}</strong></div>; }

export function TalentsPanel({ result, t, locale, loading, error }: { result?: Envelope<Talents | null>; t: Dictionary; locale: Locale; loading: boolean; error?: boolean }) {
  const loadouts = result?.data?.loadouts;
  return <section className="panel detail-panel"><div className="panel-heading"><div><span className="eyebrow">03 / {t.talents}</span><h2>{t.talentsTitle}</h2><p>{t.talentsSubtitle}</p></div><Icon name="talents" size={27}/></div><SectionStamp meta={result?.meta} section="talents" locale={locale} t={t}/>
    {!loadouts ? <SectionState t={t} loading={loading && !result} error={error}/> : !loadouts.length ? <p className="empty-copy">{t.noTalents}</p> : <div className="loadouts">{loadouts.map((loadout, index) => <Loadout key={`${loadout.provider_id}-${index}`} loadout={loadout} t={t}/>)}</div>}
  </section>;
}
function Loadout({ loadout, t }: { loadout: TalentLoadout; t: Dictionary }) {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const copy = async () => { try { await navigator.clipboard.writeText(loadout.export_code!); setCopyState('copied'); } catch { setCopyState('failed'); } };
  return <article className="loadout"><div className="loadout-heading"><div><div className="loadout-icon"><Icon name="talents" size={25}/></div><h3>{loadout.specialization.name ?? `#${loadout.specialization.id}`}</h3></div><span className={`badge ${loadout.is_active ? 'teal' : 'muted'}`}>{loadout.is_active === null ? t.activityUnknown : loadout.is_active ? t.active : t.inactive}</span></div>
    {loadout.nodes === null ? <p className="talent-unknown"><Icon name="info" size={17}/>{t.nodesUnknown}</p> : <><div className="node-count">{loadout.nodes.length} {t.nodes}</div><div className="talent-nodes">{loadout.nodes.map(node => <div className="talent-node" key={`${node.tree}-${node.node_id}`}><span className="node-icon"><Icon name={node.tree === 'hero' ? 'spark' : node.tree === 'class' ? 'shield' : 'sword'} size={19}/></span><div><strong>#{node.node_id}</strong><span>{node.tree === 'class' ? t.classTree : node.tree === 'specialization' ? t.specTree : node.tree === 'hero' ? t.heroTree : t.treeUnknown}</span></div><span className="node-rank">{t.rank} {node.rank}</span></div>)}</div></>}
    {loadout.export_code && <div className="export-code"><div><label htmlFor={`export-${loadout.provider_id ?? 'unknown'}-${loadout.specialization.id}`}>{t.exportCode}</label><button type="button" className="text-button" onClick={() => void copy()}><Icon name={copyState === 'copied' ? 'check' : 'copy'} size={14}/>{copyState === 'copied' ? t.copied : t.copy}</button></div><textarea id={`export-${loadout.provider_id ?? 'unknown'}-${loadout.specialization.id}`} readOnly value={loadout.export_code} rows={2}/>{copyState === 'failed' && <p role="status">{t.copyFailed}</p>}</div>}
  </article>;
}
