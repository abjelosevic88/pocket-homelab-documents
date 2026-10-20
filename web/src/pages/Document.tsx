import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Document, HistoryEntry, Job, Note, Share, Suggestions, CustomField, FieldValue, Version, DocShare } from '../lib/types'
import { ago, bytes, fileIcon, fmtDate, money, plural, stamp } from '../lib/format'
import { Card, Confirm, Field, Loading, Menu, Modal, Segmented, Tabs, useToast } from '../components/ui'
import { Picker } from '../components/Picker'
import { TagChip } from '../components/docs'

type Tab = 'details' | 'content' | 'notes' | 'history' | 'similar' | 'versions'

export default function DocumentPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { tax, reloadTax, settings, stamp: changeStamp, bump, users, user } = useApp()
  const { toast } = useToast()
  const [doc, setDoc] = useState<Document | null>(null)
  const [nav, setNav] = useState<{ prev: number; next: number }>({ prev: 0, next: 0 })
  const [job, setJob] = useState<Job | null>(null)
  const [form, setForm] = useState<Partial<Document> | null>(null)
  const [tab, setTab] = useState<Tab>('details')
  const [sugg, setSugg] = useState<Suggestions | null>(null)
  const [del, setDel] = useState(false)
  const [share, setShare] = useState(false)
  const [moneyOpen, setMoneyOpen] = useState(false)
  const [shareWith, setShareWith] = useState(false)
  const [langOpen, setLangOpen] = useState(false)
  const [selPages, setSelPages] = useState<number[]>([])
  const [splitOpen, setSplitOpen] = useState(false)
  const replaceInput = useRef<HTMLInputElement>(null)
  const [preview, setPreview] = useState<'pages' | 'pdf'>(() => (localStorage.getItem('pd-preview') as 'pages' | 'pdf') || (window.innerWidth < 900 ? 'pages' : 'pdf'))
  const [saving, setSaving] = useState(false)
  const [similar, setSimilar] = useState<Document[] | null>(null)
  const dirty = useMemo(() => form && doc && JSON.stringify(normalize(form)) !== JSON.stringify(normalize(doc)), [form, doc])
  useEffect(() => { localStorage.setItem('pd-preview', preview) }, [preview])

  const load = useCallback(async () => {
    const r = await api.get<{ document: Document; prev: number; next: number; job?: Job }>(`${V1}/documents/${id}`)
    setDoc(r.document); setNav({ prev: r.prev, next: r.next }); setJob(r.job || null)
    setForm(f => (f && dirty) ? f : { ...r.document })
    return r.document
  }, [id]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { setForm(null); setSugg(null); setSimilar(null); setTab('details'); setSelPages([]); load().catch(e => toast((e as Error).message, 'err')) }, [id]) // eslint-disable-line react-hooks/exhaustive-deps
  // keep in step with the worker
  useEffect(() => { if (doc && (doc.status === 'pending' || doc.status === 'processing')) load().catch(() => {}) }, [changeStamp]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!doc || doc.status === 'done' || doc.status === 'failed') return
    const t = setInterval(() => load().catch(() => {}), 3000)
    return () => clearInterval(t)
  }, [doc?.status, load]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!doc || doc.status !== 'done' || !settings?.suggest_on_open || sugg) return
    api.get<Suggestions>(`${V1}/documents/${doc.id}/suggestions`).then(setSugg).catch(() => {})
  }, [doc?.id, doc?.status, settings?.suggest_on_open]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (tab !== 'similar' || similar || !doc) return
    api.get<Document[]>(`${V1}/documents/${doc.id}/similar?limit=12`).then(setSimilar).catch(() => setSimilar([]))
  }, [tab, doc?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  const save = async () => {
    if (!form || !doc) return
    setSaving(true)
    try {
      const body: Record<string, unknown> = { title: form.title, created: form.created, correspondent: form.correspondent ?? null, document_type: form.document_type ?? null, storage_path: form.storage_path ?? null, asn: form.asn ?? null, tags: form.tags, custom_fields: form.custom_fields, favorite: form.favorite }
      if (form.content !== undefined && form.content !== doc.content) body.content = form.content
      const d = await api.put<Document>(`${V1}/documents/${doc.id}`, body)
      setDoc(d); setForm({ ...d }); toast('Saved'); reloadTax(); bump()
    } catch (e) { toast((e as Error).message, 'err') } finally { setSaving(false) }
  }
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const typing = ['INPUT', 'TEXTAREA', 'SELECT'].includes((e.target as HTMLElement)?.tagName)
      if ((e.metaKey || e.ctrlKey) && e.key === 's') { e.preventDefault(); if (dirty) save() }
      else if (!typing && e.key === 'ArrowLeft' && nav.prev) navigate(`/documents/${nav.prev}`)
      else if (!typing && e.key === 'ArrowRight' && nav.next) navigate(`/documents/${nav.next}`)
      else if (!typing && e.key === 'Escape') navigate(-1)
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const create = (kind: 'tags' | 'correspondents' | 'document_types' | 'storage_paths') => async (name: string) => { const o = await api.post<{ id: number; name: string; color?: string }>(`${V1}/${kind}`, { name }); await reloadTax(); return o }
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast(msg); await load(); bump() } catch (e) { toast((e as Error).message, 'err') } }
  if (!doc || !form) return <Loading />
  const access = doc.access || 'owner'
  const isOwner = access === 'owner'
  const canEdit = access !== 'view'
  const applySugg = (patch: Partial<Document>) => setForm(f => ({ ...f, ...patch }))
  const pageOp = async (body: Record<string, unknown>, msg: string) => {
    try { await api.post(`${V1}/documents/${doc.id}/pages`, body); toast(msg); setSelPages([]); await load(); bump() } catch (e) { toast((e as Error).message, 'err') }
  }
  const replace = async (file: File) => {
    const fd = new FormData(); fd.append('document', file)
    const res = await fetch(`${V1}/documents/${doc.id}/replace`, { method: 'POST', body: fd, credentials: 'same-origin' })
    const j = await res.json().catch(() => ({}))
    if (!res.ok) { toast(j.error || res.statusText, 'err'); return }
    toast('File replaced; OCR running'); await load(); bump()
  }
  const hasPDFSource = doc.has_archive || doc.mime === 'application/pdf'
  const toggleSel = (n: number) => setSelPages(p => p.includes(n) ? p.filter(x => x !== n) : [...p, n].sort((a, b) => a - b))
  const hasPDF = doc.has_archive || doc.mime === 'application/pdf'
  const isImage = doc.mime.startsWith('image/')
  const suggTags = (sugg?.tags || []).filter(t => !(form.tags || []).includes(t))
  const suggCorr = (sugg?.correspondents || []).filter(c => form.correspondent !== c)
  const suggType = (sugg?.document_types || []).filter(c => form.document_type !== c)
  const suggPath = (sugg?.storage_paths || []).filter(c => form.storage_path !== c)
  const suggDates = (sugg?.dates || []).filter(d => d !== form.created).slice(0, 4)
  const hasSugg = suggTags.length + suggCorr.length + suggType.length + suggPath.length + suggDates.length > 0
  const inboxIDs = tax.tags.filter(t => t.is_inbox).map(t => t.id)
  const inInbox = (form.tags || []).some(t => inboxIDs.includes(t))

  return (
    <div className="doc-page">
      <div className="doc-head">
        <Link to="/documents" className="btn ghost sm">← Documents</Link>
        <span className="row">
          <button className="btn ghost sm" disabled={!nav.prev} onClick={() => navigate(`/documents/${nav.prev}`)} title="Newer (←)">‹</button>
          <button className="btn ghost sm" disabled={!nav.next} onClick={() => navigate(`/documents/${nav.next}`)} title="Older (→)">›</button>
        </span>
        <span className="spacer" />
        {doc.status !== 'done' && <span className={`badge st-${doc.status}`}>{doc.status === 'failed' ? 'OCR failed' : doc.status === 'processing' ? 'OCR running…' : 'waiting for OCR'}</span>}
        <button className={`btn sm ${form.favorite ? 'on' : ''}`} onClick={() => setForm({ ...form, favorite: !form.favorite })} title="Favourite">{form.favorite ? '★' : '☆'}</button>
        <a className="btn sm" href={`${V1}/documents/${doc.id}/download`}>⇣ {doc.has_archive ? 'PDF' : 'Download'}</a>
        <Menu align="right" button={<button className="btn sm">More ▾</button>}>
          {doc.has_archive && <a href={`${V1}/documents/${doc.id}/download?original=1`}>Download original ({doc.original_name.split('.').pop()?.toUpperCase()})</a>}
          <a href={`${V1}/documents/${doc.id}/preview`} target="_blank" rel="noreferrer">Open in a new tab</a>
          {isOwner && <button onClick={() => setShare(true)}>Share link…</button>}
          {isOwner && users.length > 1 && <button onClick={() => setShareWith(true)}>Share with someone here…</button>}
          {isOwner && <button onClick={() => replaceInput.current?.click()}>Replace the file…</button>}
          {isOwner && <button onClick={() => setLangOpen(true)}>Run OCR again with other languages…</button>}
          <button onClick={() => setMoneyOpen(true)}>Book in Pocket Money…</button>
          <button onClick={() => act(() => api.post(`${V1}/documents/${doc.id}/reprocess`, {}), 'Queued for OCR')}>Run OCR again</button>
          <button onClick={() => act(() => api.post(`${V1}/documents/${doc.id}/reprocess`, { mode: 'force', force_text: true }), 'Queued for OCR')}>Force OCR (replace text layer)</button>
          <button onClick={() => act(() => api.post(`${V1}/documents/${doc.id}/reprocess`, { mode: 'redo' }), 'Queued for OCR')}>Redo OCR (keep vector text)</button>
          {isOwner && <button className="danger-text" onClick={() => setDel(true)}>Move to trash</button>}
        </Menu>
        <button className="btn primary sm" disabled={!dirty || saving || !canEdit} onClick={save} title="⌘S">{saving ? 'Saving…' : 'Save'}</button>
        <input ref={replaceInput} type="file" hidden accept=".pdf,.png,.jpg,.jpeg,.tif,.tiff,.webp,.docx,.xlsx,.pptx,.odt,.txt,.md,.html,.eml" onChange={e => { if (e.target.files?.[0]) replace(e.target.files[0]); e.target.value = '' }} />
      </div>
      {!isOwner && <div className="shared-banner">⇄ Shared with you by <b>{doc.owner_name}</b> · {access === 'edit' ? 'you can change its details and add notes' : 'view only'}</div>}
      {doc.status === 'failed' && <div className="callout danger mb"><b>OCR failed:</b> {doc.status_message} <button className="link-btn" onClick={() => navigate('/jobs')}>See the log</button></div>}
      {job && doc.status !== 'done' && job.progress && <div className="callout mb small">{job.progress}</div>}
      <div className="doc-split">
        <div className="doc-form">
          <input className="title-input" value={form.title || ''} onChange={e => setForm({ ...form, title: e.target.value })} placeholder="Title" />
          {hasSugg && <div className="sugg">
            <span className="muted small">Suggested:</span>
            {suggDates.map(d => <button key={d} className="chip" onClick={() => applySugg({ created: d })}>📅 {fmtDate(d)}</button>)}
            {suggCorr.map(c => <button key={'c' + c} className="chip" onClick={() => applySugg({ correspondent: c })}>👤 {tax.correspondents.find(x => x.id === c)?.name}{conf(sugg, 'correspondent', c)}</button>)}
            {suggType.map(c => <button key={'t' + c} className="chip" onClick={() => applySugg({ document_type: c })}>📁 {tax.document_types.find(x => x.id === c)?.name}{conf(sugg, 'document_type', c)}</button>)}
            {suggPath.map(c => <button key={'p' + c} className="chip" onClick={() => applySugg({ storage_path: c })}>🗂 {tax.storage_paths.find(x => x.id === c)?.name}{conf(sugg, 'storage_path', c)}</button>)}
            {suggTags.map(t => { const tag = tax.tags.find(x => x.id === t); return tag ? <TagChip key={'g' + t} tag={tag} small onClick={() => applySugg({ tags: [...(form.tags || []), t] })} /> : null })}
            <button className="link-btn small" onClick={() => applySugg({ created: suggDates[0] || form.created, correspondent: form.correspondent ?? suggCorr[0] ?? null, document_type: form.document_type ?? suggType[0] ?? null, storage_path: form.storage_path ?? suggPath[0] ?? null, tags: [...(form.tags || []), ...suggTags] })}>apply all</button>
          </div>}
          <Tabs value={tab} onChange={setTab} tabs={[{ id: 'details', label: 'Details' }, { id: 'content', label: 'Text' }, { id: 'notes', label: <>Notes{doc.notes_count ? <span className="count">{doc.notes_count}</span> : null}</> }, { id: 'history', label: 'History' }, ...(isOwner ? [{ id: 'versions' as Tab, label: 'Versions' }, { id: 'similar' as Tab, label: 'Similar' }] : [])]} />
          <div className="tab-body">
            {tab === 'versions' && <VersionsTab doc={doc} onChange={load} />}
            {tab === 'details' && !isOwner && <div className="grid" style={{ gap: 10 }}>
              <div className="row wrap"><span className="muted small">Tags:</span>{(doc.shared_tags || []).map(t => <span key={t} className="chip-tag sm" style={{ background: 'var(--surface-2)', borderColor: 'var(--border)', color: 'var(--text)' }}>{t}</span>)}{!(doc.shared_tags || []).length && <span className="muted small">none</span>}</div>
              <table className="table compact meta-table"><tbody>
                <tr><th>Correspondent</th><td>{doc.correspondent_name || '—'}</td></tr>
                <tr><th>Type</th><td>{doc.document_type_name || '—'}</td></tr>
                <tr><th>Date</th><td>{canEdit ? <input type="date" value={form.created || ''} onChange={e => setForm({ ...form, created: e.target.value })} style={{ width: 180 }} /> : fmtDate(doc.created)}</td></tr>
                <tr><th>ASN</th><td>{doc.asn ?? '—'}</td></tr>
                <tr><th>File</th><td>{fileIcon(doc.mime, doc.original_name)} {doc.original_name} · {bytes(doc.size)}{doc.page_count > 0 && <> · {plural(doc.page_count, 'page')}</>}</td></tr>
              </tbody></table>
            </div>}
            {tab === 'details' && isOwner && <div className="grid" style={{ gap: 12 }}>
              <div className="two-col tight">
                <Field label="Document date"><input type="date" value={form.created || ''} onChange={e => setForm({ ...form, created: e.target.value })} /></Field>
                <Field label="Archive serial number" help="Write it on the paper; find it later with asn:12"><div className="row"><input value={form.asn ?? ''} onChange={e => setForm({ ...form, asn: e.target.value === '' ? null : Number(e.target.value) })} inputMode="numeric" placeholder="none" /><button type="button" className="btn sm" title="Next free number" onClick={() => api.get<{ next_asn: number }>(`${V1}/documents/next_asn`).then(r => setForm(f => ({ ...f, asn: r.next_asn })))}>next</button></div></Field>
              </div>
              <Field label="Tags" group><Picker multi options={tax.tags} value={form.tags || []} onChange={v => setForm({ ...form, tags: v as number[] })} placeholder="Add tags…" onCreate={create('tags')} />{inInbox && <button type="button" className="link-btn small muted" onClick={() => setForm({ ...form, tags: (form.tags || []).filter(t => !inboxIDs.includes(t)) })}>Remove from inbox</button>}</Field>
              <Field label="Correspondent" group><Picker options={tax.correspondents} value={form.correspondent ?? null} onChange={v => setForm({ ...form, correspondent: v as number | null })} placeholder="Who sent it, or who it is for" onCreate={create('correspondents')} /></Field>
              <Field label="Document type" group><Picker options={tax.document_types} value={form.document_type ?? null} onChange={v => setForm({ ...form, document_type: v as number | null })} placeholder="Invoice, contract, letter…" onCreate={create('document_types')} /></Field>
              <Field label="Storage path" group><Picker options={tax.storage_paths} value={form.storage_path ?? null} onChange={v => setForm({ ...form, storage_path: v as number | null })} placeholder="Folder layout for the mirror and exports" onCreate={create('storage_paths')} /></Field>
              {tax.custom_fields.length > 0 && <FieldsEditor fields={tax.custom_fields} values={form.custom_fields || []} onChange={v => setForm({ ...form, custom_fields: v })} />}
              <table className="table compact meta-table">
                <tbody>
                  <tr><th>File</th><td>{fileIcon(doc.mime, doc.original_name)} {doc.original_name} · {bytes(doc.size)}{doc.has_archive && <> · PDF/A {bytes(doc.archive_size)}</>}{doc.page_count > 0 && <> · {plural(doc.page_count, 'page')}</>}</td></tr>
                  <tr><th>Added</th><td title={stamp(doc.added_at)}>{ago(doc.added_at)} via {doc.source}</td></tr>
                  <tr><th>Modified</th><td title={stamp(doc.modified_at)}>{ago(doc.modified_at)}</td></tr>
                  {doc.mirror_path && <tr><th>Mirror</th><td className="mono small">{doc.mirror_path}</td></tr>}
                  <tr><th>Checksum</th><td className="mono small">{doc.checksum.slice(0, 16)}…</td></tr>
                </tbody>
              </table>
            </div>}
            {tab === 'content' && <div className="grid" style={{ gap: 8 }}>
              <p className="muted small">The text OCR read{doc.content_edited && ', corrected by hand'} ({(form.content || '').length.toLocaleString()} characters). Edits are saved with the document and searched; running OCR again keeps them unless forced.</p>
              <textarea className="content-edit" value={form.content || ''} onChange={e => setForm({ ...form, content: e.target.value })} spellCheck={false} />
            </div>}
            {tab === 'notes' && <NotesTab doc={doc} onChange={load} />}
            {tab === 'history' && <HistoryTab doc={doc} />}
            {tab === 'similar' && (similar === null ? <Loading /> : similar.length === 0 ? <p className="muted">No similar documents found.</p> : <ul className="similar">{similar.map(s => <li key={s.id}><Link to={`/documents/${s.id}`}>{s.title}</Link><span className="muted small">{s.correspondent_name} · {fmtDate(s.created)}</span></li>)}</ul>)}
          </div>
        </div>
        <div className="doc-preview">
          <div className="preview-bar">
            {hasPDF && <Segmented size="sm" value={preview} onChange={setPreview} options={[{ value: 'pages', label: 'Pages' }, { value: 'pdf', label: 'PDF' }]} />}
            <span className="spacer" />
            <span className="muted small">{doc.page_count > 0 && plural(doc.page_count, 'page')}</span>
          </div>
          {isOwner && (hasPDFSource || isImage) && doc.status === 'done' && <div className="page-tools">
            {hasPDFSource && preview === 'pages' ? <span className="muted">{selPages.length ? `${selPages.length} selected` : 'Tap page numbers to select'}</span> : <span className="muted">Whole file:</span>}
            <button className="btn xs" onClick={() => pageOp({ op: 'rotate', degrees: 90, pages: selPages }, 'Rotated')}>↻ 90°</button>
            <button className="btn xs" onClick={() => pageOp({ op: 'rotate', degrees: 270, pages: selPages }, 'Rotated')}>↺ 90°</button>
            <button className="btn xs" onClick={() => pageOp({ op: 'rotate', degrees: 180, pages: selPages }, 'Rotated')}>180°</button>
            {hasPDFSource && selPages.length > 0 && <>
              <button className="btn xs" onClick={() => setSplitOpen(true)}>Split off → new document</button>
              <button className="btn xs danger-text" onClick={() => pageOp({ op: 'delete', pages: selPages }, 'Pages removed')}>Delete pages</button>
              <button className="btn ghost xs" onClick={() => setSelPages([])}>Clear</button>
            </>}
          </div>}
          {hasPDF && preview === 'pdf' && <iframe className="pdf-frame" src={`${V1}/documents/${doc.id}/preview#view=FitH`} title="PDF" />}
          {hasPDF && preview === 'pages' && <PageImages doc={doc} selected={selPages} onToggle={isOwner ? toggleSel : undefined} />}
          {!hasPDF && isImage && <div className="image-preview"><img src={`${V1}/documents/${doc.id}/preview`} alt="" /></div>}
          {!hasPDF && !isImage && <div className="text-preview"><div className="muted small mb">{fileIcon(doc.mime, doc.original_name)} {doc.original_name} — no PDF twin (set GOTENBERG_URL to render office files); the text:</div><pre>{doc.content || 'No text.'}</pre></div>}
        </div>
      </div>
      {del && <Confirm title="Move to the trash?" message="It can be restored from the trash for a while." onCancel={() => setDel(false)} onConfirm={async () => { setDel(false); await api.del(`${V1}/documents/${doc.id}`); toast('Moved to trash'); bump(); navigate(nav.next ? `/documents/${nav.next}` : '/documents') }} label="Delete" />}
      {share && <ShareModal doc={doc} onClose={() => setShare(false)} />}
      {moneyOpen && <MoneyModal doc={doc} onClose={() => setMoneyOpen(false)} onBooked={load} />}
      {shareWith && <ShareWithModal doc={doc} users={users.filter(u => u.id !== user?.id)} onClose={() => setShareWith(false)} />}
      {langOpen && <LanguageModal doc={doc} onClose={() => setLangOpen(false)} onDone={() => { setLangOpen(false); load() }} />}
      {splitOpen && <Modal title={`Split ${selPages.length} page${selPages.length > 1 ? 's' : ''} off`} onClose={() => setSplitOpen(false)}>
        <SplitForm doc={doc} pages={selPages} onClose={() => setSplitOpen(false)} onDone={nd => { setSplitOpen(false); setSelPages([]); toast('New document made'); navigate(`/documents/${nd.id}`) }} />
      </Modal>}
    </div>
  )
}

