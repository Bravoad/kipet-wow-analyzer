import { useEffect, useState } from 'react';
import type { Dictionary } from '../i18n.ts';
import { formatDate } from '../i18n.ts';
import type { ApiError } from '../shared/api.ts';
import { analysisMessage } from '../shared/analysisMessages.ts';
import type { AnalysisContent, AnalysisEvidence, AnalysisReference, AnalysisReport, AnalysisResult, Envelope, Locale } from '../shared/types.ts';
import { Icon } from './Icon.tsx';

interface Props {
  result: Envelope<AnalysisReport> | null; loading: boolean; error: ApiError | null;
  outdated: boolean; paused: boolean; content: AnalysisContent; locale: Locale; t: Dictionary;
  onContent: (content: AnalysisContent) => void; onRetry: () => void; onReloadProfile: () => void;
}

export function AnalysisPanel({ result, loading, error, outdated, paused, content, locale, t, onContent, onRetry, onReloadProfile }: Props) {
  const [cooldown, setCooldown] = useState(0);
  const [showNotApplicable, setShowNotApplicable] = useState(false);
  useEffect(() => {
    const seconds = error?.detail.retry_after_seconds;
    if (!seconds) { setCooldown(0); return; }
    const deadline = Date.now() + seconds * 1000;
    setCooldown(seconds);
    const timer = setInterval(() => setCooldown(Math.max(0, Math.ceil((deadline - Date.now()) / 1000))), 1000);
    return () => clearInterval(timer);
  }, [error]);
  const report = result?.data;
  useEffect(() => { setShowNotApplicable(false); }, [report?.snapshot_id, content]);
  const visibleResults = report?.results.filter(item => showNotApplicable || item.applicability !== 'not_applicable') ?? [];
  return <section className="analysis-panel" aria-label={t.analysis}>
    <div className="panel analysis-intro"><div className="panel-heading"><div><span className="eyebrow">04 / {t.analysis}</span><h2>{t.analysisTitle}</h2><p>{t.analysisSubtitle}</p></div><Icon name="spark" size={26}/></div>
      <div className="analysis-context"><label htmlFor="analysis-content">{t.analysisContext}</label><div className="select-wrap"><select id="analysis-content" value={content} onChange={event => onContent(event.target.value as AnalysisContent)}>{(['general_pve', 'raid', 'mythic_plus'] as const).map(value => <option key={value} value={value}>{t[value]}</option>)}</select><Icon name="chevron" size={14}/></div>{report && <span className="analysis-context-version">{t.patch}: <strong>{report.context.patch ?? t.unknown}</strong></span>}</div>
    </div>
    {loading && <div className="analysis-state panel" role="status" aria-live="polite"><Icon name="refresh" className="spinning"/><span>{t.analysisLoading}</span></div>}
    {!loading && (outdated || paused || error) && <div className="analysis-state panel" role="status"><Icon name="info"/><div><p>{outdated ? t.analysisOutdated : paused ? t.analysisPaused : t.analysisError}</p>{cooldown > 0 && <p className="analysis-retry-time">{t.retryIn} {cooldown} {t.seconds}</p>}</div><button type="button" className="button-secondary" disabled={cooldown > 0} onClick={outdated ? onReloadProfile : onRetry}>{outdated ? t.analysisReloadProfile : t.analysisRetry}</button></div>}
    {report && <>
      {report.ruleset?.synthetic ? <div className="analysis-notice synthetic"><Icon name="spark" size={20}/><div><strong>{t.analysisSynthetic}</strong><p>{t.analysisSyntheticText}</p></div></div> : !report.ruleset && <div className="analysis-notice"><Icon name="info" size={20}/><div><strong>{t.analysisNoRules}</strong><p>{t.analysisNoRulesText}</p></div></div>}
      <div className="analysis-summary" aria-label={t.analysisRules}>{[
        { value: report.summary.ok, label: t.analysisChecked, tone: 'ok', icon: 'check' },
        { value: report.summary.info, label: t.analysisObservations, tone: 'info', icon: 'book' },
        { value: report.summary.warning, label: t.analysisWarnings, tone: 'warning', icon: 'info' },
        { value: report.summary.unknown, label: t.analysisUnknown, tone: 'unknown', icon: 'clock' },
      ].map(item => <div className={`analysis-summary-item ${item.tone}`} key={item.tone}><Icon name={item.icon} size={17}/><strong>{item.value}</strong><span>{item.label}</span></div>)}</div>
      {!!report.summary.not_applicable && <label className="analysis-show-excluded"><input type="checkbox" checked={showNotApplicable} onChange={event => setShowNotApplicable(event.target.checked)}/><span>{t.analysisShowNotApplicable} ({report.summary.not_applicable})</span></label>}
      <div className="analysis-list-heading"><h3>{t.analysisRules}</h3><span>{t.analysisEvaluated} {formatDate(report.evaluated_at, locale)}</span></div>
      <div className="analysis-results">{visibleResults.length ? visibleResults.map(item => <ResultCard key={JSON.stringify([report.snapshot_id, report.context.content, item.rule_id, item.params.slot ?? null, item.params.item_id ?? null, item.params.set_id ?? null])} result={item} locale={locale} t={t} references={report.ruleset?.references ?? []}/>) : <p className="analysis-no-results panel">{t.analysisNoResults}</p>}</div>
      {!!report.diagnostics.length && <section className="analysis-diagnostics panel"><h3><Icon name="shield" size={17}/>{t.analysisDiagnostics}</h3><ul>{report.diagnostics.map((diagnostic, index) => <li key={`${diagnostic.code}-${index}`}><Icon name="info" size={14}/><div>{diagnostic.section && <strong>{sectionName(diagnostic.section, t)}</strong>}<p>{analysisMessage(diagnostic.message_key || diagnostic.code, locale)}</p>{!!diagnostic.evidence?.length && <EvidenceList evidence={diagnostic.evidence} locale={locale} t={t} references={report.ruleset?.references}/>}</div></li>)}</ul></section>}
      <details className="analysis-provenance panel"><summary><Icon name="book" size={16}/>{t.analysisDetails}<Icon name="chevron" size={13}/></summary><dl><div><dt>{t.analysisSnapshot}</dt><dd><code>{report.snapshot_id}</code></dd></div><div><dt>{t.analysisRulesetVersion}</dt><dd>{report.ruleset?.version ?? t.notProvided}</dd></div>{report.ruleset && <><div><dt>{t.analysisReviewed}</dt><dd>{formatDate(report.ruleset.reviewed_at, locale)} · {report.ruleset.reviewed_by}</dd></div><div><dt>{t.analysisDigest}</dt><dd><code>{report.ruleset.digest}</code></dd></div><div><dt>{t.analysisReference}</dt><dd>{report.ruleset.references.map(reference => <Reference key={reference.id} reference={reference.id} references={report.ruleset!.references}/>)}</dd></div></>}</dl></details>
    </>}
  </section>;
}

