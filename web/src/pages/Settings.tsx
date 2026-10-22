import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, Settings as SettingsT, User, Job, FolderInfo, FolderKind, Language } from '../lib/types'
import { ago, bytes, stamp } from '../lib/format'
import { Confirm, CopyField, Field, Loading, Modal, Segmented, SettingRow, Switch, Tabs, Toggle, useAsync, useIsMobile, useToast } from '../components/ui'
import { Avatar } from '../components/people'
import { Icon } from '../components/icons'
import { ShortcutsSheet } from '../components/ShortcutsSheet'
import MailSettings from './settings/Mail'
import { Select } from '../components/Select'

type TabId = 'prefs' | 'profile' | 'ocr' | 'folder' | 'mail' | 'notifications' | 'integrations' | 'users' | 'api' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string }[] = [
  { id: 'prefs', label: 'Preferences', icon: 'sliders', sub: 'Appearance, dates, how the documents list looks and what is read from new files.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'ocr', label: 'OCR & scanning', icon: 'scan', sub: 'How scans are cleaned up and read, the languages, barcodes and the consume folder.' },
  { id: 'folder', label: 'Documents folder', icon: 'folder', sub: 'A readable copy of your archive in Pocket Files, Nextcloud, WebDAV or a server folder, with an Inbox that feeds new files in.' },
  { id: 'mail', label: 'Mail', icon: 'mail', sub: 'Mailboxes checked over IMAP, and the rules that turn their attachments into documents.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'Where a message goes when a document cannot be processed or a rule says so.' },
  { id: 'integrations', label: 'Connected apps', icon: 'layers', sub: 'Pocket Money for booking invoices and receipts, and what Pocket Core sees of this app.' },
  { id: 'users', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box.' },
  { id: 'api', label: 'API tokens', icon: 'key', sub: 'Personal tokens for scripts, scanners, shortcuts and Paperless-compatible apps.' },
  { id: 'data', label: 'Import & export', icon: 'database', sub: 'Everything as one zip, imports from Paperless-ngx, the database backup and maintenance.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the server.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  // the old /settings?tab=x links still work: they land on /settings/x
  const legacy = new URLSearchParams(window.location.search).get('tab') || ''
  const want = raw || legacy
  const tab: TabId = TABS.find(t => t.id === want)?.id || 'prefs'
  const current = TABS.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  useEffect(() => { if (!raw && legacy && TABS.some(t => t.id === legacy)) navigate(`/settings/${legacy}`, { replace: true }) }, [raw, legacy, navigate])

  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || document.querySelector('.modal-backdrop, .menu, .sel-pop, .picker-list, .dropdown-pop')) return
      const t = e.target as HTMLElement
      if (t.closest?.('input, textarea')) { t.blur(); return }
      navigate('/')
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])

  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to the documents" title="Back to the documents (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={TABS.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {TABS.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title">
              <h2>{current.label}</h2>
              <p>{current.sub}</p>
            </div>
            {tab === 'prefs' && <Prefs />}
            {tab === 'profile' && <Profile />}
            {tab === 'ocr' && <OCR />}
            {tab === 'folder' && <Folder />}
            {tab === 'mail' && <MailSettings />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'integrations' && <Integrations />}
            {tab === 'users' && <Users />}
            {tab === 'api' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

/** A settings card: title, explanation, actions on the right — Pocket Calendar's section. */
function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico" style={{ background: 'var(--accent)' }}><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row wrap">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function SaveBar({ busy, onSave, isAdmin = true, note = 'Only an admin changes these.' }: { busy: boolean; onSave: () => void; isAdmin?: boolean; note?: string }) {
  return <div className="row mt-s">{isAdmin ? <button type="button" className="btn primary" onClick={onSave} disabled={busy}>{busy ? 'Saving…' : 'Save'}</button> : <span className="muted small">{note}</span>}</div>
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

// ---------- preferences ----------

function Prefs() {
  const { f, setF, save, busy } = useSettingsForm()
  const { user, themeMode, setThemeMode } = useApp()
  const [keys, setKeys] = useState(false)
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Appearance" sub="Kept in this browser.">
        <SettingRow title="Theme" help="System follows your device's light or dark mode.">
          <Segmented size="sm" label="Theme" value={themeMode} onChange={setThemeMode} options={[{ value: 'system', label: 'System' }, { value: 'light', label: 'Light' }, { value: 'dark', label: 'Dark' }]} />
        </SettingRow>
        {user?.role === 'admin' && <SettingRow title="App title" help="Shown in the sidebar and the browser tab, for everyone.">
          <input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} maxLength={40} style={{ width: 220 }} aria-label="App title" />
        </SettingRow>}
      </Section>
      <Section title="Dates & lists" sub="Yours alone; everyone on the box picks their own.">
        <SettingRow title="Dates">
          <Segmented size="sm" label="Dates" value={f.date_format} onChange={v => setF({ ...f, date_format: v })} options={[{ value: 'iso', label: '2026-03-14' }, { value: 'dmy-dot', label: '14.03.2026' }, { value: 'long', label: '14 March 2026' }]} />
        </SettingRow>
        <SettingRow title="Documents page" help="How the list opens; the buttons on the page switch it for the moment.">
          <Segmented size="sm" label="Documents page" value={f.default_view} onChange={v => setF({ ...f, default_view: v })} options={[{ value: 'table', label: 'Table' }, { value: 'cards', label: 'Cards' }, { value: 'list', label: 'List' }]} />
        </SettingRow>
        <SettingRow title="Per page">
          <div style={{ width: 120 }}><Select ariaLabel="Per page" value={String(f.page_size)} onChange={v => setF({ ...f, page_size: Number(v) })} options={[25, 50, 100, 200].map(n => ({ value: String(n), label: String(n) }))} /></div>
        </SettingRow>
        <SettingRow title="Default sort">
          <div style={{ width: 200 }}><Select ariaLabel="Default sort" value={f.default_sort} onChange={v => setF({ ...f, default_sort: v })} options={[{ value: '-created', label: 'Newest first' }, { value: 'created', label: 'Oldest first' }, { value: '-added', label: 'Recently added' }, { value: 'title', label: 'Title' }, { value: 'asn', label: 'ASN' }]} /></div>
        </SettingRow>
      </Section>
      <Section title="Reading new files">
        <SettingRow title="Date from the file name" help="2026-03-14_invoice.pdf, scan_20260314.pdf"><Switch label="Date from the file name" checked={f.date_from_name} onChange={v => setF({ ...f, date_from_name: v })} /></SettingRow>
        <SettingRow title="… or from the text" help="When I did not set a date myself."><Switch label="Date from the text" checked={f.date_from_text} onChange={v => setF({ ...f, date_from_text: v })} /></SettingRow>
        <SettingRow title="Suggestions" help="Show suggested tags, correspondent, type and dates when I open a document."><Switch label="Suggestions" checked={f.suggest_on_open} onChange={v => setF({ ...f, suggest_on_open: v })} /></SettingRow>
        <SaveBar busy={busy} onSave={() => save()} />
      </Section>
      <Section title="Keyboard" sub={<>Press <kbd>?</kbd> anywhere for the list, <kbd>⌘K</kbd> or <kbd>/</kbd> to search.</>} actions={<button type="button" className="btn sm" onClick={() => setKeys(true)}><Icon name="keyboard" size={15} />Shortcuts</button>} />
      {keys && <ShortcutsSheet onClose={() => setKeys(false)} />}
    </div>
  )
}

// ---------- profile ----------

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
    <Section title={<span className="row">{user && <Avatar user={user} size={28} />}{user?.name || user?.email}</span>} sub={user?.role === 'admin' ? 'Admin of this box' : 'Member'}>
      <form onSubmit={save} className="stack" style={{ maxWidth: 480 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoComplete="name" /></Field>
        <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} autoComplete="email" /></Field>
        {status?.auth_mode === 'local' && <>
          <hr />
          <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
          <Field label="New password" help="At least 8 characters. Leave empty to keep the current one."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
        </>}
        <div><button type="submit" className="btn primary" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button></div>
      </form>
    </Section>
  )
}

// ---------- OCR ----------

function OCR() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  if (!f) return <Loading />
  const t = f.tools
  const langs = f.ocr_language.split('+').filter(Boolean)
  const missing = langs.filter(l => t.languages.length && !t.languages.includes(l))
  const modeHelp = { skip: 'OCR only pages without text (fast; PDFs that already have text are kept).', redo: 'Redo OCR on scanned pages but keep real text; good for scanners that added a bad text layer.', force: 'Rasterise everything and OCR it all. Slow; use per document.' }[f.ocr_mode]
  return (
    <div className="stack loose">
      {!t.ocrmypdf && <div className="callout warning"><Icon name="alert" size={15} /><span><b>OCR is off:</b> <code>ocrmypdf</code>{!t.tesseract && ' and tesseract'} not found on this server. The Docker image has them.</span></div>}
      <Section title="Reading scans" sub={isAdmin ? 'For everyone on the box.' : 'For everyone on the box; only an admin changes these.'}>
        <SettingRow stack title="Languages" help={<>Tesseract codes joined with +, e.g. <code>eng+deu</code>. Installed: {t.languages.length ? t.languages.join(', ') : 'unknown'}.{missing.length > 0 && <span className="bad"> Not installed: {missing.join(', ')}.</span>}</>}>
          <input value={f.ocr_language} onChange={e => setF({ ...f, ocr_language: e.target.value })} className="mono" disabled={!isAdmin} aria-label="OCR languages" />
        </SettingRow>
        <SettingRow title="Mode" help={modeHelp}>
          <Segmented size="sm" label="Mode" value={f.ocr_mode} onChange={v => { if (isAdmin) setF({ ...f, ocr_mode: v }) }} options={[{ value: 'skip', label: 'Skip text' }, { value: 'redo', label: 'Redo' }, { value: 'force', label: 'Force' }]} />
        </SettingRow>
        <SettingRow title="Rotate pages that were scanned sideways"><Switch label="Rotate" checked={f.ocr_rotate} onChange={v => setF({ ...f, ocr_rotate: v })} disabled={!isAdmin} /></SettingRow>
        <SettingRow title="Deskew slightly crooked scans"><Switch label="Deskew" checked={f.ocr_deskew} onChange={v => setF({ ...f, ocr_deskew: v })} disabled={!isAdmin} /></SettingRow>
        <SettingRow title="Clean scans before OCR" help={<>With unpaper{!t.unpaper && ', which is not installed on this server'}.</>}><Switch label="Clean scans" checked={f.ocr_clean} onChange={v => setF({ ...f, ocr_clean: v })} disabled={!isAdmin || !t.unpaper} /></SettingRow>
        <SettingRow title="Output" help="PDF/A is the archival flavour of PDF.">
          <Segmented size="sm" label="Output" value={f.ocr_output_type} onChange={v => { if (isAdmin) setF({ ...f, ocr_output_type: v }) }} options={[{ value: 'pdfa', label: 'PDF/A' }, { value: 'pdf', label: 'PDF' }]} />
        </SettingRow>
        <SettingRow title="Max pages to OCR" help="0 = all"><input type="number" min={0} value={f.ocr_max_pages} onChange={e => setF({ ...f, ocr_max_pages: Number(e.target.value) })} disabled={!isAdmin} style={{ width: 100 }} aria-label="Max pages to OCR" /></SettingRow>
        <SaveBar busy={busy} onSave={() => save()} isAdmin={isAdmin} />
      </Section>
      <Languages inUse={f.ocr_language} onUse={code => setF({ ...f, ocr_language: f.ocr_language.split('+').filter(Boolean).includes(code) ? f.ocr_language : [...f.ocr_language.split('+').filter(Boolean), code].join('+') })} isAdmin={isAdmin} />
      <Section title="Barcodes" sub={<>On new scans: a barcode reading <code>{f.barcode_asn_prefix || 'ASN'}00042</code> sets the archive serial number; a separator sheet (a barcode reading <code>{f.barcode_separator}</code>, print one and put it between documents in a batch) splits the scan into one document per section. Needs zbar{!t.zbar && ' — not installed on this server'}.</>}>
        <SettingRow title="Read barcodes on new documents"><Switch label="Read barcodes" checked={f.barcodes_enabled} onChange={v => setF({ ...f, barcodes_enabled: v })} disabled={!isAdmin || !t.zbar} /></SettingRow>
        <SettingRow title="ASN prefix" help="Empty = no ASN barcodes."><input value={f.barcode_asn_prefix} onChange={e => setF({ ...f, barcode_asn_prefix: e.target.value })} disabled={!isAdmin} className="mono" style={{ width: 140 }} aria-label="ASN prefix" /></SettingRow>
        <SettingRow title="Separator code"><input value={f.barcode_separator} onChange={e => setF({ ...f, barcode_separator: e.target.value })} disabled={!isAdmin} className="mono" style={{ width: 180 }} aria-label="Separator code" /></SettingRow>
        <SettingRow title="Keep the separator page" help="As the first page of the next document."><Switch label="Keep the separator page" checked={f.barcode_keep_separator} onChange={v => setF({ ...f, barcode_keep_separator: v })} disabled={!isAdmin} /></SettingRow>
        <SettingRow title="Look at the first N pages"><input type="number" min={1} max={500} value={f.barcode_max_pages} onChange={e => setF({ ...f, barcode_max_pages: Number(e.target.value) })} disabled={!isAdmin} style={{ width: 100 }} aria-label="Pages to look at" /></SettingRow>
        <SaveBar busy={busy} onSave={() => save()} isAdmin={isAdmin} />
      </Section>
      <Section title="Files">
        <SettingRow stack title="Consume folder" help="Files dropped here become documents. A sub-folder named like a person's email is theirs; other sub-folders become tags."><CopyField value={f.consume_dir} label="Consume folder" /></SettingRow>
        <SettingRow title="Sub-folders of the consume folder become tags"><Switch label="Sub-folders become tags" checked={f.consume_subdirs_as_tags} onChange={v => setF({ ...f, consume_subdirs_as_tags: v })} disabled={!isAdmin} /></SettingRow>
        <SettingRow title="Keep deleted documents for" help="Days in the trash before the files are removed; 0 = until you empty the trash."><input type="number" min={0} value={f.trash_days} onChange={e => setF({ ...f, trash_days: Number(e.target.value) })} disabled={!isAdmin} style={{ width: 100 }} aria-label="Days in the trash" /></SettingRow>
        <SaveBar busy={busy} onSave={() => save()} isAdmin={isAdmin} />
      </Section>
      <Section title="Tools on this server">
        <dl className="set-dl">
          {([['ocrmypdf', t.ocrmypdf, t.versions.ocrmypdf], ['tesseract', t.tesseract, t.versions.tesseract], ['poppler (pdftotext, pdftoppm)', t.pdftotext && t.pdftoppm, t.versions.poppler], ['qpdf (page tools)', t.qpdf, ''], ['unpaper', t.unpaper, ''], ['zbar (barcodes)', t.zbar, ''], ['Gotenberg (office → PDF)', t.gotenberg, '']] as [string, boolean, string][]).map(([n, ok, v]) => (
            <div key={n}><dt>{n}</dt><dd>{ok ? <span className="ok row" style={{ gap: 5 }}><Icon name="check-circle" size={14} />{v || 'available'}</span> : <span className="muted">not available</span>}</dd></div>
          ))}
          <div><dt>Workers</dt><dd className="muted">{f.ocr_workers} document{f.ocr_workers > 1 ? 's' : ''} at a time (OCR_WORKERS)</dd></div>
          <div><dt>Upload limit</dt><dd className="muted">{bytes(f.max_upload_bytes)} (MAX_UPLOAD_BYTES)</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Languages({ inUse, onUse, isAdmin }: { inUse: string; onUse: (code: string) => void; isAdmin: boolean }) {
  const { toast } = useToast()
  const { data, setData } = useAsync(() => api.get<{ languages: Language[] }>(`${V1}/ocr/languages`), [])
  const [busy, setBusy] = useState('')
  const [q, setQ] = useState('')
  if (!data) return <Loading />
  const used = inUse.split('+').filter(Boolean)
  const install = async (code: string) => { setBusy(code); try { const r = await api.post<{ languages: Language[] }>(`${V1}/ocr/languages`, { code }); setData({ languages: r.languages }); toast(`${code} installed`) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy('') } }
  const remove = async (code: string) => { setBusy(code); try { const r = await api.del<{ languages: Language[] }>(`${V1}/ocr/languages/${code}`); setData({ languages: r.languages }); toast(`${code} removed`) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy('') } }
  const list = data.languages.filter(l => !q || l.name.toLowerCase().includes(q.toLowerCase()) || l.code.includes(q.toLowerCase()))
  const installed = list.filter(l => l.installed), more = list.filter(l => !l.installed)
  return (
    <Section title="Language packs" sub={<>Installed packs can be used in the OCR languages above (click one to add it). More can be fetched from tessdata_best (the most accurate models) without rebuilding anything. Serbian is covered in both alphabets: <code>srp</code> (Cyrillic) and <code>srp_latn</code> (Latin).</>}
      actions={<input value={q} onChange={e => setQ(e.target.value)} placeholder="Find…" className="sm" style={{ width: 140 }} aria-label="Find a language" />}>
      <div className="lang-grid">{installed.map(l => <span key={l.code} className={`chip ${used.includes(l.code) ? 'active' : ''}`} onClick={() => onUse(l.code)} title={l.source === 'system' ? 'Came with the image' : 'Downloaded'}>{used.includes(l.code) && <Icon name="check" size={13} />}{l.name} <code>{l.code}</code>{isAdmin && l.source === 'downloaded' && <button type="button" className="x" onClick={e => { e.stopPropagation(); remove(l.code) }} aria-label="Remove"><Icon name="x" size={12} /></button>}</span>)}</div>
      {isAdmin && more.length > 0 && <><h4 className="sc-h">Available</h4><div className="lang-grid">{more.slice(0, 60).map(l => <button key={l.code} type="button" className="chip" disabled={busy !== ''} onClick={() => install(l.code)}>{busy === l.code ? <span className="spinner sm" /> : <Icon name="download" size={13} />}{l.name} <code>{l.code}</code></button>)}</div></>}
    </Section>
  )
}

// ---------- documents folder ----------

const FOLDER_KINDS: { kind: FolderKind; title: string; sub: string; icon: string }[] = [
  { kind: 'none', title: 'None', sub: 'Documents live only in the app\'s data directory.', icon: 'x-circle' },
  { kind: 'files', title: 'Pocket Files', sub: 'A folder in your Pocket Files, through Pocket Core: nothing to type, with an Inbox you can drop scans into.', icon: 'folder' },
  { kind: 'nextcloud', title: 'Nextcloud', sub: 'A folder in your Nextcloud: synced to your devices, with an Inbox you can drop scans into.', icon: 'globe' },
  { kind: 'folder', title: 'Server folder', sub: 'A folder on this server: a NAS mount, a Syncthing folder.', icon: 'server' },
  { kind: 'webdav', title: 'WebDAV', sub: 'Any WebDAV server.', icon: 'link' },
]

function Folder() {
  const { data, reload } = useAsync(() => api.get<FolderInfo>(`${V1}/folder`), [])
  const { toast } = useToast()
  const { bump } = useApp()
  const [f, setF] = useState({ kind: 'none' as FolderKind, path: '', url: '', user: '', password: '', folder: 'Documents', via_core: true, file: 'archive' as 'archive' | 'original', inbox: true, inbox_name: 'Inbox' })
  const [test, setTest] = useState<{ where: string; files: number; folders: number; inbox_files: number } | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (data) setF({ kind: data.kind || 'none', path: data.path || (data.roots?.[0] ? `${data.roots[0]}` : ''), url: data.url, user: data.user, password: '', folder: data.folder || 'Documents', via_core: data.kind === 'nextcloud' ? data.via_core : !!data.core_nextcloud, file: data.file || 'archive', inbox: data.inbox, inbox_name: data.inbox_name || 'Inbox' })
  }, [data])
  if (!data) return <Loading />
  const st = data.status || {}
  const set = (p: Partial<typeof f>) => { setF({ ...f, ...p }); setTest(null) }
  const runTest = async () => {
    setBusy(true)
    try { setTest(await api.post(`${V1}/folder/test`, f)) } catch (e) { toast((e as Error).message, 'err'); setTest(null) } finally { setBusy(false) }
  }
  const save = async () => {
    setBusy(true)
    try { await api.put(`${V1}/folder`, f); toast(f.kind === 'none' ? 'Saved' : 'Saved — writing your documents there in the background'); reload(); setTest(null); bump() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const act = async (fn: () => Promise<unknown>, msg: (r: unknown) => string) => { setBusy(true); try { const r = await fn(); toast(msg(r)); reload(); bump() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) } }
  const status = data.kind === 'none' ? '' : st.error ? 'error' : st.inbox_error ? 'warn' : 'ok'
  return (
    <div className="stack loose">
      <Section title={<span className="row">{status && <span className={`set-status ${status}`} />}{data.kind === 'none' ? 'No folder chosen' : st.where || '—'}</span>}
        sub={data.kind === 'none' ? 'Your documents are only in the app\'s data directory on the server. Choose one below to have a readable copy of every document laid out by storage path, and an Inbox that feeds new files in.' : <>{data.file === 'original' ? 'Originals' : 'PDF/A with text'} laid out by storage path{data.inbox ? `, new files taken from ${st.inbox_path || data.inbox_name}` : ''}.</>}
        actions={data.kind !== 'none' && <><button type="button" className="btn sm" disabled={busy} onClick={() => act(() => api.post(`${V1}/folder/inbox`), r => `Inbox checked: ${(r as { taken: number }).taken} taken`)}><Icon name="inbox" size={14} />Check the Inbox now</button><button type="button" className="btn sm" disabled={busy} onClick={() => act(() => api.post(`${V1}/folder/sync`), () => 'Writing every document (see Processing)')}><Icon name="refresh" size={14} />Write everything again</button></>}>
        {data.kind !== 'none' && (st.error ? <div className="callout danger"><Icon name="alert" size={15} /><span>{st.error}</span></div> : st.inbox_error ? <div className="callout warning"><Icon name="alert" size={15} /><span>Inbox: {st.inbox_error}</span></div> : <p className="muted small" style={{ margin: 0 }}>Last written {st.last_write ? ago(st.last_write) : 'never'} · Inbox last looked at {st.last_inbox ? ago(st.last_inbox) : 'not yet'}{st.taken ? ` · ${st.taken} taken since the start` : ''}. The folder follows every rename, re-filing and deletion. The archive itself stays in the app; the folder is a copy you can browse, sync and search with any other tool.</p>)}
      </Section>
      <Section title="Change the folder">
        <div className="storage-kinds">
          {FOLDER_KINDS.filter(k => (k.kind !== 'folder' || (data.roots && data.roots.length)) && (k.kind !== 'files' || data.core)).map(k => (
            <button type="button" key={k.kind} className={`storage-kind ${f.kind === k.kind ? 'active' : ''}`} onClick={() => set({ kind: k.kind })}><b className="row" style={{ gap: 7 }}><Icon name={k.icon} size={15} />{k.title}</b><small className="muted">{k.sub}</small></button>
          ))}
        </div>
        <div className="stack mt" style={{ maxWidth: 640 }}>
          {f.kind === 'nextcloud' && <>
            {data.core && <Field group label="Account"><Segmented value={f.via_core ? 'core' : 'own'} onChange={v => set({ via_core: v === 'core' })} options={[{ value: 'core', label: 'Through Pocket Core' }, { value: 'own', label: 'Type it in' }]} /></Field>}
            {f.via_core && data.core ? (
              data.core_nextcloud ? <p className="muted">Pocket Core connects you as <b>{data.core_nextcloud.user}</b> at {data.core_nextcloud.url}. Nothing to type.</p> : <div className="callout warning"><Icon name="alert" size={15} /><span>Pocket Core has no Nextcloud account for you{data.core_nextcloud_error ? `: ${data.core_nextcloud_error}` : ''}. Add one in Core → Services, or type it in.</span></div>
            ) : <>
              <Field label="Nextcloud address"><input value={f.url} onChange={e => set({ url: e.target.value })} placeholder="https://cloud.example.com" spellCheck={false} /></Field>
              <div className="two-col tight">
                <Field label="Username"><input value={f.user} onChange={e => set({ user: e.target.value })} autoComplete="off" /></Field>
                <Field label="App password" help="Nextcloud → Settings → Security → Devices & sessions."><input type="password" value={f.password} onChange={e => set({ password: e.target.value })} placeholder={data.password_set && data.kind === 'nextcloud' ? 'saved — type to replace' : ''} autoComplete="new-password" /></Field>
              </div>
            </>}
            <Field label="Folder in your Nextcloud" help="Made when missing. The Nextcloud client syncs it to your computers; the phone app can upload scans straight into its Inbox."><input value={f.folder} onChange={e => set({ folder: e.target.value })} placeholder="Documents" /></Field>
          </>}
          {f.kind === 'files' && <>
            {data.core_files ? <p className="muted">Pocket Core lets this app into your Pocket Files{data.core_files_url ? <> at {data.core_files_url}</> : null}, as you and only you. Nothing to type.</p> : <div className="callout warning"><Icon name="alert" size={15} /><span>Pocket Files cannot be reached through Pocket Core{data.core_files_error ? `: ${data.core_files_error}` : ''}. Turn the Documents → Files link on in Core (Links, or Settings → Apps acting for me).</span></div>}
            <Field label="Folder in your Pocket Files" help="Made when missing. Drop scans into its Inbox from the Pocket Files page or any WebDAV client."><input value={f.folder} onChange={e => set({ folder: e.target.value })} placeholder="Documents" /></Field>
          </>}
          {f.kind === 'webdav' && <>
            <Field label="Address of the folder" help="The full WebDAV address of the folder that holds (or will hold) the documents."><input value={f.url} onChange={e => set({ url: e.target.value })} placeholder="https://dav.example.com/remote.php/webdav/Documents" spellCheck={false} /></Field>
            <div className="two-col tight">
              <Field label="Username"><input value={f.user} onChange={e => set({ user: e.target.value })} autoComplete="off" /></Field>
              <Field label="Password"><input type="password" value={f.password} onChange={e => set({ password: e.target.value })} placeholder={data.password_set && data.kind === 'webdav' ? 'saved — type to replace' : ''} autoComplete="new-password" /></Field>
            </div>
          </>}
          {f.kind === 'folder' && <Field label="Folder" help={<>Inside one of: {(data.roots || []).map(r => <code key={r}>{r}</code>)} (FOLDER_ROOTS). Mount a NAS share there and your archive is readable without this app.</>}><input value={f.path} onChange={e => set({ path: e.target.value })} placeholder={(data.roots || [])[0] || '/documents'} spellCheck={false} /></Field>}
          {f.kind !== 'none' && <>
            <Field label="Which file" group help="The PDF/A carries the OCR text and opens everywhere; originals are the untouched uploads (photos stay photos)."><Segmented value={f.file} onChange={v => set({ file: v })} options={[{ value: 'archive', label: 'PDF/A with text' }, { value: 'original', label: 'Original' }]} size="sm" /></Field>
            <Toggle checked={f.inbox} onChange={v => set({ inbox: v })} label="Take new files from an Inbox sub-folder" />
            {f.inbox && <Field label="Inbox folder name" help="Files dropped there become documents (sub-folders inside it become tags) and are removed from the Inbox once in; a file that is already in is removed too."><input value={f.inbox_name} onChange={e => set({ inbox_name: e.target.value })} placeholder="Inbox" style={{ maxWidth: 220 }} /></Field>}
          </>}
          {test && <div className="callout success"><Icon name="check-circle" size={15} /><span>It answers: <b>{test.where}</b> holds {test.files} file{test.files === 1 ? '' : 's'} in {test.folders} folder{test.folders === 1 ? '' : 's'}{test.inbox_files ? `, ${test.inbox_files} of them waiting in the Inbox` : ''}.</span></div>}
          <div className="row"><button type="button" className="btn" onClick={runTest} disabled={busy || f.kind === 'none'}>{busy ? 'Looking…' : 'Test'}</button><button type="button" className="btn primary" onClick={save} disabled={busy}>Use this</button></div>
          <p className="muted small" style={{ margin: 0 }}>Switching folders leaves the old one as it is. Files placed by the app in a previous folder are not removed.</p>
        </div>
      </Section>
    </div>
  )
}

// ---------- notifications & connected apps ----------

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { toast } = useToast()
  if (!f) return <Loading />
  return (
    <Section title="When something needs you" sub={<>A message goes to this URL when a document cannot be processed, and when a rule says “send a message”: an Apprise API endpoint (<code>https://apprise/notify/key</code>) or an ntfy topic (<code>https://ntfy.sh/topic</code>).</>} icon="bell">
      <div className="stack">
        <Field label="URL"><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="https://…" spellCheck={false} /></Field>
        <div className="row wrap"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>{busy ? 'Saving…' : 'Save'}</button><button type="button" className="btn" onClick={async () => { try { await api.post(`${V1}/settings/notify/test`, { url: f.notify_url }); toast('Sent') } catch (e) { toast((e as Error).message, 'err') } }} disabled={!f.notify_url}><Icon name="bell" size={15} />Send a test</button></div>
      </div>
    </Section>
  )
}

function Integrations() {
  const { f, setF, save, busy } = useSettingsForm()
  const [token, setToken] = useState('')
  const [money, setMoney] = useState<{ connected: boolean; via: string; error: string; url: string } | null>(null)
  const check = () => api.get<typeof money>(`${V1}/money/status`).then(setMoney)
  useEffect(() => { check() }, [])
  if (!f) return <Loading />
  return (
    <div className="set-apps">
      <section className={`set-app ${f.money_mode === 'off' ? 'off' : ''}`}>
        <div className="set-app-head">
          <span className="set-app-ico" style={{ background: '#16a34a' }}><Icon name="wallet" size={17} /></span>
          <div className="grow"><div className="set-app-name">Pocket Money</div><div className="muted small">Book an invoice or a receipt as an expense from its page. {f.core ? <>With Pocket Core on the box this works through an app link, for every person, with nothing to paste.</> : <>Without Pocket Core, paste a Money URL and one of your Money API tokens.</>}</div></div>
          {money && <span className="set-app-status small"><span className={`set-status ${money.connected ? 'ok' : f.money_mode === 'off' ? '' : 'warn'}`} />{money.connected ? `Connected via ${money.via}` : 'Not connected'}</span>}
        </div>
        <div className="set-app-controls">
          <Segmented value={f.money_mode} onChange={v => setF({ ...f, money_mode: v })} label="Connection" options={[{ value: 'core', label: 'Pocket Core' }, { value: 'direct', label: 'Direct' }, { value: 'off', label: 'Off' }]} size="sm" />
        </div>
        {f.money_mode === 'direct' && <div className="set-app-direct">
          <Field label="Money URL"><input value={f.money_url} onChange={e => setF({ ...f, money_url: e.target.value })} placeholder="https://money.example.com" spellCheck={false} /></Field>
          <Field label="Money API token" help={f.money_token_set ? 'A token is saved; enter a new one to replace it.' : undefined}><input type="password" value={token} onChange={e => setToken(e.target.value)} autoComplete="off" /></Field>
        </div>}
        {money && !money.connected && money.error && f.money_mode !== 'off' && <p className="muted small" style={{ margin: 0 }}>{money.error}</p>}
        <div className="row wrap"><button type="button" className="btn primary" onClick={async () => { await save(token ? { money_token: token } : {}); setToken(''); check() }} disabled={busy}>{busy ? 'Saving…' : 'Save'}</button><button type="button" className="btn" onClick={check}>Test</button></div>
      </section>
      <section className="set-app">
        <div className="set-app-head">
          <span className="set-app-ico" style={{ background: 'var(--accent)' }}><Icon name="layers" size={17} /></span>
          <div className="grow"><div className="set-app-name">Pocket Core</div><div className="muted small">{f.core
            ? <>Connected: this app publishes <code>documents.added</code>, <code>documents.processed</code>, <code>documents.updated</code>, <code>documents.deleted</code> and <code>documents.failed</code>, and offers <code>documents:read</code> / <code>documents:write</code> to linked apps. Links are switched on in Core.</>
            : <>Not set up on this box. With Pocket Core, other Pocket apps can read and file documents for you, and sign-in, Pocket Money and Pocket Files need nothing typed in.</>}</div></div>
          <span className="set-app-status small"><span className={`set-status ${f.core ? 'ok' : ''}`} />{f.core ? 'Connected' : 'Off'}</span>
        </div>
      </section>
    </div>
  )
}

// ---------- people ----------

function Users() {
  const { user, users, reloadTax, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); await reloadTax(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Section title={`${users.length} ${users.length === 1 ? 'person' : 'people'}`} sub="Everyone signs in with their own account. Documents, tags, correspondents, types, fields, rules and views are strictly per person; admins manage accounts and the OCR settings, nothing else."
      actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={15} />Add user</button>}>
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar user={u} size={32} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge cat">admin</span>}</div>
              <div className="muted small">{u.email}</div>
            </div>
            {isAdmin && <span className="row" style={{ gap: 4 }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit; passwords are not used here.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadTax() }} />}
      {del && <Confirm title={`Remove ${del.name || del.email}?`} message="Their account and every document, tag and setting of theirs are deleted." onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); run(() => api.del(`${V1}/users/${d.id}`), 'User removed') }} />}
    </Section>
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
      <form onSubmit={save} className="stack">
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add user'}</button></div>
      </form>
    </Modal>
  )
}