function normalize(d: Partial<Document>) {
  return { title: d.title, created: d.created, correspondent: d.correspondent ?? null, document_type: d.document_type ?? null, storage_path: d.storage_path ?? null, asn: d.asn ?? null, tags: [...(d.tags || [])].sort(), custom_fields: d.custom_fields, favorite: d.favorite, content: d.content }
}

function conf(s: Suggestions | null, kind: string, id: number) {
  const c = s?.confidence[`${kind}:${id}`]
  if (!c || c >= 1) return null
  return <small className="muted"> {Math.round(c * 100)}%</small>
}

function SplitForm({ doc, pages, onClose, onDone }: { doc: Document; pages: number[]; onClose: () => void; onDone: (d: Document) => void }) {
  const { toast } = useToast()
  const [title, setTitle] = useState(doc.title + ' (part)')
  const [remove, setRemove] = useState(true)
  const [busy, setBusy] = useState(false)
  const go = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { onDone(await api.post<Document>(`${V1}/documents/${doc.id}/pages`, { op: 'split', pages, title, remove })) } catch (err) { toast((err as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={go} className="grid" style={{ gap: 14 }}>
      <p className="muted small">Pages {pages.join(', ')} become a new document with the same tags, correspondent and type; OCR runs on it.</p>
      <Field label="Title"><input value={title} onChange={e => setTitle(e.target.value)} autoFocus /></Field>
      <label className="row"><input type="checkbox" checked={remove} onChange={e => setRemove(e.target.checked)} style={{ width: 'auto' }} /> Take the pages out of this document (the old file is kept as a version)</label>
      <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>Split</button></div>
    </form>
  )
}

function VersionsTab({ doc, onChange }: { doc: Document; onChange: () => Promise<unknown> }) {
  const { toast } = useToast()
  const [list, setList] = useState<Version[] | null>(null)
  useEffect(() => { api.get<Version[]>(`${V1}/documents/${doc.id}/versions`).then(setList) }, [doc.id, doc.modified_at])
  if (!list) return <Loading />
  if (list.length === 0) return <p className="muted">No earlier files. Rotating pages, removing pages, splitting and replacing the file keep the previous file here.</p>
  return (
    <table className="table compact"><thead><tr><th>#</th><th>Why</th><th>File</th><th>When</th><th /></tr></thead><tbody>
      {list.map(v => <tr key={v.id}><td className="muted">v{v.n}</td><td>{v.reason}<div className="muted small">{v.actor}</div></td><td className="muted small">.{v.ext} · {bytes(v.size)}</td><td className="muted small" title={stamp(v.created_at)}>{ago(v.created_at)}</td><td className="right nowrap"><a className="btn ghost xs" href={`${V1}/documents/${doc.id}/versions/${v.n}/download`}>Download</a><button className="btn ghost xs" onClick={async () => { try { await api.post(`${V1}/documents/${doc.id}/versions/${v.n}/restore`); toast('Restored; OCR running'); onChange() } catch (e) { toast((e as Error).message, 'err') } }}>Restore</button></td></tr>)}
    </tbody></table>
  )
}

function ShareWithModal({ doc, users, onClose }: { doc: Document; users: { id: number; name: string; email: string }[]; onClose: () => void }) {
  const { toast } = useToast()
  const [list, setList] = useState<DocShare[] | null>(null)
  const [uid, setUid] = useState<number>(users[0]?.id || 0)
  const [level, setLevel] = useState<'view' | 'edit'>('view')
  const load = useCallback(() => api.get<DocShare[]>(`${V1}/documents/${doc.id}/shared_with`).then(setList), [doc.id])
  useEffect(() => { load() }, [load])
  const add = async () => { try { setList(await api.post<DocShare[]>(`${V1}/documents/${doc.id}/shared_with`, { user_id: uid, level })); toast('Shared') } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Modal title="Share with someone on this box" onClose={onClose}>
      <p className="muted small">They see this one document under “Shared with me”, can open and download it, and with <i>edit</i> change its details and write notes. Nothing else of yours. Tags show as names only.</p>
      <div className="row wrap mb">
        <select value={uid} onChange={e => setUid(Number(e.target.value))} style={{ width: 'auto' }}>{users.map(u => <option key={u.id} value={u.id}>{u.name || u.email}</option>)}</select>
        <Segmented size="sm" value={level} onChange={setLevel} options={[{ value: 'view', label: 'View' }, { value: 'edit', label: 'Edit' }]} />
        <button className="btn primary sm" onClick={add} disabled={!uid}>Share</button>
      </div>
      {!list ? <Loading /> : list.length === 0 ? <p className="muted small">Not shared with anyone.</p> : <table className="table compact"><tbody>{list.map(s => <tr key={s.user_id}><td>{s.name || s.email}</td><td><span className="badge">{s.level}</span></td><td className="muted small">{ago(s.created_at)}</td><td className="right"><button className="btn ghost xs danger-text" onClick={async () => { await api.del(`${V1}/documents/${doc.id}/shared_with/${s.user_id}`); load() }}>Remove</button></td></tr>)}</tbody></table>}
    </Modal>
  )
}

function LanguageModal({ doc, onClose, onDone }: { doc: Document; onClose: () => void; onDone: () => void }) {
  const { toast } = useToast()
  const { settings } = useApp()
  const [lang, setLang] = useState(doc.language || settings?.ocr_language || 'eng')
  const [mode, setMode] = useState<'skip' | 'redo' | 'force'>('redo')
  const installed = settings?.tools.languages || []
  const go = async (e: React.FormEvent) => {
    e.preventDefault()
    try { await api.post(`${V1}/documents/${doc.id}/reprocess`, { mode, language: lang.trim() === (settings?.ocr_language || '') ? '' : lang.trim() }); toast('Queued for OCR'); onDone() } catch (err) { toast((err as Error).message, 'err') }
  }
  return (
    <Modal title="Run OCR again" onClose={onClose}>
      <form onSubmit={go} className="grid" style={{ gap: 14 }}>
        <Field label="Languages for this document" help={<>Tesseract codes joined with +. Installed: {installed.join(', ')}. Saved on the document; empty or the box default = the box default.</>}><input value={lang} onChange={e => setLang(e.target.value)} className="mono" /></Field>
        <div className="row wrap">{installed.map(l => <button key={l} type="button" className={`chip ${lang.split('+').includes(l) ? 'active' : ''}`} onClick={() => setLang(v => v.split('+').filter(Boolean).includes(l) ? v.split('+').filter(x => x && x !== l).join('+') : [...v.split('+').filter(Boolean), l].join('+'))}>{l}</button>)}</div>
        <Field label="Mode" group help={{ skip: 'Only pages without text.', redo: 'Redo scanned pages, keep real text.', force: 'Rasterise everything and OCR it all (replaces the text layer).' }[mode]}><Segmented value={mode} onChange={setMode} options={[{ value: 'skip', label: 'Skip text' }, { value: 'redo', label: 'Redo' }, { value: 'force', label: 'Force' }]} size="sm" /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Run OCR</button></div>
      </form>
    </Modal>
  )
}

function PageImages({ doc, selected = [], onToggle }: { doc: Document; selected?: number[]; onToggle?: (n: number) => void }) {
  const n = Math.max(1, doc.page_count || 1)
  const [shown, setShown] = useState(Math.min(n, 3))
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => { setShown(Math.min(n, 3)) }, [doc.id, n])
  useEffect(() => {
    const el = ref.current
    if (!el || shown >= n) return
    const io = new IntersectionObserver(es => { if (es.some(e => e.isIntersecting)) setShown(s => Math.min(n, s + 3)) })
    io.observe(el)
    return () => io.disconnect()
  }, [shown, n])
  return <div className="pages-view">{Array.from({ length: shown }, (_, i) => <div key={i} className={`page-wrap ${selected.includes(i + 1) ? 'sel' : ''}`}><img src={`${V1}/documents/${doc.id}/pages/${i + 1}.jpg?v=${encodeURIComponent(doc.modified_at)}`} alt={`Page ${i + 1}`} loading="lazy" onClick={() => onToggle?.(i + 1)} /><span className="pn" onClick={() => onToggle?.(i + 1)}>{selected.includes(i + 1) ? '✓ ' : ''}{i + 1}</span></div>)}{shown < n && <div ref={ref} className="muted small center">Loading more pages…</div>}</div>
}

function FieldsEditor({ fields, values, onChange }: { fields: CustomField[]; values: FieldValue[]; onChange: (v: FieldValue[]) => void }) {
  const set = (id: number, v: string) => onChange([...values.filter(x => x.field !== id), { field: id, value: v }])
  const remove = (id: number) => onChange(values.filter(x => x.field !== id))
  const present = fields.filter(f => values.some(v => v.field === f.id))
  const absent = fields.filter(f => !values.some(v => v.field === f.id))
  return (
    <div className="fields-editor">
      {present.map(f => { const v = values.find(x => x.field === f.id)?.value || ''; return (
        <div key={f.id} className="field-row">
          <span className="label">{f.name}{f.data_type === 'monetary' && f.extra?.currency ? ` (${f.extra.currency})` : ''}</span>
          {f.data_type === 'boolean' ? <select value={v} onChange={e => set(f.id, e.target.value)}><option value="">—</option><option value="true">yes</option><option value="false">no</option></select>
            : f.data_type === 'select' ? <select value={v} onChange={e => set(f.id, e.target.value)}><option value="">—</option>{(f.extra?.options || []).map(o => <option key={o} value={o}>{o}</option>)}</select>
            : f.data_type === 'date' ? <input type="date" value={v} onChange={e => set(f.id, e.target.value)} />
            : f.data_type === 'integer' || f.data_type === 'float' || f.data_type === 'monetary' ? <input value={v} inputMode="decimal" onChange={e => set(f.id, e.target.value)} placeholder={f.data_type === 'monetary' ? '0.00' : ''} />
            : f.data_type === 'url' ? <span className="row grow"><input value={v} onChange={e => set(f.id, e.target.value)} placeholder="https://" />{v && <a className="btn ghost sm" href={v} target="_blank" rel="noreferrer">↗</a>}</span>
            : f.data_type === 'documentlink' ? <span className="row grow"><input value={v} onChange={e => set(f.id, e.target.value)} placeholder="document id" inputMode="numeric" />{v && <Link className="btn ghost sm" to={`/documents/${v}`}>↗</Link>}</span>
            : <input value={v} onChange={e => set(f.id, e.target.value)} />}
          <button type="button" className="btn ghost xs" onClick={() => remove(f.id)} aria-label="Remove field">×</button>
        </div>) })}
      {absent.length > 0 && <select className="add-field" value="" onChange={e => { if (e.target.value) set(Number(e.target.value), '') }}><option value="">+ Add a field…</option>{absent.map(f => <option key={f.id} value={f.id}>{f.name}</option>)}</select>}
    </div>
  )
}

function NotesTab({ doc, onChange }: { doc: Document; onChange: () => Promise<unknown> }) {
  const { toast } = useToast()
  const { users } = useApp()
  const [notes, setNotes] = useState<Note[] | null>(null)
  const [text, setText] = useState('')
  const load = useCallback(() => api.get<Note[]>(`${V1}/documents/${doc.id}/notes`).then(setNotes), [doc.id])
  useEffect(() => { load() }, [load])
  const add = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!text.trim()) return
    try { await api.post(`${V1}/documents/${doc.id}/notes`, { text }); setText(''); await load(); onChange() } catch (err) { toast((err as Error).message, 'err') }
  }
  return (
    <div className="grid" style={{ gap: 12 }}>
      <form onSubmit={add} className="grid" style={{ gap: 8 }}><textarea value={text} onChange={e => setText(e.target.value)} placeholder="Write a note… (searchable)" rows={3} onKeyDown={e => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') add(e) }} /><div className="row"><span className="spacer" /><button className="btn primary sm" disabled={!text.trim()}>Add note</button></div></form>
      {!notes ? <Loading /> : notes.length === 0 ? <p className="muted">No notes yet.</p> : notes.map(n => <div key={n.id} className="note"><div className="note-text">{n.text}</div><div className="muted small row"><span>{users.find(u => u.id === n.user_id)?.name || 'you'} · <span title={stamp(n.created_at)}>{ago(n.created_at)}</span></span><span className="spacer" /><button className="link-btn small danger-text" onClick={async () => { await api.del(`${V1}/documents/${doc.id}/notes/${n.id}`); await load(); onChange() }}>delete</button></div></div>)}
    </div>
  )
}