function ResultCard({ result, locale, t, references }: { result: AnalysisResult; locale: Locale; t: Dictionary; references: AnalysisReference[] }) {
  const outcome = result.applicability === 'not_applicable' ? null : result.outcome;
  const tone = outcome === 'OK' ? 'ok' : outcome === 'Info' ? 'info' : outcome === 'Warning' ? 'warning' : outcome === 'Critical' ? 'critical' : 'unknown';
  const label = outcome === 'OK' ? t.analysisOK : outcome === 'Info' ? t.analysisInfo : outcome === 'Warning' ? t.analysisWarnings : outcome === 'Critical' ? t.analysisCritical : outcome === null ? t.analysisNotApplicable : t.analysisUnknown;
  const title = result.rule_id === 'GEAR_MISSING_ENCHANT' ? t.analysisGearEnchant : result.rule_id === 'GEAR_SET_BONUS' ? t.analysisGearSet : result.rule_id === 'TALENT_BUILD_DIFF' ? t.analysisTalentDiff : result.rule_id;
  const slot = typeof result.params.slot === 'string' ? result.params.slot : null;
  const confidence = result.confidence === 'high' ? t.analysisHigh : result.confidence === 'medium' ? t.analysisMedium : t.analysisConfidenceUnknown;
  return <article className={`analysis-result panel ${tone}`}><div className="analysis-result-heading"><div className="analysis-rule-icon"><Icon name={result.rule_id === 'GEAR_MISSING_ENCHANT' ? 'sword' : result.rule_id === 'GEAR_SET_BONUS' ? 'shield' : 'talents'} size={21}/></div><h4>{title}{slot && <span>{sectionName(slot, t)}</span>}</h4><span className={`analysis-outcome ${tone}`}><Icon name={outcome === 'OK' ? 'check' : outcome === 'Unknown' || outcome === null ? 'clock' : 'info'} size={12}/>{label}</span></div><p className="analysis-result-message">{analysisMessage(result.message_key || result.reason_code, locale)}</p><div className="analysis-result-footer"><span>{t.analysisConfidence}: <strong>{confidence}</strong></span><span>{t.analysisRuleVersion} {result.rule_version}</span></div><EvidenceList evidence={result.evidence} locale={locale} t={t} reason={result.confidence_reason} references={references}/></article>;
}

function EvidenceList({ evidence, locale, t, reason, references }: { evidence: AnalysisEvidence[]; locale: Locale; t: Dictionary; reason?: string; references?: AnalysisReference[] }) {
  if (!evidence?.length && !reason) return null;
  return <details className="analysis-evidence"><summary>{t.analysisEvidence}<span>{evidence?.length ?? 0}</span><Icon name="chevron" size={13}/></summary>{reason && <p className="analysis-confidence-reason">{analysisMessage(reason, locale)}</p>}<div>{(evidence ?? []).map((item, index) => <div className="analysis-evidence-item" key={`${item.section}-${item.path}-${index}`}><div className="analysis-evidence-meta"><strong>{sectionName(item.section, t)}</strong><span>{formatDate(item.fetched_at, locale)}</span></div><dl><div><dt>{t.analysisField}</dt><dd><code>{item.path}</code></dd></div><div><dt>{t.analysisValue}</dt><dd><code>{valueText(item.value)}</code></dd></div>{item.reference && <div><dt>{t.analysisReference}</dt><dd><Reference reference={item.reference} references={references}/></dd></div>}</dl></div>)}</div></details>;
}

function Reference({ reference, references }: { reference: string; references?: AnalysisReference[] }) { const source = references?.find(item => item.id === reference || item.url === reference); const title = source?.title ?? reference; const url = source?.url ?? reference; return url.startsWith('https://') ? <a href={url} target="_blank" rel="noreferrer">{title}</a> : <span>{title}</span>; }
function sectionName(value: string, t: Dictionary) { return value === 'profile' ? t.character : (t as Record<string, string>)[value] ?? value; }
function valueText(value: unknown) { const serialized = typeof value === 'string' ? value : JSON.stringify(value); return serialized && serialized.length > 800 ? `${serialized.slice(0, 800)}…` : serialized ?? '—'; }

