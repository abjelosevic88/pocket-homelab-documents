// ⌘K / "/" — the search palette: documents as you type (with the passage that matched), filters,
// tag:/from:/type:/path: completion, recent searches, saved views and commands.
// ↑↓ move, ↵ opens, ⌘↵ / "See all" opens the full list, Esc closes.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Document, ListResult } from '../lib/types'
import { docsURL, fmtDate, inkFor, snippetParts } from '../lib/format'
import { Star, TagList, Thumb } from './docs'
import { Icon } from './icons'

export interface PaletteCommand { id: string; label: string; icon: string; hint?: string; keywords?: string; run: () => void }

type Item =
  | { kind: 'doc'; doc: Document }
  | { kind: 'filter'; label: string; sub: string; color?: string; to: string }
  | { kind: 'complete'; label: string; sub: string; color?: string; value: string }
  | { kind: 'recent'; q: string }
  | { kind: 'all'; q: string; count: number }
  | { kind: 'view'; id: number; name: string; to: string }
  | { kind: 'cmd'; cmd: PaletteCommand }

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

export function SearchPalette({ onClose, commands, initial = '' }: { onClose: () => void; commands: PaletteCommand[]; initial?: string }) {
  const { tax, stamp } = useApp()
  const navigate = useNavigate()
  const [q, setQ] = useState(initial)
  const [res, setRes] = useState<ListResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [idx, setIdx] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const seq = useRef(0)

  // the word being typed, for tag:/from:/type:/path: completion
  const token = useMemo(() => {
    const m = /(^|\s)((?:tag|from|correspondent|type|path):|#)("?)([^\s"]*)$/i.exec(q)
    if (!m) return null
    const field = FIELDS.find(f => f.key === m[2].replace(':', '').toLowerCase())
    return field ? { field, partial: m[4], start: m.index + m[1].length } : null
  }, [q])

  useEffect(() => {
    const text = q.trim()
    if (text.length < 2 || token) { setRes(null); setLoading(false); return }
    const my = ++seq.current
    setLoading(true)
    const t = setTimeout(() => {
      api.get<ListResult>(`${V1}/documents?q=${encodeURIComponent(text)}&limit=6&no_facets=1`)
        .then(r => { if (my === seq.current) setRes(r) })
        .catch(() => { if (my === seq.current) setRes(null) })
        .finally(() => { if (my === seq.current) setLoading(false) })
    }, 140)
    return () => clearTimeout(t)
  }, [q, token, stamp])

  const items = useMemo<Item[]>(() => {
    const out: Item[] = []
    const text = q.trim()
    const t = text.toLowerCase()
    if (token) {
      const l = tax[token.field.kind] as { id: number; name: string; color?: string; document_count: number }[]
      const p = token.partial.toLowerCase()
      l.filter(x => !p || x.name.toLowerCase().includes(p)).sort((a, b) => b.document_count - a.document_count).slice(0, 8)
        .forEach(x => out.push({ kind: 'complete', label: x.name, sub: `${token.field.label} · ${x.document_count}`, color: x.color, value: q.slice(0, token.start) + (token.field.key === '#' ? '#' : token.field.key + ':') + quote(x.name) + ' ' }))
      return out
    }
    if (!text) {
      recent().forEach(r => out.push({ kind: 'recent', q: r }))
      tax.saved_views.slice(0, 6).forEach(v => out.push({ kind: 'view', id: v.id, name: v.name, to: docsURL({ ...v.query, view: v.id }) }))
      commands.forEach(c => out.push({ kind: 'cmd', cmd: c }))
      return out
    }
    if (text.length >= 2 && !text.includes(':')) {
      const pick = (l: { id: number; name: string; color?: string; document_count: number }[], label: string, to: (id: number) => string) =>
        l.filter(x => x.name.toLowerCase().includes(t)).slice(0, 2).forEach(x => out.push({ kind: 'filter', label: x.name, sub: `${label} · ${x.document_count} document${x.document_count === 1 ? '' : 's'}`, color: x.color, to: to(x.id) }))
      pick(tax.tags, 'Tag', id => docsURL({ tags: [id] }))
      pick(tax.correspondents, 'Correspondent', id => docsURL({ correspondent: id }))
      pick(tax.document_types, 'Type', id => docsURL({ document_type: id }))
    }
    res?.results.forEach(d => out.push({ kind: 'doc', doc: d }))
    if (text.length >= 2 && res) out.push({ kind: 'all', q: text, count: res.count })
    if (!text.includes(':')) {
      const words = t.split(/\s+/).filter(Boolean)
      const hit = (s: string) => words.every(w => s.toLowerCase().includes(w))
      tax.saved_views.filter(v => hit(v.name)).slice(0, 4).forEach(v => out.push({ kind: 'view', id: v.id, name: v.name, to: docsURL({ ...v.query, view: v.id }) }))
      commands.filter(c => hit(`${c.label} ${c.keywords || ''}`)).forEach(c => out.push({ kind: 'cmd', cmd: c }))
    }
    return out
  }, [q, token, tax, res, commands])

  useEffect(() => { setIdx(0) }, [items.length, q])
  useEffect(() => { list.current?.querySelector('.qs-item.hl')?.scrollIntoView({ block: 'nearest' }) }, [idx])

  const searchAll = (text: string) => { remember(text); onClose(); navigate(docsURL({ q: text })) }
  const choose = (it: Item | undefined) => {
    if (!it) { if (q.trim()) searchAll(q.trim()); return }
    switch (it.kind) {
      case 'doc': remember(q.trim()); onClose(); navigate(`/documents/${it.doc.id}`); break
      case 'filter': case 'view': onClose(); navigate(it.to); break
      case 'complete': setQ(it.value); input.current?.focus(); break
      case 'recent': setQ(it.q); input.current?.focus(); break
      case 'all': searchAll(it.q); break
      case 'cmd': onClose(); it.cmd.run(); break
    }
  }
  const key = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setIdx(i => Math.min(items.length - 1, i + 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setIdx(i => Math.max(0, i - 1)) }
    else if (e.key === 'Enter') { e.preventDefault(); if ((e.metaKey || e.ctrlKey) && q.trim()) searchAll(q.trim()); else choose(items[idx]) }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); if (q) setQ(''); else onClose() }
    else if (e.key === 'Tab' && token && items[idx]?.kind === 'complete') { e.preventDefault(); choose(items[idx]) }
  }

  let n = -1
  const row = (it: Item, body: React.ReactNode, cls = '') => { n++; const i = n; return <button type="button" key={i} role="option" aria-selected={i === idx} className={`qs-item ${cls} ${i === idx ? 'hl' : ''}`} onMouseEnter={() => setIdx(i)} onMouseDown={e => e.preventDefault()} onClick={() => choose(it)}>{body}</button> }
  const of = <K extends Item['kind']>(k: K) => items.filter(i => i.kind === k) as Extract<Item, { kind: K }>[]
  const filters = of('filter'), docs = of('doc'), completes = of('complete'), recents = of('recent'), views = of('view'), cmds = of('cmd'), all = of('all')[0]
  const empty = q.trim().length >= 2 && !token && res && res.results.length === 0 && filters.length === 0

  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box search" role="dialog" aria-label="Search documents and commands">
        <form className="pal-input" onSubmit={e => { e.preventDefault(); choose(items[idx]) }}>
          {loading ? <span className="spinner" style={{ width: 16, height: 16 }} /> : <Icon name="search" size={18} />}
          <input ref={input} autoFocus value={q} onChange={e => setQ(e.target.value)} onKeyDown={key}
            placeholder="Search documents…  tag:x  from:x  created:2024  is:inbox" aria-label="Search" role="combobox" aria-expanded autoComplete="off" spellCheck={false} />
          {q ? <button type="button" className="qs-clear" onClick={() => { setQ(''); input.current?.focus() }} aria-label="Clear"><Icon name="x" size={16} /></button> : <kbd>esc</kbd>}
        </form>
        <div className="pal-list qs" ref={list} role="listbox">
          {recents.length > 0 && <div className="qs-section"><div className="pal-group">Recent searches</div>{recents.map(it => row(it, <><span className="qs-ico"><Icon name="clock" size={16} /></span><span className="grow ellipsis">{it.q}</span></>))}</div>}
          {completes.length > 0 && <div className="qs-section"><div className="pal-group">{token?.field.label}</div>{completes.map(it => row(it, <><span className="qs-dot" style={{ background: it.color || 'var(--faint)' }} /><span className="grow ellipsis">{it.label}</span><small className="muted">{it.sub}</small><kbd className="hide-sm">tab</kbd></>))}</div>}
          {filters.length > 0 && <div className="qs-section"><div className="pal-group">Filter by</div><div className="qs-chips">{filters.map(it => row(it, <><span className="qs-chip-label" style={it.color ? { background: it.color, color: inkFor(it.color) } : undefined}>{it.label}</span><small className="muted">{it.sub}</small></>, 'qs-chip'))}</div></div>}
          {docs.length > 0 && <div className="qs-section"><div className="pal-group">Documents</div>{docs.map(it => { const d = it.doc; return row(it, <>
            <Thumb d={d} size="sm" />
            <span className="qs-doc">
              <span className="qs-title">{d.favorite && <Star size={12} />}<Highlight text={d.title} q={q} /></span>
              {d.snippet && <span className="qs-snippet">{snippetParts(d.snippet.replace(/\s+/g, ' ')).map((p, k) => p.hit ? <mark key={k}>{p.text}</mark> : <span key={k}>{p.text}</span>)}</span>}
              <span className="qs-meta">{[d.correspondent_name, d.document_type_name, fmtDate(d.created), d.owner_name && `shared by ${d.owner_name}`].filter(Boolean).join(' · ')}</span>
            </span>
            <span className="qs-tags"><TagList ids={d.tags} names={d.shared_tags} small max={2} /></span>
          </>, 'qs-docrow') })}</div>}
          {empty && <div className="qs-empty">Nothing matches “{q.trim()}”. Try fewer words, <code>from:</code> or <code>tag:</code>.</div>}
          {all && <div className="qs-section">{row(all, <><span className="qs-ico"><Icon name="list" size={16} /></span><span className="grow">See all {all.count} result{all.count === 1 ? '' : 's'} for “{all.q}”</span><kbd className="hide-sm">⌘↵</kbd></>, 'qs-all')}</div>}
          {views.length > 0 && <div className="qs-section"><div className="pal-group">Saved views</div>{views.map(it => row(it, <><span className="qs-ico"><Icon name="bookmark" size={16} /></span><span className="grow ellipsis">{it.name}</span></>))}</div>}
          {cmds.length > 0 && <div className="qs-section"><div className="pal-group">Go to &amp; do</div>{cmds.map(it => row(it, <><span className="qs-ico"><Icon name={it.cmd.icon} size={16} /></span><span className="grow ellipsis">{it.cmd.label}</span>{it.cmd.hint && <kbd>{it.cmd.hint}</kbd>}</>, 'qs-cmd'))}</div>}
        </div>
        <div className="qs-foot hide-sm"><span><kbd>↑</kbd><kbd>↓</kbd> move</span><span><kbd>↵</kbd> open</span><span><kbd>⌘↵</kbd> all results</span><span className="spacer" /><span><code>tag:</code> <code>from:</code> <code>type:</code> <code>created:</code> <code>is:inbox</code></span></div>
      </div>
    </div>, document.body)
}