// ---------- API tokens ----------

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
    <div className="stack loose">
      <Section title="Your tokens" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you: upload (<code>POST /api/v1/documents</code>), search, download, feed a Homepage widget (<code>GET /api/v1/dashboard</code>). See <code>docs/api.md</code>.</>}>
        <form onSubmit={create} className="row"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. scanner script)" aria-label="Token name" /><button type="submit" className="btn primary">Create</button></form>
        {fresh && <div className="set-fresh"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><CopyField value={fresh} label="New token" /></div>}
        {data && data.length > 0 ? (
          <div className="set-people mt">
            {data.map(t => (
              <div key={t.id} className="set-person">
                <span className="set-token-ico"><Icon name="key" size={15} /></span>
                <div className="grow"><div className="set-person-name">{t.name}<code className="muted small">{t.prefix}…</code></div><div className="muted small">Last used {t.last_used_at ? stamp(t.last_used_at) : 'never'} · made {stamp(t.created_at)}</div></div>
                <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button>
              </div>
            ))}
          </div>
        ) : <p className="muted small mt" style={{ marginBottom: 0 }}>No tokens yet.</p>}
        {del && <Confirm title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} confirmLabel="Revoke" />}
      </Section>
      <Section title="Examples" sub={<>iOS Shortcut: Get File → Get Contents of URL (POST, form, field <code>document</code>, header <code>Authorization</code>). Or just share a photo to the installed web app.</>}>
        <pre>{`# upload a scan with two tags
curl -F document=@scan.pdf -F tags=1,2 -F title="Water bill" \\
  -H "Authorization: Bearer pd_…" ${location.origin}/api/v1/documents

# search
curl -H "Authorization: Bearer pd_…" \\
  "${location.origin}/api/v1/documents?q=invoice+created:2026&limit=10"

# the PDF/A with its text layer
curl -OJ -H "Authorization: Bearer pd_…" ${location.origin}/api/v1/documents/12/download`}</pre>
      </Section>
    </div>
  )
}

