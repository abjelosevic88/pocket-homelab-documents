import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Document, ListResult } from '../lib/types'
import { docsURL, fmtDate, inkFor, snippetParts } from '../lib/format'
import { TagList, Thumb } from './docs'

// The search box in the top bar: results appear under it while you type.
// ↑↓ move, ↵ opens, ⌘↵ / "See all" opens the full list, Esc closes.

type Item =
  | { kind: 'doc'; doc: Document }
  | { kind: 'filter'; label: string; sub: string; color?: string; to: string }
  | { kind: 'complete'; label: string; sub: string; color?: string; value: string }
  | { kind: 'recent'; q: string }
  | { kind: 'all'; q: string; count: number }

const RECENT_KEY = 'pd-recent-searches'
const recent = (): string[] => { try { return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]') } catch { return [] } }
const remember = (q: string) => {
  q = q.trim()
  if (!q) return
  localStorage.setItem(RECENT_KEY, JSON.stringify([q, ...recent().filter(x => x !== q)].slice(0, 6)))
}

const FIELDS: { key: string; kind: 'tags' | 'correspondents' | 'document_types' | 'storage_paths'; label: string }[] = [
  { key: 'tag', kind: 'tags', label: 'Tag' }, { key: '#', kind: 'tags', label: 'Tag' }, { key: 'from', kind: 'correspondents', label: 'From' },
  { key: 'correspondent', kind: 'correspondents', label: 'From' }, { key: 'type', kind: 'document_types', label: 'Type' }, { key: 'path', kind: 'storage_paths', label: 'Path' },
]
const quote = (s: string) => (/\s/.test(s) ? `"${s}"` : s)

/** Marks the words of the query inside a title. */
function Highlight({ text, q }: { text: string; q: string }) {
  const words = q.split(/\s+/).map(w => w.replace(/^[-#"]+|"+$/g, '')).filter(w => w.length > 1 && !w.includes(':'))
  if (!words.length) return <>{text}</>
  const fold = (s: string) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase()
  const folded = fold(text)
  const marks: [number, number][] = []
  for (const w of words.map(fold)) {
    let i = 0
    while ((i = folded.indexOf(w, i)) >= 0) { marks.push([i, i + w.length]); i += w.length }
  }
  if (!marks.length || folded.length !== text.length) return <>{text}</>
  marks.sort((a, b) => a[0] - b[0])
  const out: React.ReactNode[] = []
  let last = 0
  marks.forEach(([a, b], k) => { if (a < last) return; if (a > last) out.push(text.slice(last, a)); out.push(<mark key={k}>{text.slice(a, b)}</mark>); last = b })
  out.push(text.slice(last))
  return <>{out}</>
}

export interface QuickSearchHandle { focus: () => void }

export const QuickSearch = forwardRef<QuickSearchHandle>(function QuickSearch(_, ref) {
  const { tax, stamp } = useApp()
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [open, setOpen] = useState(false)
  const [res, setRes] = useState<ListResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [idx, setIdx] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const seq = useRef(0)
  useImperativeHandle(ref, () => ({ focus: () => { input.current?.focus(); input.current?.select(); setOpen(true) } }))

  // the word being typed, for tag:/from:/type:/path: completion
  const token = useMemo(() => {
    const m = /(^|\s)((?:tag|from|correspondent|type|path):|#)("?)([^\s"]*)$/i.exec(q)
    if (!m) return null
    const field = FIELDS.find(f => f.key === m[2].replace(':', '').toLowerCase())
    return field ? { field, partial: m[4], start: m.index + m[1].length } : null
  }, [q])

  useEffect(() => {
    const text = q.trim()
    if (!open || text.length < 2 || token) { setRes(null); setLoading(false); return }
    const my = ++seq.current
    setLoading(true)
    const t = setTimeout(() => {
      api.get<ListResult>(`${V1}/documents?q=${encodeURIComponent(text)}&limit=6&no_facets=1`)
        .then(r => { if (my === seq.current) setRes(r) })
        .catch(() => { if (my === seq.current) setRes(null) })
        .finally(() => { if (my === seq.current) setLoading(false) })
    }, 140)
    return () => clearTimeout(t)
  }, [q, open, token, stamp])

  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => { if (box.current && !box.current.contains(e.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])

  const items = useMemo<Item[]>(() => {
    const out: Item[] = []
    const text = q.trim()
    if (token) {
      const list = tax[token.field.kind] as { id: number; name: string; color?: string; document_count: number }[]
      const p = token.partial.toLowerCase()
      list.filter(x => !p || x.name.toLowerCase().includes(p)).sort((a, b) => b.document_count - a.document_count).slice(0, 8)
        .forEach(x => out.push({ kind: 'complete', label: x.name, sub: `${token.field.label} · ${x.document_count}`, color: x.color, value: q.slice(0, token.start) + (token.field.key === '#' ? '#' : token.field.key + ':') + quote(x.name) + ' ' }))
      return out
    }
    if (!text) {
      recent().forEach(r => out.push({ kind: 'recent', q: r }))
      return out
    }
    if (text.length >= 2 && !text.includes(':')) {
      const t = text.toLowerCase()
      const pick = (list: { id: number; name: string; color?: string; document_count: number }[], label: string, to: (id: number) => string) =>
        list.filter(x => x.name.toLowerCase().includes(t)).slice(0, 2).forEach(x => out.push({ kind: 'filter', label: x.name, sub: `${label} · ${x.document_count} document${x.document_count === 1 ? '' : 's'}`, color: x.color, to: to(x.id) }))
      pick(tax.tags, 'Tag', id => docsURL({ tags: [id] }))
      pick(tax.correspondents, 'Correspondent', id => docsURL({ correspondent: id }))
      pick(tax.document_types, 'Type', id => docsURL({ document_type: id }))
    }
    res?.results.forEach(d => out.push({ kind: 'doc', doc: d }))
    if (text.length >= 2 && res) out.push({ kind: 'all', q: text, count: res.count })
    return out
  }, [q, token, tax, res])

  useEffect(() => { setIdx(0) }, [items.length, q])

  const close = () => { setOpen(false); input.current?.blur() }
  const searchAll = (text: string) => { remember(text); navigate(docsURL({ q: text })); close() }
  const choose = (it: Item | undefined) => {
    if (!it) { if (q.trim()) searchAll(q.trim()); return }
    switch (it.kind) {
      case 'doc': remember(q.trim()); navigate(`/documents/${it.doc.id}`); close(); setQ(''); break
      case 'filter': navigate(it.to); close(); setQ(''); break
      case 'complete': setQ(it.value); input.current?.focus(); break
      case 'recent': setQ(it.q); input.current?.focus(); break
      case 'all': searchAll(it.q); break
    }
  }
  const key = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setOpen(true); setIdx(i => Math.min(items.length - 1, i + 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setIdx(i => Math.max(0, i - 1)) }
    else if (e.key === 'Enter') { e.preventDefault(); if ((e.metaKey || e.ctrlKey) && q.trim()) searchAll(q.trim()); else choose(items[idx]) }
    else if (e.key === 'Escape') { if (q) setQ(''); else close() }
    else if (e.key === 'Tab' && token && items[idx]?.kind === 'complete') { e.preventDefault(); choose(items[idx]) }
  }
  // keep the highlighted row in view
  useEffect(() => { box.current?.querySelector('.qs-item.hl')?.scrollIntoView({ block: 'nearest' }) }, [idx])

  const showPanel = open && (items.length > 0 || (q.trim().length >= 2 && !token))
  let n = -1
  const row = (it: Item, body: React.ReactNode, cls = '') => { n++; const i = n; return <button type="button" key={i} className={`qs-item ${cls} ${i === idx ? 'hl' : ''}`} onMouseEnter={() => setIdx(i)} onMouseDown={e => e.preventDefault()} onClick={() => choose(it)}>{body}</button> }
  const filters = items.filter(i => i.kind === 'filter'), docs = items.filter(i => i.kind === 'doc'), completes = items.filter(i => i.kind === 'complete'), recents = items.filter(i => i.kind === 'recent'), all = items.find(i => i.kind === 'all')

  return (
    <div className={`topsearch-wrap ${showPanel ? 'open' : ''}`} ref={box}>
      <form className="topsearch" onSubmit={e => { e.preventDefault(); choose(items[idx]) }}>
        <span className="ico">{loading ? <span className="spinner sm" /> : '⌕'}</span>
        <input ref={input} value={q} onChange={e => { setQ(e.target.value); setOpen(true) }} onFocus={() => setOpen(true)} onKeyDown={key}
          placeholder="Search documents…  tag:x  from:x  type:x  created:2024  is:inbox" aria-label="Search" role="combobox" aria-expanded={showPanel} autoComplete="off" spellCheck={false} />
        {q ? <button type="button" className="qs-clear" onClick={() => { setQ(''); input.current?.focus() }} aria-label="Clear">×</button> : <span className="kbd hide-sm">⌘K</span>}
      </form>
      {showPanel && <div className="qs-panel" role="listbox">
        {recents.length > 0 && <div className="qs-section"><div className="qs-head">Recent searches</div>{recents.map(it => row(it, <><span className="qs-ico">↺</span><span className="grow ellipsis">{(it as { q: string }).q}</span></>))}</div>}
        {completes.length > 0 && <div className="qs-section"><div className="qs-head">{token?.field.label}</div>{completes.map(it => { const c = it as Extract<Item, { kind: 'complete' }>; return row(it, <><span className="qs-dot" style={{ background: c.color || 'var(--muted)' }} /><span className="grow ellipsis">{c.label}</span><small className="muted">{c.sub}</small><kbd className="kbd hide-sm">tab</kbd></>) })}</div>}
        {filters.length > 0 && <div className="qs-section qs-filters"><div className="qs-head">Filter by</div><div className="qs-chips">{filters.map(it => { const f = it as Extract<Item, { kind: 'filter' }>; return row(it, <><span className="qs-chip-label" style={f.color ? { background: f.color, color: inkFor(f.color) } : undefined}>{f.label}</span><small className="muted">{f.sub}</small></>, 'qs-chip') })}</div></div>}
        {docs.length > 0 && <div className="qs-section"><div className="qs-head">Documents</div>{docs.map(it => { const d = (it as { doc: Document }).doc; return row(it, <>
          <Thumb d={d} size="sm" />
          <span className="qs-doc">
            <span className="qs-title"><Highlight text={d.title} q={q} />{d.favorite && <span className="star"> ★</span>}</span>
            {d.snippet && <span className="qs-snippet">{snippetParts(d.snippet.replace(/\s+/g, ' ')).map((p, k) => p.hit ? <mark key={k}>{p.text}</mark> : <span key={k}>{p.text}</span>)}</span>}
            <span className="qs-meta">{[d.correspondent_name, d.document_type_name, fmtDate(d.created)].filter(Boolean).join(' · ')}{d.owner_name && ` · ⇄ ${d.owner_name}`}</span>
          </span>
          <span className="qs-tags"><TagList ids={d.tags} names={d.shared_tags} small max={2} /></span>
        </>, 'qs-docrow') })}</div>}
        {q.trim().length >= 2 && !token && res && res.results.length === 0 && filters.length === 0 && <div className="qs-empty">Nothing matches “{q.trim()}”. Try fewer words, <code>from:</code> or <code>tag:</code>.</div>}
        {all && row(all, <><span className="grow">See all {(all as { count: number }).count} result{(all as { count: number }).count === 1 ? '' : 's'} for “{(all as { q: string }).q}”</span><kbd className="kbd hide-sm">⌘↵</kbd></>, 'qs-all')}
        <div className="qs-foot hide-sm"><span><kbd className="kbd">↑</kbd><kbd className="kbd">↓</kbd> move</span><span><kbd className="kbd">↵</kbd> open</span><span><kbd className="kbd">esc</kbd> close</span><span className="spacer" /><span>tag: from: type: created: is:inbox</span></div>
      </div>}
    </div>
  )
})
