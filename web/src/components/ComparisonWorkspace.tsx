import { useEffect, useRef, useState } from 'react';
import { dictionary, formatDate, number } from '../i18n.ts';
import { ApiError } from '../shared/api.ts';
import { requestComparison, useComparison } from '../features/useComparison.ts';
import { comparisonFromSearch, comparisonKeys, downloadJSON, exportable } from '../shared/social.ts';
import type { ComparisonEntry } from '../shared/social.ts';
import { observationStatus, socialMessages } from '../shared/socialMessages.ts';
import type { CharacterKey, Locale, Runtime } from '../shared/types.ts';
import { EquipmentPanel, StatisticsPanel, TalentsPanel } from './Details.tsx';
import { RealmPicker } from './RealmPicker.tsx';
import { Icon } from './Icon.tsx';

export function ComparisonWorkspace({runtime,locale}:{runtime:Runtime|null;locale:Locale}) {
  const t=socialMessages(locale), standard=dictionary(locale);
  const initial=useRef(comparisonFromSearch(location.search));
  const [rows,setRows]=useState<CharacterKey[]>(initial.current.length?initial.current:[{region:'eu',realm:'',name:''}]);
  const [keys,setKeys]=useState<CharacterKey[]|null>(initial.current.length?initial.current:null);
  const [invalid,setInvalid]=useState<string|null>(null),[exportError,setExportError]=useState(''),[exporting,setExporting]=useState(false);
  const comparison=useComparison(keys,locale);
  const operation=JSON.stringify([keys,locale]);const current=useRef(operation);current.current=operation;
  const controller=useRef<AbortController|null>(null);
  useEffect(()=>{setExporting(false);setExportError('');return()=>controller.current?.abort()},[operation]);
  useEffect(()=>{const pop=()=>{const next=comparisonFromSearch(location.search);setRows(next.length?next:[{region:'eu',realm:'',name:''}]);setKeys(next.length?next:null)};window.addEventListener('popstate',pop);return()=>window.removeEventListener('popstate',pop)},[]);
  const submit = (input: CharacterKey[]) => {
    const checked = comparisonKeys(input);
    if (checked.error) { setInvalid(checked.error === 'count' ? t.compareCount : checked.error === 'duplicate' ? t.compareDuplicate : t.compareIdentity); return; }
    setInvalid(null); setExportError(''); setRows(checked.keys); setKeys(checked.keys);
    const params = new URLSearchParams({ view: 'comparison', characters: JSON.stringify(checked.keys) });
    if (runtime?.raiderio_enabled || new URLSearchParams(location.search).get('source') === 'fixture') params.set('source', 'fixture');
    history.pushState(null, '', `?${params}`);
    comparison.reload();
  };
  const update=(index:number,value:Partial<CharacterKey>)=>setRows(previous=>previous.map((row,i)=>i===index?{...row,...value}:row));
  const exportResult=async()=>{if(!keys||exporting)return;const binding=operation;const abort=new AbortController();controller.current?.abort();controller.current=abort;setExporting(true);setExportError('');try{const fresh=await requestComparison(keys,locale,abort.signal);if(current.current!==binding||abort.signal.aborted)return;if(fresh.status!==200||!exportable(fresh.body.meta)){setExportError(t.exportUnavailable);comparison.reload();return}downloadJSON(fresh.body,'kipet-comparison.json')}catch(error){if(!abort.signal.aborted&&current.current===binding)setExportError(error instanceof ApiError&&error.status===429?t.cooldown:t.error)}finally{if(current.current===binding)setExporting(false)}};
  const result=comparison.result;
  const match=(value:boolean|null)=>value===null?t.unknown:value?t.yes:t.no;
  return <div className="social-workspace">
    <section className="hero hero-compact"><div className="hero-copy"><div className="eyebrow"><span className="gold-line"/>{t.compareTop}</div><h1>{t.compareTitle}</h1><p>{t.compareSubtitle}</p></div></section>
    <form className="panel social-panel comparison-form" onSubmit={event=>{event.preventDefault();submit(rows)}}>
      {rows.map((row,index)=><div className="comparison-input-row" key={index}><div className="field"><label htmlFor={`comparison-region-${index}`}>{standard.region} · {index+1}</label><select id={`comparison-region-${index}`} value={row.region} onChange={event=>update(index,{region:event.target.value as 'eu'|'us',realm:''})}><option value="eu">Europe · EU</option><option value="us">Americas · US</option></select></div><div className="field"><label htmlFor={`comparison-realm-${index}`}>{standard.realm} · {index+1}</label><RealmPicker id={`comparison-realm-${index}`} region={row.region} locale={locale} selected={row.realm?{slug:row.realm,name:row.realm}:null} onChange={realm=>update(index,{realm:realm?.slug??''})} t={standard} invalid={false}/></div><div className="field"><label htmlFor={`comparison-name-${index}`}>{t.name} · {index+1}</label><input id={`comparison-name-${index}`} value={row.name} onChange={event=>update(index,{name:event.target.value})} autoComplete="off"/></div><button type="button" className="button-secondary" aria-label={`${t.remove}: ${index+1}`} disabled={rows.length===1} onClick={()=>setRows(previous=>previous.filter((_,i)=>i!==index))}><Icon name="close" size={15}/></button></div>)}
      <div className="social-actions"><button type="button" className="button-secondary" disabled={rows.length>=5} onClick={()=>setRows(previous=>[...previous,{region:'eu',realm:'',name:''}])}>{t.add}</button><button type="submit" className="button-primary" disabled={comparison.busy}>{t.compare}</button></div>
      {invalid&&<p className="validation-message" role="alert">{invalid}</p>}
    </form>
    {runtime?.provider_mode==='fixture'&&<div className="demo-banner"><div><span className="demo-pill">{t.demo}</span><p>{locale==='ru-RU'?'Вымышленные персонажи для проверки сравнения.':'Invented characters for comparison testing.'}</p></div><div className="demo-actions">{runtime.demo_comparisons?.map((demo,i)=><button type="button" className="demo-button" key={i} onClick={()=>submit(demo)}>{t.demoComparison}<Icon name="arrow" size={13}/></button>)}</div></div>}
    {comparison.busy&&<div className="progress-banner" role="status"><Icon name="refresh" className="spinning" size={18}/>{t.compareLoading}</div>}
    {(comparison.error||exportError)&&<div className="error-banner" role="alert">{exportError|| (comparison.error?.status===400?t.compareIdentity:t.error)}</div>}
    {comparison.paused&&<div className="analysis-notice" role="status">{t.paused}</div>}
    {result&&<>
      <section className="panel social-panel"><div className="social-section-heading"><h2>{t.context}</h2><div className="social-actions"><button type="button" className="button-secondary" disabled={comparison.busy} onClick={comparison.reload}>{t.reload}</button><button type="button" className="button-secondary" disabled={exporting||comparison.busy||!exportable(result.meta)} onClick={()=>void exportResult()}>{exporting?t.exporting:t.export}</button></div></div><dl className="social-facts"><div><dt>{t.samePatch}</dt><dd>{match(result.data.compatibility.same_patch)}</dd></div><div><dt>{t.sameSpec}</dt><dd>{match(result.data.compatibility.same_specialization)}</dd></div><div><dt>{t.sameSeason}</dt><dd>{match(result.data.compatibility.mythic_plus_same_season)}</dd></div></dl><p className="social-note">{t.noCombat}</p><p className="social-note">{t.exportNote}</p></section>
      {result.data.entries.some(entry=>entry.status==='pending')&&<p className="social-note">{t.comparePending}</p>}
      <div className="comparison-columns">{result.data.entries.map((entry,index)=><ComparisonCard key={`${entry.key.region}/${entry.key.realm}/${entry.key.name}/${index}`} entry={entry} locale={locale}/>)}</div>
    </>}
  </div>
}
function ComparisonCard({entry,locale}:{entry:ComparisonEntry;locale:Locale}){
  const t=socialMessages(locale),standard=dictionary(locale),d=entry.data;
  return <article className="comparison-card panel social-panel"><div className="eyebrow">{entry.key.region.toUpperCase()} · {entry.key.realm}</div><h2>{d?.profile?.name??entry.key.name}</h2><span className={`badge ${entry.status==='fresh'?'teal':entry.status==='stale'?'amber':'muted'}`}>{observationStatus(entry.status,t)}</span><p className="social-note">{t.source}: {entry.meta.source==='fixture'?t.fixture:t.live} · {t.fetched}: {formatDate(entry.meta.fetched_at,locale)}</p>{!d?<p className="social-note">{t.entryUnavailable}</p>:<>
    <dl className="social-facts"><div><dt>{t.ilvl}</dt><dd>{number(d.profile?.equipped_item_level,locale)}</dd></div><div><dt>{t.class}</dt><dd>{d.profile?.character_class.name??'—'}</dd></div><div><dt>{t.spec}</dt><dd>{d.profile?.active_specialization?.name??'—'}</dd></div><div><dt>{t.patch}</dt><dd>{entry.meta.patch??'—'}</dd></div><div><dt>{t.mythic}</dt><dd>{number(d.mythic_plus?.rating,locale)}</dd></div><div><dt>{t.season}</dt><dd>{d.mythic_plus?.season?.name??d.mythic_plus?.season?.id??'—'}</dd></div></dl>
    <details className="comparison-detail"><summary>{t.equipment}</summary>{d.equipment?<EquipmentPanel result={{data:d.equipment,meta:entry.meta,request_id:''}} t={standard} locale={locale} loading={false} error={false}/>:<p className="social-note">{t.sectionUnknown}</p>}</details>
    <details className="comparison-detail"><summary>{t.statistics}</summary>{d.statistics?<StatisticsPanel result={{data:d.statistics,meta:entry.meta,request_id:''}} t={standard} locale={locale} loading={false} error={false}/>:<p className="social-note">{t.sectionUnknown}</p>}</details>
    <details className="comparison-detail"><summary>{t.talents}</summary>{d.talents?<TalentsPanel result={{data:d.talents,meta:entry.meta,request_id:''}} t={standard} locale={locale} loading={false} error={false}/>:<p className="social-note">{t.sectionUnknown}</p>}</details>
    <details className="comparison-detail"><summary>{t.mythic}</summary>{d.mythic_plus?.best_runs?.map((run,i)=><p className="social-note" key={i}>{run.dungeon.name??`#${run.dungeon.id}`} · +{run.keystone_level} · {run.within_time===null?standard.unknown:run.within_time?standard.progressTimed:standard.progressOvertime}</p>)??<p className="social-note">{t.sectionUnknown}</p>}</details>
    <details className="comparison-detail"><summary>{t.raids}</summary>{d.raids?.expansions?.flatMap(expansion=>expansion.instances??[]).map(raid=><div key={raid.id}><h3>{raid.name??`#${raid.id}`}</h3>{raid.modes?.map((mode,i)=><p className="social-note" key={i}>{mode.difficulty} · {number(mode.completed,locale)} / {number(mode.total,locale)}</p>)}</div>)??<p className="social-note">{t.sectionUnknown}</p>}</details>
  </>}<p className="social-note">{t.noRules}</p></article>
}