// ---------- import & export ----------

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
    <div className="stack loose">
      <Section title="Export" icon="download" sub={<>Everything of yours as one zip: a <code>manifest.json</code> with all metadata, notes, tags, correspondents, types, paths, fields, views and rules, plus the original files and the PDF/A twins. It imports here or on another box.</>}>
        <div className="row wrap"><a className="btn primary" href={`${V1}/export`}><Icon name="download" size={15} />Download everything</a><a className="btn" href={`${V1}/export?no_archive=1`}>Originals only (smaller)</a></div>
      </Section>
      <Section title="Import a zip" icon="upload" sub={<>A zip from the export above, or a <b>Paperless-ngx export folder</b> (<code>document_exporter</code>) zipped up. Tags and the rest are matched by name; files already here are skipped.</>}>
        <label className="btn">{busy ? <><span className="spinner" />Importing…</> : <><Icon name="upload" size={15} />Import zip…</>}<input type="file" accept=".zip,application/zip" hidden disabled={busy} onChange={e => e.target.files?.[0] && importZip(e.target.files[0])} /></label>
        <p className="muted small mt-s" style={{ marginBottom: 0 }}>Large exports: the upload runs in this request; keep the tab open. OCR of the imported files runs afterwards in the background.</p>
      </Section>
      <Section title="Import from a running Paperless-ngx" icon="refresh" sub="Pulls every document the token can see, with tags, correspondents, types, storage paths, custom fields and notes, over Paperless' API. Nothing is changed there. Make a token in Paperless under your profile (Django admin → Tokens).">
        <div className="two-col tight">
          <Field label="Paperless URL"><input value={pl.url} onChange={e => setPl({ ...pl, url: e.target.value })} placeholder="http://paperless:8000" spellCheck={false} /></Field>
          <Field label="API token"><input value={pl.token} onChange={e => setPl({ ...pl, token: e.target.value })} type="password" autoComplete="off" /></Field>
        </div>
        <div className="row wrap mt-s"><button type="button" className="btn" onClick={test} disabled={!pl.url || !pl.token}>Test</button><button type="button" className="btn primary" onClick={start} disabled={!pl.url || !pl.token}>Start import</button><span className="muted small">{plTest}</span></div>
        {job && <p className="small mt-s" style={{ marginBottom: 0 }}><span className={`badge st-${job.status}`}>{job.status}</span> {job.progress} {job.error && <span className="bad">{job.error}</span>} <Link to="/jobs">log</Link></p>}
      </Section>
      {isAdmin && <Section title="Database backup" icon="database" sub={<>A consistent copy of the SQLite database (<code>VACUUM INTO</code>) with everyone's metadata. The files live next to it under <code>originals/</code> and <code>archive/</code>; back up the whole data directory. Admins only.</>}
        actions={<a className="btn sm" href={`${V1}/backup`}><Icon name="download" size={14} />Download database</a>} />}
      <Section title="Maintenance" icon="wrench" sub="Neither is normally needed: the index follows every change and the classifier retrains itself.">
        <div className="row wrap">
          <button type="button" className="btn" onClick={async () => { await api.post(`${V1}/reindex`); toast('Rebuilding the search index (see Processing)') }}>Rebuild search index</button>
          <button type="button" className="btn" onClick={async () => { const r = await api.post<{ trained_on: number }>(`${V1}/retrain`); toast(`Classifier trained on ${r.trained_on} documents`) }}>Retrain classifier</button>
        </div>
      </Section>
    </div>
  )
}

