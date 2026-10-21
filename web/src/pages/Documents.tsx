import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Document, ListResult, SavedView } from '../lib/types'
import { docsURL, plural } from '../lib/format'
import { Card, Confirm, Dropdown, Empty, Field, Loading, MenuButton, Modal, Segmented, Toggle, useMediaQuery, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { DocCard, DocRow } from '../components/docs'
import { ReviewsProvider } from '../components/Review'
import { Picker } from '../components/Picker'
import { Select } from '../components/Select'

const SORTS = [['-created', 'Newest first'], ['created', 'Oldest first'], ['-added', 'Recently added'], ['title', 'Title A–Z'], ['-title', 'Title Z–A'], ['asn', 'ASN'], ['-modified', 'Recently changed'], ['-pages', 'Most pages'], ['-size', 'Largest'], ['correspondent', 'Correspondent'], ['type', 'Type'], ['rank', 'Best match']]
const ALL_COLS = [['asn', 'ASN'], ['correspondent', 'Correspondent'], ['title', 'Title'], ['type', 'Type'], ['tags', 'Tags'], ['created', 'Created'], ['added', 'Added'], ['modified', 'Modified'], ['pages', 'Pages'], ['size', 'Size'], ['path', 'Storage path'], ['notes', 'Notes'], ['status', 'Status']]

/** The list parameters live in the URL so a search is a link. */
function useListParams() {
  const [sp, setSp] = useSearchParams()
  const get = (k: string) => sp.get(k) || ''
  const ints = (k: string) => (sp.get(k) || '').split(',').filter(Boolean).map(Number)
  const params = {
    q: get('q'), tags: ints('tags'), tags_none: ints('tags__none'), correspondent: get('correspondent'), document_type: get('document_type'), storage_path: get('storage_path'),
    created: get('created'), added: get('added'), inbox: get('inbox') === '1', untagged: get('untagged') === '1', favorite: get('favorite') === '1', status: get('status'), mime: get('mime'),
    sort: get('sort'), page: Number(get('page') || 1), view: get('view'), field: sp.getAll('field'), asn: get('asn'), shared: get('shared') === '1',
  }
  const set = (patch: Record<string, unknown>) => {
    const next = new URLSearchParams(sp)
    for (const [k, v] of Object.entries(patch)) {
      next.delete(k)
      if (v === undefined || v === null || v === '' || v === false || (Array.isArray(v) && v.length === 0)) continue
      if (Array.isArray(v)) { if (k === 'field') v.forEach(x => next.append(k, String(x))); else next.set(k, v.join(',')) }
      else next.set(k, v === true ? '1' : String(v))
    }
    if (!('page' in patch)) next.delete('page')
    setSp(next)
  }
  return { params, set, sp }
}

export default function Documents() {
  const { params, set, sp } = useListParams()
  const { tax, settings, stamp, reloadTax } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [data, setData] = useState<ListResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [sel, setSel] = useState<Set<number>>(new Set())
  const [view, setView] = useState<'table' | 'cards' | 'list'>(() => (localStorage.getItem('pd-view') as 'table' | 'cards') || settings?.default_view || 'table')
  const [cols, setCols] = useState<string[]>(() => JSON.parse(localStorage.getItem('pd-cols') || '["asn","correspondent","title","type","tags","created","pages"]'))
  const [wideFilters, setWideFilters] = useState(() => localStorage.getItem('pd-filters') !== '0')
  // on phones and narrow windows the filters are a sheet over the list, closed until asked for
  const narrow = useMediaQuery('(max-width: 1099px)')
  const [sheet, setSheet] = useState(false)
  const filters = narrow ? sheet : wideFilters
  // a wide table does not fit a phone: it shows as the list there
  const phone = useMediaQuery('(max-width: 767px)')
  const shown = phone && view === 'table' ? 'list' : view
  const setFilters = (fn: (f: boolean) => boolean) => { if (narrow) setSheet(fn); else setWideFilters(fn) }
  const [saveView, setSaveView] = useState(false)
  const [help, setHelp] = useState(false)
  const [q, setQ] = useState(params.q)
  const lastAnchor = useRef<number | null>(null)
  const pageSize = settings?.page_size || 50
  useEffect(() => { localStorage.setItem('pd-view', view) }, [view])
  useEffect(() => { localStorage.setItem('pd-cols', JSON.stringify(cols)) }, [cols])
  useEffect(() => { localStorage.setItem('pd-filters', wideFilters ? '1' : '0') }, [wideFilters])
  useEffect(() => { setQ(params.q) }, [params.q])
  const qs = sp.toString()
  useEffect(() => {
    let stop = false
    setLoading(true)
    const p = new URLSearchParams(sp)
    p.set('limit', String(pageSize)); p.set('offset', String((params.page - 1) * pageSize))
    p.delete('view')
    api.get<ListResult>(`${V1}/documents?${p.toString()}`).then(r => { if (!stop) { setData(r); setSel(new Set()) } }).catch(e => toast((e as Error).message, 'err')).finally(() => { if (!stop) setLoading(false) })
    return () => { stop = true }
  }, [qs, pageSize, stamp]) // eslint-disable-line react-hooks/exhaustive-deps
  const savedView = useMemo(() => tax.saved_views.find(v => String(v.id) === params.view), [tax.saved_views, params.view])
  const total = data?.count || 0
  const pages = Math.max(1, Math.ceil(total / pageSize))
  const facets = data?.facets
  const activeFilters = [params.tags.length, params.tags_none.length, params.correspondent, params.document_type, params.storage_path, params.created, params.added, params.inbox, params.untagged, params.favorite, params.status, params.mime, params.field.length, params.asn, params.shared].filter(Boolean).length

  const submit = (e: React.FormEvent) => { e.preventDefault(); set({ q }) }
  const toggleSel = (id: number, e: React.MouseEvent) => {
    e.preventDefault()
    setSel(s => {
      const n = new Set(s)
      if (e.shiftKey && lastAnchor.current != null && data) {
        const ids = data.results.map(d => d.id)
        const a = ids.indexOf(lastAnchor.current), b = ids.indexOf(id)
        if (a >= 0 && b >= 0) { for (let i = Math.min(a, b); i <= Math.max(a, b); i++) n.add(ids[i]); return n }
      }
      if (n.has(id)) n.delete(id); else n.add(id)
      lastAnchor.current = id
      return n
    })
  }
  const allSel = data ? data.results.length > 0 && data.results.every(d => sel.has(d.id)) : false
  const bulk = async (body: Record<string, unknown>, msg: string) => {
    try {
      const r = await api.post<{ done: number; failed: number }>(`${V1}/documents/bulk`, { ids: Array.from(sel), ...body })
      toast(`${msg}: ${r.done} done${r.failed ? `, ${r.failed} failed` : ''}`)
      setSel(new Set()); setData(null); set({ page: params.page }); reloadTax()
    } catch (e) { toast((e as Error).message, 'err') }
  }
  const openFirst = (d: Document) => navigate(`/documents/${d.id}`)

  return (
    <div className={`docs-page ${filters ? 'with-filters' : ''}`}>
      <aside className="filters">
        <div className="filters-head"><h2>Filters</h2><span className="row">{activeFilters > 0 && <button type="button" className="btn ghost sm" onClick={() => navigate(docsURL({ q: params.q, sort: params.sort }))}>Clear {activeFilters}</button>}<button type="button" className="btn sm filters-close" onClick={() => setFilters(() => false)}>Done</button></span></div>
        <FilterGroup title="Quick">
          <div className="chips">
          <Chip on={params.inbox} onClick={() => set({ inbox: !params.inbox })}><Icon name="inbox" size={13} />Inbox</Chip>
          <Chip on={params.favorite} onClick={() => set({ favorite: !params.favorite })}><Icon name="star" size={13} />Favourites</Chip>
          <Chip on={params.shared} onClick={() => set({ shared: !params.shared })}><Icon name="share" size={13} />Shared with me</Chip>
          <Chip on={params.untagged} onClick={() => set({ untagged: !params.untagged })} count={facets?.untagged}>Untagged</Chip>
          <Chip on={params.correspondent === 'none'} onClick={() => set({ correspondent: params.correspondent === 'none' ? '' : 'none' })} count={facets?.no_correspondent}>No correspondent</Chip>
          <Chip on={params.document_type === 'none'} onClick={() => set({ document_type: params.document_type === 'none' ? '' : 'none' })} count={facets?.no_type}>No type</Chip>
          {(facets?.status.failed || params.status === 'failed') ? <Chip on={params.status === 'failed'} onClick={() => set({ status: params.status === 'failed' ? '' : 'failed' })} count={facets?.status.failed} warn>Failed OCR</Chip> : null}
          </div>
        </FilterGroup>
        <FilterGroup title="Tags" count={params.tags.length}>
          <FacetList items={tax.tags.map(t => ({ id: t.id, name: t.name, color: t.color }))} counts={facets?.tags} selected={params.tags} excluded={params.tags_none}
            onToggle={id => set({ tags: params.tags.includes(id) ? params.tags.filter(x => x !== id) : [...params.tags, id], tags__none: params.tags_none.filter(x => x !== id) })}
            onExclude={id => set({ tags__none: params.tags_none.includes(id) ? params.tags_none.filter(x => x !== id) : [...params.tags_none, id], tags: params.tags.filter(x => x !== id) })} />
        </FilterGroup>
        <FilterGroup title="Correspondent" count={params.correspondent && params.correspondent !== 'none' ? 1 : 0}>
          <FacetList items={tax.correspondents} counts={facets?.correspondents} selected={params.correspondent.split(',').filter(Boolean).map(Number)} onToggle={id => set({ correspondent: toggleCSV(params.correspondent, id) })} />
        </FilterGroup>
        <FilterGroup title="Document type" count={params.document_type && params.document_type !== 'none' ? 1 : 0}>
          <FacetList items={tax.document_types} counts={facets?.document_types} selected={params.document_type.split(',').filter(Boolean).map(Number)} onToggle={id => set({ document_type: toggleCSV(params.document_type, id) })} />
        </FilterGroup>
        {tax.storage_paths.length > 0 && <FilterGroup title="Storage path" count={params.storage_path ? 1 : 0}>
          <FacetList items={tax.storage_paths} counts={facets?.storage_paths} selected={params.storage_path.split(',').filter(Boolean).map(Number)} onToggle={id => set({ storage_path: toggleCSV(params.storage_path, id) })} />
        </FilterGroup>}
        <FilterGroup title="Dates">
          <Field label="Document date" help="2024, 2024-05, 2024-05-03, 2023..2024, >2024-01-01, today, week, month, year, 30d"><input value={params.created} onChange={e => set({ created: e.target.value })} placeholder="any" /></Field>
          <Field label="Added"><input value={params.added} onChange={e => set({ added: e.target.value })} placeholder="any" /></Field>
        </FilterGroup>
        <FilterGroup title="More">
          <Field label="ASN"><input value={params.asn} onChange={e => set({ asn: e.target.value })} placeholder="number" /></Field>
          <Field group label="File type"><Select value={params.mime} onChange={v => set({ mime: v })} placeholder="any" clearLabel="any" options={[{ value: 'application/pdf', label: 'PDF' }, { value: 'image/', label: 'Images' }, { value: 'application/vnd', label: 'Office' }, { value: 'text/', label: 'Text' }, { value: 'message/', label: 'E-mail' }]} /></Field>
          {tax.custom_fields.length > 0 && <Field group label="Custom field has a value"><Select value="" onChange={v => { if (v) set({ field: [...params.field, v] }) }} placeholder="add…" options={tax.custom_fields.map(f => ({ value: String(f.id), label: f.name, hint: f.data_type }))} />
            {params.field.length > 0 && <div className="chips">{params.field.map(f => <Chip key={f} on onClick={() => set({ field: params.field.filter(x => x !== f) })}>{tax.custom_fields.find(x => String(x.id) === f.split(':')[0])?.name}{f.includes(':') ? ` = ${f.split(':')[1]}` : ''}<Icon name="x" size={12} /></Chip>)}</div>}</Field>}
        </FilterGroup>
      </aside>
      <div className="docs-main">
        <div className="docs-toolbar">
          <button type="button" className={`btn icon ${filters ? 'on' : ''}`} onClick={() => setFilters(f => !f)} title="Filters" aria-label="Filters" aria-pressed={filters}><Icon name="filter" size={16} />{activeFilters > 0 && <span className="count">{activeFilters}</span>}</button>
          <form className="docs-search" onSubmit={submit}>
            <Icon name="search" size={15} />
            <input value={q} onChange={e => setQ(e.target.value)} placeholder="Search text, title, notes…" aria-label="Search" />
            {q && <button type="button" className="x" onClick={() => { setQ(''); set({ q: '' }) }} aria-label="Clear"><Icon name="x" size={15} /></button>}
            <button type="button" className="btn ghost icon-only sm" onClick={() => setHelp(true)} title="Search syntax" aria-label="Search syntax"><Icon name="help" size={15} /></button>
          </form>
          <Select value={params.sort || (params.q ? 'rank' : settings?.default_sort || '-created')} onChange={v => set({ sort: v })} className="sort" ariaLabel="Sort" options={SORTS.filter(([v]) => v !== 'rank' || params.q).map(([v, l]) => ({ value: v, label: l }))} />
          <Segmented size="sm" value={shown} onChange={setView} label="View" options={[{ value: 'table', label: <Icon name="table" size={15} />, title: 'Table' }, { value: 'cards', label: <Icon name="grid" size={15} />, title: 'Cards' }, { value: 'list', label: <Icon name="list" size={15} />, title: 'List' }]} />
          {shown === 'table' && <Dropdown align="right" label="Columns" button={<button type="button" className="btn icon" title="Columns" aria-label="Columns"><Icon name="columns" size={16} /></button>}>
            {ALL_COLS.map(([k, l]) => <label key={k} className="menu-check" onClick={e => e.stopPropagation()}><input type="checkbox" checked={cols.includes(k)} onChange={() => setCols(c => c.includes(k) ? c.filter(x => x !== k) : ALL_COLS.map(([x]) => x).filter(x => c.includes(x) || x === k))} /> {l}</label>)}
            {tax.custom_fields.map(f => <label key={f.id} className="menu-check" onClick={e => e.stopPropagation()}><input type="checkbox" checked={cols.includes('cf:' + f.id)} onChange={() => setCols(c => c.includes('cf:' + f.id) ? c.filter(x => x !== 'cf:' + f.id) : [...c, 'cf:' + f.id])} /> {f.name}</label>)}
          </Dropdown>}
          <button type="button" className={`btn ${savedView ? 'on' : ''}`} onClick={() => setSaveView(true)} title="Save this search as a view"><Icon name="bookmark" size={15} />{savedView ? savedView.name : 'Save view'}</button>
        </div>
        {sel.size > 0 && <BulkBar count={sel.size} onClear={() => setSel(new Set())} onAll={() => setSel(new Set(data?.results.map(d => d.id)))} allSel={allSel} bulk={bulk} ids={Array.from(sel)} reloadAll={() => { setData(null); set({ page: params.page }) }} />}
        {loading && !data ? <Loading /> : !data || data.results.length === 0 ? <Empty title={params.q || activeFilters ? 'Nothing matches' : 'No documents yet'}>{params.q || activeFilters ? 'Try fewer filters or other words. Searches match word beginnings, and fall back to substrings.' : 'Upload a scan or drop a file into the consume folder.'}</Empty> : (
          <>
            <div className="muted small list-count">{plural(total, 'document')}{params.q && ` for “${params.q}”`}{loading && ' …'}</div>
            <ReviewsProvider ids={data.results.map(d => d.id)}>
            {shown === 'cards' && <div className="doc-grid">{data.results.map(d => <DocCard key={d.id} d={d} selected={sel.has(d.id)} onSelect={e => toggleSel(d.id, e)} />)}</div>}
            {shown === 'list' && <div className="doc-list">{data.results.map(d => <DocCard key={d.id} d={d} selected={sel.has(d.id)} onSelect={e => toggleSel(d.id, e)} onOpen={() => openFirst(d)} />)}</div>}
            {shown === 'table' && <div className="table-wrap"><table className="table docs-table">
              <thead><tr><th className="sel"><input type="checkbox" checked={allSel} onChange={() => setSel(allSel ? new Set() : new Set(data.results.map(d => d.id)))} /></th><th />{cols.map(c => <th key={c} className={['asn', 'pages', 'notes'].includes(c) ? 'num' : ''}><SortHead col={c} sort={params.sort} onSort={s => set({ sort: s })} label={ALL_COLS.find(([k]) => k === c)?.[1] || tax.custom_fields.find(f => 'cf:' + f.id === c)?.name || c} /></th>)}</tr></thead>
              <tbody>{data.results.map(d => <DocRow key={d.id} d={d} cols={cols} selected={sel.has(d.id)} onSelect={e => toggleSel(d.id, e)} />)}</tbody>
            </table></div>}
            </ReviewsProvider>
            {pages > 1 && <div className="pager"><button type="button" className="btn sm" disabled={params.page <= 1} onClick={() => set({ page: params.page - 1 })}><Icon name="chevron-left" size={14} />Previous</button><span className="muted small">Page {params.page} of {pages}</span><button type="button" className="btn sm" disabled={params.page >= pages} onClick={() => set({ page: params.page + 1 })}>Next<Icon name="chevron-right" size={14} /></button></div>}
          </>
        )}
      </div>
      {saveView && <SaveViewModal existing={savedView} query={Object.fromEntries(Array.from(sp.entries()).filter(([k]) => !['page', 'view'].includes(k)))} onClose={() => setSaveView(false)} onSaved={v => { setSaveView(false); reloadTax(); set({ view: v.id }) }} />}
      {help && <Modal title="Search syntax" onClose={() => setHelp(false)}>
        <table className="table compact"><tbody>
          <tr><td><code>invoice ikea</code></td><td>both words (word beginnings match: <code>inv</code> finds invoice)</td></tr>
          <tr><td><code>"amount due"</code></td><td>exact phrase</td></tr>
          <tr><td><code>-draft</code></td><td>without this word</td></tr>
          <tr><td><code>tag:tax</code> <code>#tax</code> <code>-tag:paid</code></td><td>with / without a tag</td></tr>
          <tr><td><code>from:ikea</code> <code>type:invoice</code> <code>path:2024</code></td><td>correspondent, document type, storage path (<code>none</code> for unset)</td></tr>
          <tr><td><code>created:2024</code> <code>created:2024-05</code> <code>created:2023..2024</code> <code>created:&gt;2024-01-01</code> <code>created:30d</code></td><td>document date; <code>added:</code> the same for when it came in</td></tr>
          <tr><td><code>asn:12</code> <code>asn:10..20</code></td><td>archive serial number</td></tr>
          <tr><td><code>is:inbox</code> <code>is:untagged</code> <code>is:favorite</code> <code>is:failed</code> <code>is:pdf</code> <code>is:image</code></td><td>states</td></tr>
          <tr><td><code>has:notes</code> <code>has:asn</code></td><td>…</td></tr>
          <tr><td><code>field:amount=120</code></td><td>a custom field containing a value</td></tr>
          <tr><td><code>title:lease</code></td><td>words in the title only</td></tr>
        </tbody></table>
        <p className="muted small mt">Everything else is full text over the OCR text, title, notes, tags and names. When no whole word matches, substrings are tried, so <code>nvoic</code> still finds invoices.</p>
      </Modal>}
    </div>
  )
}

function toggleCSV(cur: string, id: number): string {
  const l = cur.split(',').filter(x => x && x !== 'none').map(Number)
  return (l.includes(id) ? l.filter(x => x !== id) : [...l, id]).join(',')
}

function SortHead({ col, sort, onSort, label }: { col: string; sort: string; onSort: (s: string) => void; label: string }) {
  const key = { type: 'type', correspondent: 'correspondent', path: '', tags: '', status: '' }[col] ?? col
  if (!key || col.startsWith('cf:')) return <>{label}</>
  const active = sort === key || sort === '-' + key
  return <button type="button" className={`th-sort ${active ? 'on' : ''}`} onClick={() => onSort(sort === '-' + key ? key : '-' + key)}>{label}{active && <Icon name={sort.startsWith('-') ? 'chevron-down' : 'chevron-up'} size={12} />}</button>
}

function FilterGroup({ title, count, children }: { title: string; count?: number; children: React.ReactNode }) {
  const key = 'pd-fg:' + title
  const [open, setOpen] = useState(() => localStorage.getItem(key) !== '0')
  useEffect(() => { localStorage.setItem(key, open ? '1' : '0') }, [open, key])
  return <div className="fgroup"><button type="button" className="fgroup-head" aria-expanded={open} onClick={() => setOpen(o => !o)}><span>{title}{count ? <span className="count">{count}</span> : null}</span><Icon name={open ? 'chevron-down' : 'chevron-right'} size={14} /></button>{open && <div className="fgroup-body">{children}</div>}</div>
}

function Chip({ on, onClick, children, count, warn }: { on?: boolean; onClick: () => void; children: React.ReactNode; count?: number; warn?: boolean }) {
  return <button type="button" className={`chip ${on ? 'active' : ''} ${warn ? 'warn' : ''}`} onClick={onClick}>{children}{count != null && count > 0 && <small>{count}</small>}</button>
}

function FacetList({ items, counts, selected, excluded = [], onToggle, onExclude }: { items: { id: number; name: string; color?: string }[]; counts?: Record<string, number>; selected: number[]; excluded?: number[]; onToggle: (id: number) => void; onExclude?: (id: number) => void }) {
  const [q, setQ] = useState('')
  const [more, setMore] = useState(false)
  const list = useMemo(() => {
    const t = q.toLowerCase()
    return items.filter(i => !t || i.name.toLowerCase().includes(t)).sort((a, b) => Number(selected.includes(b.id) || excluded.includes(b.id)) - Number(selected.includes(a.id) || excluded.includes(a.id)) || ((counts?.[b.id] || 0) - (counts?.[a.id] || 0)) || a.name.localeCompare(b.name))
  }, [items, q, selected, excluded, counts])
  if (items.length === 0) return <p className="muted small">None yet.</p>
  const shown = more ? list : list.slice(0, 8)
  return (
    <div className="facet">
      {items.length > 8 && <input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter…" className="facet-q" />}
      {shown.map(i => { const n = counts?.[i.id]; const on = selected.includes(i.id); const ex = excluded.includes(i.id); return (
        <div key={i.id} className={`facet-row ${on ? 'on' : ''} ${ex ? 'ex' : ''} ${!n && !on && !ex ? 'zero' : ''}`}>
          <button type="button" className="facet-btn" onClick={() => onToggle(i.id)}>{i.color && <span className="swatch" style={{ background: i.color }} />}<span className="ellipsis">{i.name}</span>{n != null && n > 0 && <small>{n}</small>}</button>
          {onExclude && <button type="button" className="facet-ex" title={ex ? 'Stop excluding' : 'Exclude'} onClick={() => onExclude(i.id)} aria-label={ex ? 'Stop excluding' : 'Exclude'}><Icon name={ex ? 'x-circle' : 'minus'} size={13} /></button>}
        </div>
      ) })}
      {list.length > 8 && <button type="button" className="link-btn muted small" onClick={() => setMore(m => !m)}>{more ? 'Show fewer' : `Show all ${list.length}`}</button>}
    </div>
  )
}

function BulkBar({ count, onClear, onAll, allSel, bulk, ids, reloadAll }: { count: number; onClear: () => void; onAll: () => void; allSel: boolean; bulk: (body: Record<string, unknown>, msg: string) => Promise<void>; ids: number[]; reloadAll: () => void }) {
  const { tax, reloadTax } = useApp()
  const [del, setDel] = useState(false)
  const [merge, setMerge] = useState(false)
  const [tagsOp, setTagsOp] = useState<'add' | 'remove' | null>(null)
  const [pick, setPick] = useState<number[]>([])
  const [which, setWhich] = useState<'correspondent' | 'document_type' | 'storage_path' | null>(null)
  const [one, setOne] = useState<number | null>(null)
  const items = which === 'correspondent' ? tax.correspondents : which === 'document_type' ? tax.document_types : tax.storage_paths
  const create = (kind: 'tags' | 'correspondents' | 'document_types' | 'storage_paths') => async (name: string) => { const o = await api.post<{ id: number; name: string; color?: string }>(`${V1}/${kind}`, { name }); await reloadTax(); return o }
  return (
    <div className="bulk-bar">
      <span className="grow"><b>{count}</b> selected · <button type="button" className="link-btn" onClick={onAll}>{allSel ? 'all on this page' : 'select page'}</button> · <button type="button" className="link-btn" onClick={onClear}>clear</button></span>
      <button type="button" className="btn sm" onClick={() => { setTagsOp('add'); setPick([]) }}><Icon name="plus" size={14} />Tags</button>
      <button type="button" className="btn sm" onClick={() => { setTagsOp('remove'); setPick([]) }}><Icon name="minus" size={14} />Tags</button>
      <button type="button" className="btn sm" onClick={() => { setWhich('correspondent'); setOne(null) }}>Correspondent</button>
      <button type="button" className="btn sm" onClick={() => { setWhich('document_type'); setOne(null) }}>Type</button>
      <button type="button" className="btn sm" onClick={() => { setWhich('storage_path'); setOne(null) }}>Path</button>
      <MenuButton label="More actions" className="btn sm" align="right" width={250} items={[
        { label: 'Remove inbox tags', icon: 'inbox', onSelect: () => bulk({ op: 'remove_inbox' }, 'Inbox tags removed') },
        { label: 'Favourite', icon: 'star', onSelect: () => bulk({ op: 'set_favorite', favorite: true }, 'Marked favourite') },
        { label: 'Unfavourite', icon: 'x', onSelect: () => bulk({ op: 'set_favorite', favorite: false }, 'Unmarked') },
        'sep',
        { label: 'Run OCR again', icon: 'refresh', onSelect: () => bulk({ op: 'reprocess' }, 'Queued for OCR') },
        { label: 'Force OCR (replace text layer)', icon: 'scan', onSelect: () => bulk({ op: 'reprocess', mode: 'force' }, 'Queued for OCR') },
      ]}>More<Icon name="chevron-down" size={14} /></MenuButton>
      {count >= 2 && <button type="button" className="btn sm" onClick={() => setMerge(true)}>Merge</button>}
      <button type="button" className="btn sm danger-text" onClick={() => setDel(true)}>Delete</button>
      {merge && <MergeModal ids={ids} onClose={() => setMerge(false)} onDone={() => { setMerge(false); onClear(); reloadAll() }} />}
      {del && <Confirm title={`Move ${plural(count, 'document')} to the trash?`} message="They can be restored from the trash for a while." onCancel={() => setDel(false)} onConfirm={() => { setDel(false); bulk({ op: 'delete' }, 'Moved to trash') }} label="Delete" />}
      {tagsOp && <Modal title={tagsOp === 'add' ? 'Add tags' : 'Remove tags'} onClose={() => setTagsOp(null)}>
        <Picker multi options={tax.tags} value={pick} onChange={v => setPick(v as number[])} placeholder="Tags…" onCreate={tagsOp === 'add' ? create('tags') : undefined} />
        <div className="form-actions"><button type="button" className="btn" onClick={() => setTagsOp(null)}>Cancel</button><button type="button" className="btn primary" disabled={pick.length === 0} onClick={() => { const op = tagsOp; setTagsOp(null); bulk(op === 'add' ? { op: 'add_tag', add_tags: pick } : { op: 'remove_tag', remove_tags: pick }, op === 'add' ? 'Tags added' : 'Tags removed') }}>Apply</button></div>
      </Modal>}
      {which && <Modal title={`Set ${which.replace('_', ' ')}`} onClose={() => setWhich(null)}>
        <Picker options={items} value={one} onChange={v => setOne(v as number | null)} placeholder="Choose… (empty = clear)" onCreate={create(which === 'correspondent' ? 'correspondents' : which === 'document_type' ? 'document_types' : 'storage_paths')} />
        <div className="form-actions"><button type="button" className="btn" onClick={() => setWhich(null)}>Cancel</button><button type="button" className="btn primary" onClick={() => { const w = which; setWhich(null); bulk({ op: 'set_' + w, [w]: one }, 'Updated') }}>Apply</button></div>
      </Modal>}
    </div>
  )
}

function MergeModal({ ids, onClose, onDone }: { ids: number[]; onClose: () => void; onDone: () => void }) {
  const { toast } = useToast()
  const navigate = useNavigate()
  const [title, setTitle] = useState('')
  const [del, setDel] = useState(false)
  const [busy, setBusy] = useState(false)
  const go = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { const d = await api.post<Document>(`${V1}/documents/merge`, { ids, title, delete_originals: del }); toast('Merged; OCR running'); onDone(); navigate(`/documents/${d.id}`) } catch (err) { toast((err as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={`Merge ${ids.length} documents`} onClose={onClose}>
      <form onSubmit={go} className="grid" style={{ gap: 14 }}>
        <p className="muted small">Their PDFs are joined in the order they were selected into a new document with the first one's details and every tag. The originals are kept unless you say otherwise.</p>
        <Field label="Title of the new document"><input value={title} onChange={e => setTitle(e.target.value)} placeholder="(first title) (merged)" autoFocus /></Field>
        <Toggle checked={del} onChange={setDel} label="Move the originals to the trash" />
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary" disabled={busy}>Merge</button></div>
      </form>
    </Modal>
  )
}

function SaveViewModal({ existing, query, onClose, onSaved }: { existing?: SavedView; query: Record<string, string>; onClose: () => void; onSaved: (v: SavedView) => void }) {
  const { toast } = useToast()
  const [name, setName] = useState(existing?.name || '')
  const [dash, setDash] = useState(existing?.on_dashboard || false)
  const [side, setSide] = useState(existing?.in_sidebar ?? true)
  const [asNew, setAsNew] = useState(!existing)
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const body = { name, query, on_dashboard: dash, in_sidebar: side }
      const v = existing && !asNew ? await api.put<SavedView>(`${V1}/saved_views/${existing.id}`, body) : await api.post<SavedView>(`${V1}/saved_views`, body)
      toast('View saved'); onSaved(v)
    } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title={existing && !asNew ? 'Update view' : 'Save view'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} autoFocus required /></Field>
        <Toggle checked={side} onChange={setSide} label="Show in the sidebar" />
        <Toggle checked={dash} onChange={setDash} label="Show on the dashboard" />
        {existing && <Toggle checked={asNew} onChange={setAsNew} label={`Save as a new view instead of updating “${existing.name}”`} />}
        <p className="muted small">Saves the current search, filters and sort: {Object.entries(query).map(([k, v]) => `${k}=${v}`).join(' ') || 'everything'}</p>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

export { Card }
