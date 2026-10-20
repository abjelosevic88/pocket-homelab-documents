import { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, Settings as SettingsT, User, Job } from '../lib/types'
import { bytes, stamp } from '../lib/format'
import { Card, Confirm, Field, Loading, Modal, PageHeader, Segmented, Tabs, Toggle, useAsync, useToast } from '../components/ui'
import { Avatar } from '../components/people'
import { Link } from 'react-router-dom'

type Tab = 'ocr' | 'prefs' | 'users' | 'profile' | 'api' | 'data' | 'integrations' | 'about'

export default function Settings() {
  const [tab, setTab] = useState<Tab>(() => (new URLSearchParams(window.location.search).get('tab') as Tab) || 'ocr')
  return (
    <>
      <PageHeader title="Settings" />
      <Tabs tabs={[{ id: 'ocr', label: 'OCR & storage' }, { id: 'prefs', label: 'Preferences' }, { id: 'users', label: 'Users' }, { id: 'profile', label: 'Profile' }, { id: 'api', label: 'API tokens' }, { id: 'data', label: 'Import & export' }, { id: 'integrations', label: 'Integrations' }, { id: 'about', label: 'About' }]} value={tab} onChange={setTab} />
      <div className="mt">
        {tab === 'ocr' && <OCR />}
        {tab === 'prefs' && <Prefs />}
        {tab === 'users' && <Users />}
        {tab === 'profile' && <Profile />}
        {tab === 'api' && <Tokens />}
        {tab === 'data' && <Data />}
        {tab === 'integrations' && <Integrations />}
        {tab === 'about' && <About />}
      </div>
    </>
  )
}

function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (extra: Record<string, unknown> = {}) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...extra }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return { f, setF, save, busy, isAdmin: user?.role === 'admin' }
}

