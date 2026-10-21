import { Link } from 'react-router-dom'
import type { Document, Tag } from '../lib/types'
import { V1 } from '../lib/api'
import { bytes, fileIconName, fmtDate, inkFor, snippetParts } from '../lib/format'
import { useApp } from '../lib/app-context'
import { Icon } from './icons'

export function TagChip({ tag, onClick, small }: { tag: Tag; onClick?: () => void; small?: boolean }) {
  const style = { background: tag.color, color: inkFor(tag.color), borderColor: tag.color }
  const inbox = tag.is_inbox ? <Icon name="inbox" size={small ? 11 : 12} strokeWidth={2.2} /> : null
  if (onClick) return <button type="button" className={`chip-tag ${small ? 'sm' : ''}`} style={style} onClick={onClick}>{inbox}{tag.name}</button>
  return <span className={`chip-tag ${small ? 'sm' : ''}`} style={style}>{inbox}{tag.name}</span>
}

export function TagList({ ids, small, max = 6, names }: { ids: number[]; small?: boolean; max?: number; names?: string[] }) {
  const { tax } = useApp()
  if (names) return <span className="tag-list">{names.slice(0, max).map(n => <span key={n} className={`chip-tag plain ${small ? 'sm' : ''}`}>{n}</span>)}</span>
  const tags = ids.map(id => tax.tags.find(t => t.id === id)).filter(Boolean) as Tag[]
  return <span className="tag-list">{tags.slice(0, max).map(t => <TagChip key={t.id} tag={t} small={small} />)}{tags.length > max && <span className="muted small">+{tags.length - max}</span>}</span>
}

export function StatusDot({ d }: { d: Document }) {
  if (d.status === 'done') return null
  const label = { pending: 'Waiting for OCR', processing: 'OCR running', failed: 'Failed: ' + d.status_message }[d.status] || d.status
  return <span className={`status-dot ${d.status}`} title={label}>{d.status === 'failed' ? '!' : ''}</span>
}

export function Thumb({ d, size = 'md' }: { d: Document; size?: 'sm' | 'md' | 'lg' }) {
  const canThumb = d.has_thumb || d.has_archive || d.mime === 'application/pdf'
  return (
    <span className={`thumb ${size}`}>
      {canThumb ? <img src={`${V1}/documents/${d.id}/thumb?v=${encodeURIComponent(d.modified_at)}`} alt="" loading="lazy" onError={e => { (e.target as HTMLImageElement).style.display = 'none' }} /> : null}
      <span className="thumb-fallback"><Icon name={fileIconName(d.mime, d.original_name)} size={size === 'sm' ? 16 : size === 'md' ? 24 : 36} strokeWidth={1.5} /></span>
      {d.page_count > 1 && <span className="pages">{d.page_count}</span>}
    </span>
  )
}

/** The filled star of a favourite. */
export function Star({ size = 13 }: { size?: number }) { return <span className="star" title="Favourite"><Icon name="star" size={size} /></span> }

export function Snippet({ s }: { s: string }) {
  return <span className="snippet">{snippetParts(s).map((p, i) => p.hit ? <mark key={i}>{p.text}</mark> : <span key={i}>{p.text}</span>)}</span>
}

import { ReviewBadge, useReview } from './Review'

export function DocCard({ d, selected, onSelect, onOpen }: { d: Document; selected?: boolean; onSelect?: (e: React.MouseEvent) => void; onOpen?: () => void }) {
  const review = useReview(d.id)
  return (
    <div className={`doc-card ${selected ? 'selected' : ''} ${d.status}`}>
      {onSelect && <label className="sel" onClick={e => e.stopPropagation()}><input type="checkbox" checked={!!selected} onChange={() => {}} onClick={onSelect} /></label>}
      <Link to={`/documents/${d.id}`} className="doc-thumb" onClick={e => { if (onOpen) { e.preventDefault(); onOpen() } }}><Thumb d={d} size="lg" /></Link>
      <div className="doc-body">
        <Link to={`/documents/${d.id}`} className="doc-title" title={d.title}>{d.favorite && <Star />}<StatusDot d={d} />{d.title}</Link>
        <ReviewBadge review={review} small />
        <div className="muted small ellipsis">{d.owner_name && <span className="badge cat" style={{ marginRight: 4 }}><Icon name="share" size={11} />{d.owner_name}</span>}{d.correspondent_name || <span className="muted">—</span>}{d.document_type_name && <> · {d.document_type_name}</>}</div>
        {d.snippet ? <div className="small doc-snippet"><Snippet s={d.snippet} /></div> : null}
        <div className="doc-foot"><span className="muted small">{fmtDate(d.created)}</span><TagList ids={d.tags} names={d.shared_tags} small max={3} /></div>
      </div>
    </div>
  )
}

export function DocRow({ d, selected, onSelect, cols }: { d: Document; selected?: boolean; onSelect?: (e: React.MouseEvent) => void; cols: string[] }) {
  const { tax } = useApp()
  const review = useReview(d.id)
  const cell = (c: string) => {
    switch (c) {
      case 'asn': return <td className="mono muted num">{d.asn ?? ''}</td>
      case 'correspondent': return <td className="ellipsis">{d.correspondent_name}</td>
      case 'title': return <td className="title-cell"><Link to={`/documents/${d.id}`}>{d.favorite && <Star />}<StatusDot d={d} />{d.title}</Link>{d.owner_name && <span className="badge cat" style={{ marginLeft: 6 }}><Icon name="share" size={11} />{d.owner_name}</span>}<ReviewBadge review={review} small />{d.snippet && <div className="small doc-snippet"><Snippet s={d.snippet} /></div>}</td>
      case 'type': return <td className="ellipsis">{d.document_type_name}</td>
      case 'tags': return <td><TagList ids={d.tags} names={d.shared_tags} small max={4} /></td>
      case 'created': return <td className="muted nowrap">{fmtDate(d.created)}</td>
      case 'added': return <td className="muted nowrap">{fmtDate(d.added_at)}</td>
      case 'modified': return <td className="muted nowrap">{fmtDate(d.modified_at)}</td>
      case 'pages': return <td className="muted num">{d.page_count || ''}</td>
      case 'size': return <td className="muted nowrap">{bytes(d.size)}</td>
      case 'path': return <td className="ellipsis muted">{tax.storage_paths.find(p => p.id === d.storage_path)?.name}</td>
      case 'notes': return <td className="muted num">{d.notes_count || ''}</td>
      case 'status': return <td><span className={`badge st-${d.status}`}>{d.status}</span></td>
      default: {
        const f = tax.custom_fields.find(x => 'cf:' + x.id === c)
        const v = d.custom_fields.find(x => x.field === f?.id)
        return <td className="muted ellipsis">{v?.value}</td>
      }
    }
  }
  return (
    <tr className={`${selected ? 'selected' : ''}`}>
      {onSelect && <td className="sel"><input type="checkbox" checked={!!selected} onChange={() => {}} onClick={onSelect} /></td>}
      <td className="thumb-cell"><Link to={`/documents/${d.id}`}><Thumb d={d} size="sm" /></Link></td>
      {cols.map(c => <Fragmented key={c}>{cell(c)}</Fragmented>)}
    </tr>
  )
}

function Fragmented({ children }: { children: React.ReactNode }) { return <>{children}</> }