// ---------- about ----------

function About() {
  const { data } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const counts = (data?.counts || {}) as Record<string, number>
  const core = data?.core as { configured: boolean; registered: boolean; error: string } | undefined
  return (
    <div className="stack loose">
      <Section title={<span className="set-about-hero"><img src="/favicon.svg" alt="" width={40} height={40} /><span style={{ display: 'grid', gap: 2 }}><span style={{ fontSize: 16 }}>Pocket Documents</span><span className="muted small" style={{ fontWeight: 500 }}>{data ? `Version ${String(data.version)}` : ' '}</span></span></span>}>
        <p>A self-hosted document archive: scans and files OCR'd into searchable PDF/A, tagged, filed with rules and a classifier that learns from you, found again with full-text search. A Paperless-ngx replacement in one Go binary with one SQLite file.</p>
        <p className="muted small" style={{ marginBottom: 0 }}>Open source (MIT) — <a href="https://github.com/abjelosevic88/pocket-homelab-documents" target="_blank" rel="noreferrer">github.com/abjelosevic88/pocket-homelab-documents</a>. OCR by <a href="https://ocrmypdf.readthedocs.io" target="_blank" rel="noreferrer">OCRmyPDF</a> and Tesseract, rendering by Poppler.</p>
      </Section>
      {data && <div className="set-stats">
        {([['file-text', counts.documents, 'documents'], ['tag', counts.tags, 'tags'], ['users', counts.correspondents, 'correspondents'], ['sparkles', Number(data.classifier ?? 0), 'trained on']] as [string, number, string][]).map(([i, n, l]) => (
          <div key={l} className="set-stat"><Icon name={i} size={17} /><span className="set-stat-n">{n ?? 0}</span><span className="set-stat-l">{l}</span></div>
        ))}
      </div>}
      {data && <Section title="The server">
        <dl className="set-dl">
          <div><dt>Version</dt><dd className="mono">{String(data.version)}</dd></div>
          <div><dt>Runtime</dt><dd className="mono">{String(data.go)} · {String(data.os)}/{String(data.arch)}</dd></div>
          <div><dt>Auth mode</dt><dd className="mono">{String(data.auth_mode)}</dd></div>
          {data.admin === true && <div><dt>Storage</dt><dd className="mono">db {bytes(Number(data.db_bytes))} · originals {bytes(Number(data.originals_bytes))} · archive {bytes(Number(data.archive_bytes))} · thumbs {bytes(Number(data.thumbs_bytes))} · page cache {bytes(Number(data.pages_bytes))}</dd></div>}
          {data.admin === true && <div><dt>Data dir</dt><dd className="mono">{String(data.data_dir)}</dd></div>}
          <div><dt>Timezone</dt><dd className="mono">{String(data.timezone)}</dd></div>
          <div><dt>Uptime</dt><dd className="mono">{String(data.uptime)}</dd></div>
          <div><dt>Yours</dt><dd className="mono">{counts.documents} documents · {counts.tags} tags · {counts.correspondents} correspondents · {counts.document_types} types · {counts.rules} rules</dd></div>
          <div><dt>Classifier</dt><dd className="mono">trained on {String(data.classifier ?? 0)} documents</dd></div>
          {core?.configured && <div><dt>Pocket Core</dt><dd className="mono">{core.registered ? 'registered' : `not registered: ${core.error}`}</dd></div>}
          {data.admin === true && <div><dt>Users</dt><dd className="mono">{counts.users}</dd></div>}
        </dl>
      </Section>}
    </div>
  )
}