function OCR() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  if (!f) return <Loading />
  const t = f.tools
  const langs = f.ocr_language.split('+').filter(Boolean)
  const missing = langs.filter(l => t.languages.length && !t.languages.includes(l))
  return (
    <div className="two-col">
      <Card title="OCR">
        {!t.ocrmypdf && <div className="callout warning mb"><b>OCR is off:</b> <code>ocrmypdf</code>{!t.tesseract && ' and tesseract'} not found on this server. The Docker image has them.</div>}
        <div className="grid" style={{ gap: 14 }}>
          <Field label="Languages" help={<>Tesseract codes joined with +, e.g. <code>eng+deu</code>. Installed: {t.languages.length ? t.languages.join(', ') : 'unknown'}.{missing.length > 0 && <span className="bad"> Not installed: {missing.join(', ')}.</span>}</>}><input value={f.ocr_language} onChange={e => setF({ ...f, ocr_language: e.target.value })} className="mono" disabled={!isAdmin} /></Field>
          <Field label="Mode" group help={{ skip: 'OCR only pages without text (fast; PDFs that already have text are kept).', redo: 'Redo OCR on scanned pages but keep real text; good for scanners that added a bad text layer.', force: 'Rasterise everything and OCR it all. Slow; use per document.' }[f.ocr_mode]}>
            <Segmented value={f.ocr_mode} onChange={v => setF({ ...f, ocr_mode: v })} options={[{ value: 'skip', label: 'Skip text' }, { value: 'redo', label: 'Redo' }, { value: 'force', label: 'Force' }]} />
          </Field>
          <Toggle checked={f.ocr_rotate} onChange={v => setF({ ...f, ocr_rotate: v })} label="Rotate pages that were scanned sideways" disabled={!isAdmin} />
          <Toggle checked={f.ocr_deskew} onChange={v => setF({ ...f, ocr_deskew: v })} label="Deskew slightly crooked scans" disabled={!isAdmin} />
          <Toggle checked={f.ocr_clean} onChange={v => setF({ ...f, ocr_clean: v })} label={<>Clean scans before OCR (unpaper{!t.unpaper && ', not installed'})</>} disabled={!isAdmin || !t.unpaper} />
          <div className="two-col tight">
            <Field label="Output" group><Segmented value={f.ocr_output_type} onChange={v => setF({ ...f, ocr_output_type: v })} options={[{ value: 'pdfa', label: 'PDF/A' }, { value: 'pdf', label: 'PDF' }]} size="sm" /></Field>
            <Field label="Max pages to OCR" help="0 = all"><input type="number" min={0} value={f.ocr_max_pages} onChange={e => setF({ ...f, ocr_max_pages: Number(e.target.value) })} disabled={!isAdmin} /></Field>
          </div>
          {isAdmin ? <div><button className="btn primary" onClick={() => save()} disabled={busy}>Save</button></div> : <p className="muted small">Only an admin changes these.</p>}
        </div>
      </Card>
      <div className="grid" style={{ gap: 16 }}>
        <Card title="Files">
          <div className="grid" style={{ gap: 14 }}>
            <Field label="Consume folder" help="Files dropped here become documents. A sub-folder named like a person's email is theirs; other sub-folders become tags."><input value={f.consume_dir} readOnly className="mono" /></Field>
            <Toggle checked={f.consume_subdirs_as_tags} onChange={v => setF({ ...f, consume_subdirs_as_tags: v })} label="Sub-folders of the consume folder become tags" disabled={!isAdmin} />
            <hr />
            <Toggle checked={f.mirror} onChange={v => setF({ ...f, mirror: v })} label="Keep a browsable mirror folder laid out by storage path" disabled={!isAdmin} />
            {f.mirror && <>
              <Field label="Mirror folder"><input value={f.mirror_dir} readOnly className="mono" /></Field>
              <Field label="Which file" group><Segmented value={f.mirror_file} onChange={v => setF({ ...f, mirror_file: v })} options={[{ value: 'archive', label: 'PDF/A with text' }, { value: 'original', label: 'Original' }]} size="sm" /></Field>
              <p className="muted small">Hard links where the file system allows, copies otherwise. Point MIRROR_DIR at a NAS share and your archive is readable without this app.</p>
            </>}
            <Field label="Keep deleted documents for" help="Days in the trash before the files are removed; 0 = until you empty the trash."><input type="number" min={0} value={f.trash_days} onChange={e => setF({ ...f, trash_days: Number(e.target.value) })} disabled={!isAdmin} style={{ width: 120 }} /></Field>
            {isAdmin && <div><button className="btn primary" onClick={() => save()} disabled={busy}>Save</button></div>}
          </div>
        </Card>
        <Card title="Tools on this server">
          <table className="table compact"><tbody>
            {[['ocrmypdf', t.ocrmypdf, t.versions.ocrmypdf], ['tesseract', t.tesseract, t.versions.tesseract], ['poppler (pdftotext, pdftoppm)', t.pdftotext && t.pdftoppm, t.versions.poppler], ['unpaper', t.unpaper, ''], ['Gotenberg (office → PDF)', t.gotenberg, '']].map(([n, ok, v]) => <tr key={String(n)}><th>{n}</th><td>{ok ? <span className="ok">✓ {v}</span> : <span className="muted">not available</span>}</td></tr>)}
            <tr><th>Workers</th><td className="muted">{f.ocr_workers} document{f.ocr_workers > 1 ? 's' : ''} at a time (OCR_WORKERS)</td></tr>
            <tr><th>Upload limit</th><td className="muted">{bytes(f.max_upload_bytes)} (MAX_UPLOAD_BYTES)</td></tr>
          </tbody></table>
        </Card>
      </div>
    </div>
  )
}

