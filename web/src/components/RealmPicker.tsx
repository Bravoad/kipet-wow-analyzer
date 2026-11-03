import { useEffect, useRef, useState } from 'react';
import { get } from '../shared/api.ts';
import type { Locale, Realm, Region } from '../shared/types.ts';
import type { Dictionary } from '../i18n.ts';
import { Icon } from './Icon.tsx';

export type RealmSelection = Pick<Realm, 'slug' | 'name'>;
export function RealmPicker({ region, locale, selected, onChange, t, invalid, id = 'realm', errorID = 'search-validation' }: { region: Region; locale: Locale; selected: RealmSelection | null; onChange: (realm: RealmSelection | null) => void; t: Dictionary; invalid: boolean; id?: string; errorID?: string }) {
  const [query, setQuery] = useState(selected?.name ?? '');
  const [items, setItems] = useState<Realm[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const [active, setActive] = useState(0);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => { if (selected) setQuery(selected.name); }, [selected]);
  useEffect(() => { setQuery(selected?.name ?? ''); }, [region]);
  useEffect(() => {
    const controller = new AbortController();
    const timer = setTimeout(() => {
      setLoading(true); setFailed(false);
      const q = selected && query === selected.name ? '' : query;
      void get<{ items: Realm[]; next_cursor: string | null }>(`/api/v1/meta/realms?region=${region}&locale=${locale}&limit=30&q=${encodeURIComponent(q)}`, controller.signal)
        .then(result => { setItems(result.body.data.items); setActive(0); })
        .catch(() => { if (!controller.signal.aborted) { setItems([]); setFailed(true); } })
        .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    }, 180);
    return () => { clearTimeout(timer); controller.abort(); };
  }, [region, locale, query, selected]);
  useEffect(() => {
    const close = (event: PointerEvent) => { if (!ref.current?.contains(event.target as Node)) setOpen(false); };
    document.addEventListener('pointerdown', close);
    return () => document.removeEventListener('pointerdown', close);
  }, []);
  const choose = (item: Realm) => { onChange(item); setQuery(item.name); setOpen(false); };
  return <div className="realm-picker" ref={ref} onBlur={event => { if (!ref.current?.contains(event.relatedTarget as Node)) setOpen(false); }}>
    <div className="input-icon"><Icon name="globe" size={17}/><input id={id} role="combobox" aria-autocomplete="list" aria-controls={`${id}-options`} aria-expanded={open} aria-activedescendant={open && items.length ? `${id}-option-${active}` : undefined} aria-invalid={invalid} aria-describedby={invalid ? errorID : undefined} autoComplete="off" value={query} placeholder={t.realmPlaceholder}
      onFocus={() => setOpen(true)} onChange={event => { setQuery(event.target.value); onChange(null); setOpen(true); setActive(0); }}
      onKeyDown={event => {
        if (event.key === 'Escape') { setOpen(false); return; }
        if (event.key === 'ArrowDown') { event.preventDefault(); setOpen(true); setActive(value => Math.min(value + 1, Math.max(0, items.length - 1))); }
        if (event.key === 'ArrowUp') { event.preventDefault(); setActive(value => Math.max(value - 1, 0)); }
        if (event.key === 'Enter' && open && items[active]) { event.preventDefault(); choose(items[active]); }
      }} /><Icon name="chevron" size={14}/></div>
    {open && <div className="realm-menu" id={`${id}-options`} role="listbox" aria-label={t.realm}>
      {loading ? <div className="realm-message">{t.realmsLoading}</div> : failed ? <div className="realm-message">{t.catalogError}</div> : items.length ? items.map((item, index) => <button id={`${id}-option-${index}`} type="button" tabIndex={-1} role="option" aria-selected={item.slug === selected?.slug} className={active === index ? 'is-active' : ''} key={item.id} onPointerMove={() => setActive(index)} onClick={() => choose(item)}><span>{item.name}</span><span className="realm-region">{item.region.toUpperCase()}</span></button>) : <div className="realm-message">{t.realmsEmpty}</div>}
    </div>}
  </div>;
}
