import { useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { api, V1 } from '../lib/api'
import { uploadFiles, type UploadItem } from '../lib/upload'
import { bytes } from '../lib/format'
import { Card, Field, PageHeader, useToast } from '../components/ui'
import { Picker } from '../components/Picker'

export default function Upload() {
  const { tax, settings, reloadTax, bump } = useApp()
  const { toast } = useToast()
  const [files, setFiles] = useState<File[]>([])
  const [items, setItems] = useState<UploadItem[]>([])
  const [busy, setBusy] = useState(false)
  const [meta, setMeta] = useState({ title: '', created: '', correspondent: null as number | null, document_type: null as number | null, storage_path: null as number | null, tags: [] as number[], asn: '' })
  const [over, setOver] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const camera = useRef<HTMLInputElement>(null)
  const add = (list: FileList | File[]) => setFiles(f => [...f, ...Array.from(list)])
  const create = (kind: 'tags' | 'correspondents' | 'document_types' | 'storage_paths') => async (name: string) => { const o = await api.post<{ id: number; name: string; color?: string }>(`${V1}/${kind}`, { name }); await reloadTax(); return o }
  const go = async () => {
    if (files.length === 0) return
    setBusy(true)
    const m: Record<string, string> = { title: files.length === 1 ? meta.title : '', created: meta.created, asn: files.length === 1 ? meta.asn : '', tags: meta.tags.join(',') }
    if (meta.correspondent) m.correspondent = String(meta.correspondent)
    if (meta.document_type) m.document_type = String(meta.document_type)
    if (meta.storage_path) m.storage_path = String(meta.storage_path)
    const res = await uploadFiles(files, m, setItems)
    setBusy(false); setFiles([]); bump()
    const ok = res.filter(r => r.status === 'done').length
    toast(ok === res.length ? `${ok} uploaded, OCR running` : `${ok} of ${res.length} uploaded`, ok === res.length ? 'ok' : 'err')
  }
  const max = settings?.max_upload_bytes || 200 << 20
  return (
    <>
      <PageHeader title="Upload" sub="PDFs, scans and photos get OCR; office files, e-mails and text are read as they are. Files can also be dropped anywhere in the app, or into the consume folder." />
      <div className="upload-grid">
        <div>
          <div className={`dropzone ${over ? 'over' : ''}`} onDragOver={e => { e.preventDefault(); setOver(true) }} onDragLeave={() => setOver(false)} onDrop={e => { e.preventDefault(); setOver(false); add(e.dataTransfer.files) }} onClick={() => input.current?.click()}>
            <div className="dz-ico">⇡</div>
            <div><b>Drop files here</b> or click to choose</div>
            <div className="muted small">PDF, JPG, PNG, TIFF, WEBP, HEIC · DOCX, XLSX, PPTX, ODT · TXT, MD, HTML, EML · up to {bytes(max)} each</div>
            <input ref={input} type="file" multiple hidden accept=".pdf,.png,.jpg,.jpeg,.tif,.tiff,.webp,.gif,.bmp,.heic,.docx,.xlsx,.pptx,.odt,.ods,.odp,.rtf,.txt,.md,.csv,.html,.htm,.eml,.json" onChange={e => { if (e.target.files) add(e.target.files); e.target.value = '' }} />
            <input ref={camera} type="file" accept="image/*" capture="environment" hidden onChange={e => { if (e.target.files) add(e.target.files); e.target.value = '' }} />
          </div>
          <div className="row mt wrap">
            <button className="btn" onClick={() => camera.current?.click()}>📷 Take a photo</button>
            {files.length > 0 && <button className="btn primary" onClick={go} disabled={busy}>{busy ? 'Uploading…' : `Upload ${files.length} file${files.length > 1 ? 's' : ''}`}</button>}
            {files.length > 0 && !busy && <button className="btn ghost" onClick={() => setFiles([])}>Clear</button>}
          </div>
          {files.length > 0 && <ul className="upload-list mt">{files.map((f, i) => <li key={i}><span className="grow ellipsis">{f.name}</span><span className="muted small">{bytes(f.size)}</span><button className="btn ghost xs" onClick={() => setFiles(l => l.filter((_, j) => j !== i))} aria-label="Remove">×</button></li>)}</ul>}
          {items.length > 0 && <Card className="mt" title="Uploaded">
            <ul className="upload-list">{items.map((it, i) => <li key={i} className={it.status}><span className="grow ellipsis">{it.file.name}</span>{it.status === 'uploading' && <span className="muted small">{Math.round(it.progress * 100)}%</span>}{it.status === 'done' && it.document && <Link to={`/documents/${it.document.id}`}>open</Link>}{it.status === 'error' && <span className="bad small">{it.error}{it.document && <> · <Link to={`/documents/${it.document.id}`}>open the existing one</Link></>}</span>}</li>)}</ul>
            <p className="muted small mt-s">OCR runs in the background; see <Link to="/jobs">Processing</Link>.</p>
          </Card>}
        </div>
        <Card title="Apply to these uploads" className="upload-meta">
          <div className="grid" style={{ gap: 12 }}>
            {files.length <= 1 && <Field label="Title" help="Empty = the file name. The date is read from the text when it can be."><input value={meta.title} onChange={e => setMeta({ ...meta, title: e.target.value })} /></Field>}
            <Field label="Document date"><input type="date" value={meta.created} onChange={e => setMeta({ ...meta, created: e.target.value })} /></Field>
            <Field label="Tags" group><Picker multi options={tax.tags} value={meta.tags} onChange={v => setMeta({ ...meta, tags: v as number[] })} placeholder="Tags…" onCreate={create('tags')} /></Field>
            <Field label="Correspondent" group><Picker options={tax.correspondents} value={meta.correspondent} onChange={v => setMeta({ ...meta, correspondent: v as number | null })} onCreate={create('correspondents')} /></Field>
            <Field label="Document type" group><Picker options={tax.document_types} value={meta.document_type} onChange={v => setMeta({ ...meta, document_type: v as number | null })} onCreate={create('document_types')} /></Field>
            <Field label="Storage path" group><Picker options={tax.storage_paths} value={meta.storage_path} onChange={v => setMeta({ ...meta, storage_path: v as number | null })} onCreate={create('storage_paths')} /></Field>
            {files.length <= 1 && <Field label="Archive serial number"><input value={meta.asn} onChange={e => setMeta({ ...meta, asn: e.target.value })} placeholder="optional" inputMode="numeric" /></Field>}
            <p className="muted small">What you leave empty is filled in by the matching rules and what the app learned from your other documents. Everything new also gets the inbox tag.</p>
          </div>
        </Card>
      </div>
      <Card className="mt" title="Other ways in">
        <div className="two-col">
          <div><h3>Consume folder and your Inbox</h3><p className="muted small">Anything dropped into <code>{settings?.consume_dir || 'DATA_DIR/consume'}</code> on the server becomes a document (a scanner's network share, Syncthing…). A sub-folder named after a person's email is theirs; other sub-folders become tags. With a <Link to="/settings?tab=folder">documents folder</Link> in your Nextcloud, its <b>Inbox</b> does the same from any device.</p></div>
          <div><h3>API and scripts</h3><pre>{`curl -F document=@scan.pdf -F tags=1,2 -H "Authorization: Bearer <token>" ${location.origin}/api/v1/documents`}</pre><p className="muted small">Tokens under Settings → API tokens. <code>/api/v1/documents/post_document</code> accepts what Paperless clients send.</p></div>
        </div>
      </Card>
    </>
  )
}
