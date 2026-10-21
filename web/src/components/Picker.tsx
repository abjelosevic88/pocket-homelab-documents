import { useEffect, useMemo, useRef, useState } from 'react'
import { inkFor } from '../lib/format'
import { Icon } from './icons'

export interface Option { id: number; name: string; color?: string; count?: number }

/** Pick one or many from a list, type to filter, Enter to create. */
export function Picker({ options, value, onChange, multi = false, placeholder = 'Choose…', onCreate, allowNone = true, compact = false }: {
  options: Option[]; value: number[] | number | null; onChange: (v: number[] | number | null) => void; multi?: boolean; placeholder?: string
  onCreate?: (name: string) => Promise<Option | null>; allowNone?: boolean; compact?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const [idx, setIdx] = useState(0)
  const ref = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const selected = useMemo(() => multi ? (value as number[] | null) || [] : (value == null ? [] : [value as number]), [value, multi])
  const byId = useMemo(() => new Map(options.map(o => [o.id, o])), [options])
  const list = useMemo(() => {
    const t = q.trim().toLowerCase()
    const l = options.filter(o => !t || o.name.toLowerCase().includes(t))
    return l.sort((a, b) => Number(selected.includes(b.id)) - Number(selected.includes(a.id)) || a.name.localeCompare(b.name)).slice(0, 200)
  }, [options, q, selected])
  const canCreate = !!onCreate && q.trim() !== '' && !options.some(o => o.name.toLowerCase() === q.trim().toLowerCase())
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) { setOpen(false); setQ('') } }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])
  useEffect(() => { setIdx(0) }, [q])
  const toggle = (id: number) => {
    if (multi) { const s = selected.includes(id) ? selected.filter(x => x !== id) : [...selected, id]; onChange(s) }
    else { onChange(selected.includes(id) && allowNone ? null : id); setOpen(false); setQ('') }
  }
  const create = async () => {
    if (!onCreate || !canCreate) return
    const o = await onCreate(q.trim())
    if (o) { if (multi) onChange([...selected, o.id]); else { onChange(o.id); setOpen(false) } ; setQ('') }
  }
  const key = (e: React.KeyboardEvent) => {
    const n = list.length + (canCreate ? 1 : 0)
    if (e.key === 'ArrowDown') { e.preventDefault(); setIdx(i => Math.min(n - 1, i + 1)); setOpen(true) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setIdx(i => Math.max(0, i - 1)) }
    else if (e.key === 'Enter') { e.preventDefault(); if (idx < list.length) toggle(list[idx].id); else create() }
    else if (e.key === 'Escape') { setOpen(false); setQ('') }
    else if (e.key === 'Backspace' && q === '' && multi && selected.length) onChange(selected.slice(0, -1))
  }
  return (
    <div className={`picker ${open ? 'open' : ''} ${compact ? 'compact' : ''}`} ref={ref}>
      <div className="picker-box" onClick={() => { setOpen(true); input.current?.focus() }}>
        {selected.map(id => { const o = byId.get(id); if (!o) return null; return <span key={id} className="chip-tag" style={o.color ? { background: o.color, color: inkFor(o.color), borderColor: o.color } : undefined}>{o.name}{multi && <button type="button" className="x" onClick={e => { e.stopPropagation(); toggle(id) }} aria-label="Remove"><Icon name="x" size={11} strokeWidth={2.4} /></button>}</span> })}
        <input ref={input} value={q} onChange={e => { setQ(e.target.value); setOpen(true) }} onFocus={() => setOpen(true)} onKeyDown={key} placeholder={selected.length ? '' : placeholder} />
        {!multi && selected.length > 0 && allowNone && <button type="button" className="x clear" onClick={e => { e.stopPropagation(); onChange(null) }} aria-label="Clear"><Icon name="x" size={14} /></button>}
      </div>
      {open && <div className="picker-list">
        {list.map((o, i) => <button type="button" key={o.id} className={`${selected.includes(o.id) ? 'on' : ''} ${i === idx ? 'hl' : ''}`} onMouseEnter={() => setIdx(i)} onClick={() => toggle(o.id)}>
          {o.color && <span className="swatch" style={{ background: o.color }} />}<span className="grow ellipsis">{o.name}</span>{o.count != null && <small className="muted">{o.count}</small>}{selected.includes(o.id) && <Icon name="check" size={15} className="check" />}
        </button>)}
        {canCreate && <button type="button" className={`create ${idx === list.length ? 'hl' : ''}`} onClick={create}><Icon name="plus" size={15} />Create “{q.trim()}”</button>}
        {list.length === 0 && !canCreate && <div className="muted small pad-s">Nothing matches</div>}
      </div>}
    </div>
  )
}