function Prefs() {
  const { f, setF, save, busy } = useSettingsForm()
  const { user } = useApp()
  if (!f) return <Loading />
  return (
    <div className="two-col">
      <Card title="Your preferences">
        <div className="grid" style={{ gap: 14 }}>
          {user?.role === 'admin' && <Field label="App title"><input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} maxLength={40} /></Field>}
          <Field label="Dates" group><Segmented value={f.date_format} onChange={v => setF({ ...f, date_format: v })} options={[{ value: 'iso', label: '2026-03-14' }, { value: 'dmy-dot', label: '14.03.2026' }, { value: 'long', label: '14 March 2026' }]} size="sm" /></Field>
          <Field label="Documents page" group><Segmented value={f.default_view} onChange={v => setF({ ...f, default_view: v })} options={[{ value: 'table', label: 'Table' }, { value: 'cards', label: 'Cards' }, { value: 'list', label: 'List' }]} size="sm" /></Field>
          <div className="two-col tight">
            <Field label="Per page"><select value={f.page_size} onChange={e => setF({ ...f, page_size: Number(e.target.value) })}>{[25, 50, 100, 200].map(n => <option key={n} value={n}>{n}</option>)}</select></Field>
            <Field label="Default sort"><select value={f.default_sort} onChange={e => setF({ ...f, default_sort: e.target.value })}><option value="-created">Newest first</option><option value="created">Oldest first</option><option value="-added">Recently added</option><option value="title">Title</option><option value="asn">ASN</option></select></Field>
          </div>
          <Toggle checked={f.date_from_text} onChange={v => setF({ ...f, date_from_text: v })} label="Read the document date from the text when I did not set one" />
          <Toggle checked={f.suggest_on_open} onChange={v => setF({ ...f, suggest_on_open: v })} label="Show suggestions (tags, correspondent, type, dates) when I open a document" />
          <div><button className="btn primary" onClick={() => save()} disabled={busy}>Save</button></div>
        </div>
      </Card>
      <Card title="Keyboard">
        <table className="table compact"><tbody>
          <tr><th><kbd className="kbd">⌘K</kbd> <kbd className="kbd">/</kbd></th><td>Search</td></tr>
          <tr><th><kbd className="kbd">u</kbd></th><td>Upload</td></tr>
          <tr><th><kbd className="kbd">g</kbd> then <kbd className="kbd">d</kbd> <kbd className="kbd">l</kbd> <kbd className="kbd">i</kbd> <kbd className="kbd">j</kbd> <kbd className="kbd">m</kbd> <kbd className="kbd">t</kbd> <kbd className="kbd">s</kbd></th><td>Go to dashboard, list, inbox, jobs, manage, trash, settings</td></tr>
          <tr><th><kbd className="kbd">←</kbd> <kbd className="kbd">→</kbd></th><td>Previous / next document</td></tr>
          <tr><th><kbd className="kbd">⌘S</kbd></th><td>Save the document</td></tr>
          <tr><th><kbd className="kbd">⇧</kbd> + click</th><td>Select a range in the list</td></tr>
        </tbody></table>
      </Card>
    </div>
  )
}

