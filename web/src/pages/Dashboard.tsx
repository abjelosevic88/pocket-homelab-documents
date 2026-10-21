import { Link, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Dashboard as DashT } from '../lib/types'
import { bytes, docsURL, fmtDate } from '../lib/format'
import { Card, Loading, PageHeader, useAsync } from '../components/ui'
import { DocCard } from '../components/docs'
import { Icon } from '../components/icons'
import { useEffect } from 'react'

export default function Dashboard() {
  const { user, stamp, tax, settings } = useApp()
  const navigate = useNavigate()
  const { data, reload } = useAsync(() => api.get<DashT>(`${V1}/dashboard`), [])
  useEffect(() => { reload() }, [stamp]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!data) return <Loading />
  const st = data.stats
  const max = Math.max(1, ...st.by_month.map(m => m.count))
  const tools = settings?.tools
  return (
    <>
      <PageHeader title={`Hello, ${user?.name?.split(' ')[0] || 'there'}`} sub={data.documents ? `${data.documents} documents, ${st.pages} pages, ${bytes(data.bytes + data.archive_bytes)} on disk` : 'Your archive is empty. Upload something or drop a file into the consume folder.'} actions={<Link to="/upload" className="btn primary"><Icon name="upload" size={16} />Upload</Link>} />
      {tools && !tools.ocrmypdf && <div className="callout warning mb"><Icon name="alert" size={15} /><span>OCR is not available on this server: <code>ocrmypdf</code> and <code>tesseract</code> are not installed. Scans are stored without text until they are (the Docker image has them).</span></div>}
      <div className="tiles">
        <Tile label="Inbox" icon="inbox" value={data.inbox} to={docsURL({ inbox: 1 })} accent />
        <Tile label="Untagged" icon="tag" value={data.untagged} to={docsURL({ untagged: 1 })} />
        <Tile label="Added this week" icon="calendar" value={data.added_week} to={docsURL({ added: '7d' })} />
        <Tile label="Processing" icon="cpu" value={data.pending} to="/jobs" warn={data.failed > 0} hint={data.failed > 0 ? `${data.failed} failed` : undefined} />
        <Tile label="Favourites" icon="star" value={data.favorites} to={docsURL({ favorite: 1 })} />
        <Tile label="Trash" icon="trash" value={data.trash} to="/trash" />
      </div>
      <div className="dash-grid">
        <Card title="Recently added" actions={<Link to={docsURL({ sort: '-added' })} className="btn ghost sm">All<Icon name="chevron-right" size={14} /></Link>}>
          {data.recent.length === 0 ? <p className="muted">Nothing yet.</p> : <div className="doc-grid compact">{data.recent.map(d => <DocCard key={d.id} d={d} />)}</div>}
        </Card>
        <div className="stack">
          <Card title="Documents per month" actions={<span className="muted small">by document date, last year</span>}>
            {st.by_month.length === 0 ? <p className="muted">No dated documents yet.</p> : <div className="bars">{st.by_month.map(m => <button type="button" key={m.month} className="bar" title={`${m.month}: ${m.count}`} onClick={() => navigate(docsURL({ created: m.month }))}><span style={{ height: `${Math.max(4, (m.count / max) * 100)}%` }} /><small>{m.month.slice(5)}</small></button>)}</div>}
          </Card>
          <Card title="Document types"><Ranked rows={st.by_type} onPick={id => navigate(docsURL({ document_type: id }))} /></Card>
          <Card title="Correspondents"><Ranked rows={st.by_correspondent} onPick={id => navigate(docsURL({ correspondent: id }))} /></Card>
          <Card title="Tags"><Ranked rows={st.by_tag} onPick={id => navigate(docsURL({ tags: [id] }))} /></Card>
        </div>
      </div>
      {data.views.map(v => (
        <Card key={v.view.id} className="mt" title={v.view.name} actions={<Link to={docsURL({ ...v.view.query, view: v.view.id })} className="btn ghost sm">{v.count}<Icon name="chevron-right" size={14} /></Link>}>
          {v.documents.length === 0 ? <p className="muted">No documents match.</p> : <div className="doc-grid compact">{v.documents.map(d => <DocCard key={d.id} d={d} />)}</div>}
        </Card>
      ))}
      {tax.saved_views.length === 0 && data.documents > 0 && <p className="muted small mt">Tip: save a search from the Documents page and show it here (Save view → “Show on the dashboard”).</p>}
      {st.oldest && <p className="muted small mt">Oldest document {fmtDate(st.oldest)}, newest {fmtDate(st.newest)} · {st.characters.toLocaleString()} characters of text</p>}
    </>
  )
}

function Tile({ label, icon, value, to, accent, warn, hint }: { label: string; icon: string; value: number; to: string; accent?: boolean; warn?: boolean; hint?: string }) {
  return <Link to={to} className={`tile ${accent ? 'accent' : ''} ${warn ? 'warn' : ''}`}><Icon name={icon} size={17} className="tile-ico" /><span className="v">{value}</span><span className="l">{label}</span>{hint && <span className="h">{hint}</span>}</Link>
}

function Ranked({ rows, onPick }: { rows: { id: number; name: string; count: number; color?: string }[]; onPick: (id: number) => void }) {
  const max = Math.max(1, ...rows.map(r => r.count))
  if (rows.length === 0) return <p className="muted small">None yet. Add some under Manage.</p>
  return <div className="ranked">{rows.map(r => <button type="button" key={r.id + r.name} onClick={() => onPick(r.id)}><span className="name ellipsis">{r.color && <span className="swatch" style={{ background: r.color }} />}{r.name}</span><span className="meter"><span style={{ width: `${(r.count / max) * 100}%` }} /></span><span className="n">{r.count}</span></button>)}</div>
}