function HistoryTab({ doc }: { doc: Document }) {
  const { tax } = useApp()
  const [items, setItems] = useState<HistoryEntry[] | null>(null)
  useEffect(() => { api.get<HistoryEntry[]>(`${V1}/documents/${doc.id}/history`).then(setItems) }, [doc.id, doc.modified_at])
  const name = (k: string, v: unknown): string => {
    if (v == null) return '—'
    const id = typeof v === 'number' ? v : Number(v)
    if (k === 'correspondent') return tax.correspondents.find(x => x.id === id)?.name || `#${id}`
    if (k === 'document_type') return tax.document_types.find(x => x.id === id)?.name || `#${id}`
    if (k === 'storage_path') return tax.storage_paths.find(x => x.id === id)?.name || `#${id}`
    if (k === 'tags' && Array.isArray(v)) return v.map(t => tax.tags.find(x => x.id === t)?.name || `#${t}`).join(', ') || '—'
    if (k === 'custom_fields' && typeof v === 'object') return Object.entries(v as Record<string, string>).map(([f, val]) => `${tax.custom_fields.find(x => x.id === Number(f))?.name || f}=${val}`).join(', ') || '—'
    if (typeof v === 'object') return JSON.stringify(v)
    return String(v)
  }
  if (!items) return <Loading />
  return (
    <ul className="history">{items.map(h => <li key={h.id}>
      <div className="row"><b>{h.action}</b><span className="muted small">by {h.actor} · <span title={stamp(h.created_at)}>{ago(h.created_at)}</span></span></div>
      {h.changes && Object.keys(h.changes).length > 0 && <div className="small muted changes">{Object.entries(h.changes).map(([k, v]) => <div key={k}>{Array.isArray(v) && v.length === 2 && ['title', 'created', 'correspondent', 'document_type', 'storage_path', 'asn', 'tags', 'custom_fields', 'favorite'].includes(k) ? <><span className="key">{k}</span> {name(k, v[0])} → {name(k, v[1])}</> : <><span className="key">{k}</span> {typeof v === 'object' ? JSON.stringify(v) : String(v)}</>}</div>)}</div>}
    </li>)}</ul>
  )
}