function Users() {
  const { user, users, reloadTax, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); await reloadTax(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Card title="Users" actions={isAdmin && <button className="btn primary sm" onClick={() => setEdit({ role: 'member' })}>+ Add user</button>}>
      <p className="muted">Everyone signs in with their own account. Documents, tags, correspondents, types, fields, rules and views are strictly per person; admins manage accounts and the OCR settings, nothing else.</p>
      <table className="table compact">
        <thead><tr><th /><th>Name</th><th>Email</th><th>Role</th><th /></tr></thead>
        <tbody>
          {users.map(u => (
            <tr key={u.id}>
              <td style={{ width: 32 }}><Avatar user={u} /></td>
              <td>{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge" style={{ marginLeft: 6 }}>you</span>}</td>
              <td className="muted">{u.email}</td>
              <td><span className={`badge ${u.role === 'admin' ? 'cat' : ''}`}>{u.role}</span></td>
              <td className="right">{isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit; passwords are not used here.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadTax() }} />}
      {del && <Confirm title={`Remove ${del.name || del.email}?`} message="Their account and every document, tag and setting of theirs are deleted." onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); run(() => api.del(`${V1}/users/${d.id}`), 'User removed') }} />}
    </Card>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try {
      if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f)
      toast(user.id ? 'User saved' : 'User added'); onSaved()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit user' : 'Add user'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add user'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Card title="Profile">
      <form onSubmit={save} className="grid" style={{ gap: 14, maxWidth: 480 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
        <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
        {status?.auth_mode === 'local' && <>
          <hr />
          <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
          <Field label="New password" help="At least 8 characters. Leave empty to keep the current one."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
        </>}
        <div><button className="btn primary" disabled={busy}>Save</button></div>
      </form>
    </Card>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="two-col">
      <Card title="API tokens">
        <p className="muted">Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you: upload (<code>POST /api/v1/documents</code>), search, download, feed a Homepage widget (<code>GET /api/v1/dashboard</code>). See <code>docs/api.md</code>.</p>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. scanner script)" /><button className="btn primary">Create</button></form>
        {fresh && <div className="callout success mb"><b>Copy it now — it is shown once.</b><pre className="mono" style={{ margin: '8px 0 0' }}>{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? stamp(t.last_used_at) : 'never'}</td><td className="muted">{stamp(t.created_at)}</td><td className="right"><button className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Card>
      <Card title="Examples">
        <pre>{`# upload a scan with two tags
curl -F document=@scan.pdf -F tags=1,2 -F title="Water bill" \\
  -H "Authorization: Bearer pd_…" ${location.origin}/api/v1/documents

# search
curl -H "Authorization: Bearer pd_…" \\
  "${location.origin}/api/v1/documents?q=invoice+created:2026&limit=10"

# the PDF/A with its text layer
curl -OJ -H "Authorization: Bearer pd_…" ${location.origin}/api/v1/documents/12/download`}</pre>
        <p className="muted small mt">iOS Shortcut: Get File → Get Contents of URL (POST, form, field <code>document</code>, header <code>Authorization</code>). Or just share a photo to the installed web app.</p>
      </Card>
    </div>
  )
}

function Data() {
  const { toast } = useToast()
  const { user, reloadTax, bump, stamp: st } = useApp()
  const isAdmin = user?.role === 'admin'
  const [busy, setBusy] = useState(false)
  const [pl, setPl] = useState({ url: '', token: '' })
  const [plTest, setPlTest] = useState('')
  const [job, setJob] = useState<Job | null>(null)
  useEffect(() => { if (job && (job.status === 'queued' || job.status === 'running')) api.get<Job>(`${V1}/jobs/${job.id}`).then(setJob).catch(() => {}) }, [st]) // eslint-disable-line react-hooks/exhaustive-deps
  const importZip = async (file: File) => {
    setBusy(true)
    try {
      const fd = new FormData(); fd.append('file', file)
      const res = await fetch(`${V1}/import`, { method: 'POST', body: fd, credentials: 'same-origin' })
      const j = await res.json()
      if (!res.ok) throw new Error(j.error || res.statusText)
      toast(`Imported ${j.documents} documents${j.skipped ? `, ${j.skipped} already here` : ''}${j.errors?.length ? `, ${j.errors.length} errors` : ''}`); reloadTax(); bump()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const test = async () => { setPlTest('…'); try { const r = await api.post<{ documents: number }>(`${V1}/import/paperless/test`, pl); setPlTest(`Connected: ${r.documents} documents there`) } catch (e) { setPlTest((e as Error).message) } }
  const start = async () => { try { setJob(await api.post<Job>(`${V1}/import/paperless`, pl)); toast('Import started (see Processing)') } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <div className="grid" style={{ gap: 16 }}>
      <div className="two-col">
        <Card title="Export">
          <p className="muted">Everything of yours as one zip: a <code>manifest.json</code> with all metadata, notes, tags, correspondents, types, paths, fields, views and rules, plus the original files and the PDF/A twins. It imports here or on another box.</p>
          <div className="row wrap"><a className="btn primary" href={`${V1}/export`}>Download everything</a><a className="btn" href={`${V1}/export?no_archive=1`}>Originals only (smaller)</a></div>
        </Card>
        <Card title="Import">
          <p className="muted">A zip from the export above, or a <b>Paperless-ngx export folder</b> (<code>document_exporter</code>) zipped up. Tags and the rest are matched by name; files already here are skipped.</p>
          <label className="btn">{busy ? 'Importing…' : 'Import zip…'}<input type="file" accept=".zip,application/zip" hidden disabled={busy} onChange={e => e.target.files?.[0] && importZip(e.target.files[0])} /></label>
          <p className="muted small mt-s">Large exports: the upload runs in this request; keep the tab open. OCR of the imported files runs afterwards in the background.</p>
        </Card>
      </div>
      <Card title="Import from a running Paperless-ngx">
        <p className="muted">Pulls every document the token can see, with tags, correspondents, types, storage paths, custom fields and notes, over Paperless' API. Nothing is changed there. Make a token in Paperless under your profile (Django admin → Tokens).</p>
        <div className="two-col tight">
          <Field label="Paperless URL"><input value={pl.url} onChange={e => setPl({ ...pl, url: e.target.value })} placeholder="http://paperless:8000" /></Field>
          <Field label="API token"><input value={pl.token} onChange={e => setPl({ ...pl, token: e.target.value })} type="password" autoComplete="off" /></Field>
        </div>
        <div className="row wrap mt-s"><button className="btn" onClick={test} disabled={!pl.url || !pl.token}>Test</button><button className="btn primary" onClick={start} disabled={!pl.url || !pl.token}>Start import</button><span className="muted small">{plTest}</span></div>
        {job && <p className="small mt-s"><span className={`badge st-${job.status}`}>{job.status}</span> {job.progress} {job.error && <span className="bad">{job.error}</span>} <Link to="/jobs">log</Link></p>}
      </Card>
      <div className="two-col">
        {isAdmin && <Card title="Database backup">
          <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>) with everyone's metadata. The files live next to it under <code>originals/</code> and <code>archive/</code>; back up the whole data directory. Admins only.</p>
          <a className="btn" href={`${V1}/backup`}>Download database</a>
        </Card>}
        <Card title="Maintenance">
          <div className="row wrap">
            <button className="btn" onClick={async () => { await api.post(`${V1}/reindex`); toast('Rebuilding the search index (see Processing)') }}>Rebuild search index</button>
            <button className="btn" onClick={async () => { const r = await api.post<{ trained_on: number }>(`${V1}/retrain`); toast(`Classifier trained on ${r.trained_on} documents`) }}>Retrain classifier</button>
          </div>
          <p className="muted small mt-s">Neither is normally needed: the index follows every change and the classifier retrains itself.</p>
        </Card>
      </div>
    </div>
  )
}

function Integrations() {
  const { f, setF, save, busy } = useSettingsForm()
  const { toast } = useToast()
  const [token, setToken] = useState('')
  const [money, setMoney] = useState<{ connected: boolean; via: string; error: string; url: string } | null>(null)
  const check = () => api.get<typeof money>(`${V1}/money/status`).then(setMoney)
  useEffect(() => { check() }, [])
  if (!f) return <Loading />
  return (
    <div className="two-col">
      <Card title="Pocket Money">
        <p className="muted">Book an invoice or a receipt as an expense from its page. {f.core ? <>With Pocket Core on the box this works through an app link, for every person, with nothing to paste.</> : <>Without Pocket Core, paste a Money URL and one of your Money API tokens.</>}</p>
        <div className="grid" style={{ gap: 12 }}>
          <Field label="Connection" group><Segmented value={f.money_mode} onChange={v => setF({ ...f, money_mode: v })} options={[{ value: 'core', label: 'Pocket Core' }, { value: 'direct', label: 'Direct' }, { value: 'off', label: 'Off' }]} size="sm" /></Field>
          {f.money_mode === 'direct' && <>
            <Field label="Money URL"><input value={f.money_url} onChange={e => setF({ ...f, money_url: e.target.value })} placeholder="https://money.example.com" /></Field>
            <Field label="Money API token" help={f.money_token_set ? 'A token is saved; enter a new one to replace it.' : undefined}><input type="password" value={token} onChange={e => setToken(e.target.value)} autoComplete="off" /></Field>
          </>}
          <div className="row wrap"><button className="btn primary" onClick={async () => { await save(token ? { money_token: token } : {}); setToken(''); check() }} disabled={busy}>Save</button><button className="btn" onClick={check}>Test</button>{money && <span className={`small ${money.connected ? 'ok' : 'muted'}`}>{money.connected ? `Connected via ${money.via}` : money.error}</span>}</div>
        </div>
      </Card>
      <Card title="Notifications">
        <p className="muted">When a document cannot be processed, a message goes to this URL: an Apprise API endpoint (<code>https://apprise/notify/key</code>) or an ntfy topic (<code>https://ntfy.sh/topic</code>).</p>
        <div className="grid" style={{ gap: 12 }}>
          <Field label="URL"><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="https://…" /></Field>
          <div className="row wrap"><button className="btn primary" onClick={() => save()} disabled={busy}>Save</button><button className="btn" onClick={async () => { try { await api.post(`${V1}/settings/notify/test`, { url: f.notify_url }); toast('Sent') } catch (e) { toast((e as Error).message, 'err') } }} disabled={!f.notify_url}>Send a test</button></div>
        </div>
        {f.core && <p className="muted small mt">Pocket Core is connected: this app publishes <code>documents.added</code>, <code>documents.processed</code>, <code>documents.updated</code>, <code>documents.deleted</code> and <code>documents.failed</code>, and offers <code>documents:read</code> / <code>documents:write</code> to linked apps.</p>}
      </Card>
    </div>
  )
}

function About() {
  const { data } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const counts = (data?.counts || {}) as Record<string, number>
  const core = data?.core as { configured: boolean; registered: boolean; error: string } | undefined
  return (
    <Card title="About">
      <p><b>Pocket Documents</b> is a self-hosted document archive: scans and files OCR'd into searchable PDF/A, tagged, filed with rules and a classifier that learns from you, found again with full-text search. A Paperless-ngx replacement in one Go binary with one SQLite file.</p>
      <p className="muted small">Open source (MIT) — <a href="https://github.com/abjelosevic88/pocket-homelab-documents" target="_blank" rel="noreferrer">github.com/abjelosevic88/pocket-homelab-documents</a>. OCR by <a href="https://ocrmypdf.readthedocs.io" target="_blank" rel="noreferrer">OCRmyPDF</a> and Tesseract, rendering by Poppler.</p>
      {data && (
        <table className="table compact mt">
          <tbody>
            <tr><th>Version</th><td className="mono">{String(data.version)}</td></tr>
            <tr><th>Runtime</th><td className="mono">{String(data.go)} · {String(data.os)}/{String(data.arch)}</td></tr>
            <tr><th>Auth mode</th><td className="mono">{String(data.auth_mode)}</td></tr>
            {data.admin === true && <tr><th>Storage</th><td className="mono">db {bytes(Number(data.db_bytes))} · originals {bytes(Number(data.originals_bytes))} · archive {bytes(Number(data.archive_bytes))} · thumbs {bytes(Number(data.thumbs_bytes))} · page cache {bytes(Number(data.pages_bytes))}</td></tr>}
            {data.admin === true && <tr><th>Data dir</th><td className="mono">{String(data.data_dir)}</td></tr>}
            <tr><th>Timezone</th><td className="mono">{String(data.timezone)}</td></tr>
            <tr><th>Uptime</th><td className="mono">{String(data.uptime)}</td></tr>
            <tr><th>Yours</th><td className="mono">{counts.documents} documents · {counts.tags} tags · {counts.correspondents} correspondents · {counts.document_types} types · {counts.rules} rules</td></tr>
            <tr><th>Classifier</th><td className="mono">trained on {String(data.classifier ?? 0)} documents</td></tr>
            {core?.configured && <tr><th>Pocket Core</th><td className="mono">{core.registered ? 'registered' : `not registered: ${core.error}`}</td></tr>}
            {data.admin === true && <tr><th>Users</th><td className="mono">{counts.users}</td></tr>}
          </tbody>
        </table>
      )}
    </Card>
  )
}