function ShareModal({ doc, onClose }: { doc: Document; onClose: () => void }) {
  const { toast } = useToast()
  const [list, setList] = useState<Share[] | null>(null)
  const [days, setDays] = useState(7)
  const [file, setFile] = useState<'archive' | 'original'>('archive')
  const load = useCallback(() => api.get<Share[]>(`${V1}/documents/${doc.id}/shares`).then(setList), [doc.id])
  useEffect(() => { load() }, [load])
  const make = async () => { try { const s = await api.post<Share>(`${V1}/documents/${doc.id}/shares`, { days, file }); await load(); await navigator.clipboard?.writeText(s.url).catch(() => {}); toast('Link created and copied') } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Modal title="Share link" onClose={onClose}>
      <p className="muted small">Anyone with the link can view and download this document, no account needed.</p>
      <div className="row wrap mb">
        <Segmented size="sm" value={days} onChange={setDays} options={[{ value: 1, label: '1 day' }, { value: 7, label: '1 week' }, { value: 30, label: '1 month' }, { value: 0, label: 'No expiry' }]} />
        {doc.has_archive && <Segmented size="sm" value={file} onChange={setFile} options={[{ value: 'archive', label: 'PDF/A' }, { value: 'original', label: 'Original' }]} />}
        <button className="btn primary sm" onClick={make}>Create link</button>
      </div>
      {!list ? <Loading /> : list.length === 0 ? <p className="muted small">No links yet.</p> : <table className="table compact"><tbody>{list.map(s => <tr key={s.id}><td className="mono small ellipsis" style={{ maxWidth: 260 }}><a href={s.url} target="_blank" rel="noreferrer">{s.url}</a></td><td className="muted small nowrap">{s.expires_at ? `until ${fmtDate(s.expires_at)}` : 'no expiry'} · {s.views} views</td><td className="right nowrap"><button className="btn ghost xs" onClick={() => navigator.clipboard?.writeText(s.url).then(() => toast('Copied'))}>Copy</button><button className="btn ghost xs danger-text" onClick={async () => { await api.del(`${V1}/shares/${s.id}`); load() }}>Revoke</button></td></tr>)}</tbody></table>}
    </Modal>
  )
}

function MoneyModal({ doc, onClose, onBooked }: { doc: Document; onClose: () => void; onBooked: () => Promise<unknown> }) {
  const { toast } = useToast()
  const { tax } = useApp()
  const [st, setSt] = useState<{ connected: boolean; error: string; accounts: { id: number; name: string; currency: string }[]; categories: { id: number; full_name: string; name: string }[]; default_account: number; url: string } | null>(null)
  const amountField = tax.custom_fields.find(f => f.data_type === 'monetary')
  const guess = doc.custom_fields.find(v => v.field === amountField?.id)?.value || ''
  const [form, setForm] = useState({ amount: guess, currency: amountField?.extra?.currency || 'EUR', account_id: 0, category_id: 0, date: doc.created, description: doc.title, payee: doc.correspondent_name || '' })
  const [busy, setBusy] = useState(false)
  useEffect(() => { api.get<typeof st>(`${V1}/money/status`).then(s => { setSt(s); if (s?.accounts.length) setForm(f => ({ ...f, account_id: s.default_account || s.accounts[0].id, currency: s.accounts.find(a => a.id === (s.default_account || s.accounts[0].id))?.currency || f.currency })) }) }, [])
  useEffect(() => { if (!form.account_id) return; api.get<typeof st>(`${V1}/money/status?account=${form.account_id}`).then(s => setSt(s)) }, [form.account_id])
  const book = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try {
      const r = await api.post<{ transaction: number; url: string }>(`${V1}/documents/${doc.id}/money`, { ...form, amount: Number(String(form.amount).replace(',', '.')), category_id: form.category_id || null })
      toast('Booked in Pocket Money'); await onBooked(); onClose()
      if (r.url) window.open(`${r.url}/transactions/${r.transaction}`, '_blank')
    } catch (err) { toast((err as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Book in Pocket Money" onClose={onClose}>
      {!st ? <Loading /> : !st.connected ? <div className="callout">{st.error || 'Pocket Money is not connected.'} <span className="muted small">(Settings → Integrations)</span></div> : (
        <form onSubmit={book} className="grid" style={{ gap: 12 }}>
          <div className="two-col tight">
            <Field label="Amount"><input value={form.amount} onChange={e => setForm({ ...form, amount: e.target.value })} inputMode="decimal" required autoFocus placeholder="0.00" /></Field>
            <Field label="Currency"><input value={form.currency} onChange={e => setForm({ ...form, currency: e.target.value.toUpperCase() })} maxLength={3} /></Field>
          </div>
          <Field label="Account"><select value={form.account_id} onChange={e => setForm({ ...form, account_id: Number(e.target.value) })}>{st.accounts.map(a => <option key={a.id} value={a.id}>{a.name} ({a.currency})</option>)}</select></Field>
          <Field label="Category"><select value={form.category_id} onChange={e => setForm({ ...form, category_id: Number(e.target.value) })}><option value={0}>— let Money decide —</option>{st.categories.map(c => <option key={c.id} value={c.id}>{c.full_name || c.name}</option>)}</select></Field>
          <div className="two-col tight">
            <Field label="Date"><input type="date" value={form.date} onChange={e => setForm({ ...form, date: e.target.value })} /></Field>
            <Field label="Payee"><input value={form.payee} onChange={e => setForm({ ...form, payee: e.target.value })} /></Field>
          </div>
          <Field label="Description"><input value={form.description} onChange={e => setForm({ ...form, description: e.target.value })} /></Field>
          <p className="muted small">The transaction links back to this document. {guess && <>Amount taken from the “{amountField?.name}” field: {money(guess, form.currency)}.</>}</p>
          <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>Book expense</button></div>
        </form>
      )}
    </Modal>
  )
}
