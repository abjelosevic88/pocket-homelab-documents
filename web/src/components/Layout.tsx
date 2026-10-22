// The app shell: sidebar, top bar, search palette, shortcuts, drop-to-upload — the Pocket Calendar layout.
import { useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ErrorBoundary } from './ErrorBoundary'
import { UploadOverlay } from './UploadOverlay'
import { Icon } from './icons'
import { Sidebar, type UploadKind } from './Sidebar'
import { SearchPalette, type PaletteCommand } from './SearchPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { Avatar, type MenuItem, MenuButton, modalOpen, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pd-sidebar'
const ACCEPT = '.pdf,.png,.jpg,.jpeg,.tif,.tiff,.webp,.gif,.bmp,.heic,.docx,.xlsx,.pptx,.odt,.ods,.odp,.rtf,.txt,.md,.csv,.html,.htm,.eml,.json'

/** The document page puts its title into the top bar's breadcrumb. */
export const setCrumb = (title: string | null) => window.dispatchEvent(new CustomEvent('pd:crumb', { detail: title }))

const section = (path: string) => path.split('/')[1] || 'dashboard'

export default function Layout() {
  const navigate = useNavigate()
  const loc = useLocation()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [crumb, setCrumbState] = useState<string | null>(null)
  useEffect(() => {
    const c = (e: Event) => setCrumbState((e as CustomEvent<string | null>).detail)
    const s = () => setShortcuts(true)
    window.addEventListener('pd:crumb', c)
    window.addEventListener('pd:shortcuts', s)
    return () => { window.removeEventListener('pd:crumb', c); window.removeEventListener('pd:shortcuts', s) }
  }, [])

  // uploads: the file picker, the camera, or files dropped anywhere
  const filesInput = useRef<HTMLInputElement>(null)
  const cameraInput = useRef<HTMLInputElement>(null)
  const [dropped, setDropped] = useState<File[] | null>(null)
  const [drop, setDrop] = useState(false)
  const dragDepth = useRef(0)
  const upload = (k: UploadKind) => {
    closeDrawer()
    if (k === 'page') navigate('/upload')
    else if (k === 'camera') cameraInput.current?.click()
    else filesInput.current?.click()
  }
  useEffect(() => {
    const enter = (e: DragEvent) => { if (e.dataTransfer?.types.includes('Files')) { dragDepth.current++; setDrop(true) } }
    const leave = () => { dragDepth.current = Math.max(0, dragDepth.current - 1); if (dragDepth.current === 0) setDrop(false) }
    const over = (e: DragEvent) => { if (e.dataTransfer?.types.includes('Files')) e.preventDefault() }
    const dropH = (e: DragEvent) => {
      if (!e.dataTransfer?.files.length) return
      e.preventDefault(); dragDepth.current = 0; setDrop(false)
      setDropped(Array.from(e.dataTransfer.files))
    }
    window.addEventListener('dragenter', enter); window.addEventListener('dragleave', leave); window.addEventListener('dragover', over); window.addEventListener('drop', dropH)
    return () => { window.removeEventListener('dragenter', enter); window.removeEventListener('dragleave', leave); window.removeEventListener('dragover', over); window.removeEventListener('drop', dropH) }
  }, [])

  // keyboard: ⌘K / "/" search, "?" shortcuts, "u" upload, "," settings, "[" sidebar, "g" then a letter
  const gAt = useRef(0)
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest?.('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop, .dropdown-pop')) return
      if (gAt.current && Date.now() - gAt.current < 900) {
        gAt.current = 0
        const map: Record<string, string> = { d: '/', l: '/documents', i: '/documents?inbox=1', j: '/jobs', s: '/settings', m: '/manage', t: '/trash' }
        if (map[e.key]) { e.preventDefault(); navigate(map[e.key]) }
        return
      }
      switch (e.key) {
        case 'g': gAt.current = Date.now(); break
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'u': e.preventDefault(); navigate('/upload'); break
        case ',': e.preventDefault(); navigate('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'upload', label: 'Upload files', icon: 'upload', hint: 'U', keywords: 'add scan new import', run: () => navigate('/upload') },
    { id: 'camera', label: 'Take a photo of a document', icon: 'camera', keywords: 'scan camera picture', run: () => cameraInput.current?.click() },
    { id: 'dash', label: 'Dashboard', icon: 'home', keywords: 'home', run: () => navigate('/') },
    { id: 'docs', label: 'Documents', icon: 'file-text', keywords: 'list all', run: () => navigate('/documents') },
    { id: 'inbox', label: 'Inbox', icon: 'inbox', run: () => navigate('/documents?inbox=1') },
    { id: 'fav', label: 'Favourites', icon: 'star', keywords: 'starred', run: () => navigate('/documents?favorite=1') },
    { id: 'manage', label: 'Manage tags, correspondents, types, fields, rules', icon: 'tag', keywords: 'manage taxonomy views', run: () => navigate('/manage') },
    { id: 'jobs', label: 'Processing', icon: 'cpu', keywords: 'jobs ocr queue log', run: () => navigate('/jobs') },
    { id: 'trash', label: 'Trash', icon: 'trash', keywords: 'deleted restore', run: () => navigate('/trash') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', keywords: 'preferences ocr folder mail', run: () => navigate('/settings') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  return (
    <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
      {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
      <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
        <Sidebar onUpload={upload} onNavigate={closeDrawer} />
      </div>
      <div className="main">
        <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
        <main className="content"><div className="content-inner"><ErrorBoundary key={section(loc.pathname)} where={loc.pathname}><Outlet /></ErrorBoundary></div></main>
      </div>
      {mobile && (loc.pathname === '/' || loc.pathname === '/documents') && <button type="button" className="fab" aria-label="Upload" title="Upload" onClick={() => upload('files')}><Icon name="upload" size={24} strokeWidth={2} /></button>}
      <input ref={filesInput} type="file" multiple hidden accept={ACCEPT} onChange={e => { if (e.target.files?.length) setDropped(Array.from(e.target.files)); e.target.value = '' }} />
      <input ref={cameraInput} type="file" accept="image/*" capture="environment" hidden onChange={e => { if (e.target.files?.length) setDropped(Array.from(e.target.files)); e.target.value = '' }} />
      {palette && <SearchPalette onClose={() => setPalette(false)} commands={commands} />}
      {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
      {drop && <div className="drop-veil"><div><Icon name="upload" size={22} />Drop to upload</div></div>}
      {dropped && <UploadOverlay files={dropped} onClose={() => setDropped(null)} />}
    </div>
  )
}

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string | null }) {
  const { user, themeMode, setThemeMode, status, setUser, tax } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const sp = new URLSearchParams(loc.search)
  const part = loc.pathname.split('/')[1] || ''
  const view = tax.saved_views.find(v => String(v.id) === sp.get('view'))
  const docsTitle = view ? view.name : sp.get('inbox') === '1' ? 'Inbox' : sp.get('favorite') === '1' ? 'Favourites' : sp.get('shared') === '1' ? 'Shared with me' : sp.get('q') ? 'Search' : 'Documents'
  const SECTION: Record<string, string> = { '': 'Dashboard', documents: docsTitle, upload: 'Upload', manage: 'Manage', jobs: 'Processing', trash: 'Trash' }
  const title = SECTION[part] ?? ''
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pd:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  const isDoc = part === 'documents' && !!loc.pathname.split('/')[2]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {isDoc
          ? <><button type="button" className="crumb" onClick={() => navigate('/documents')}>Documents</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb || ''}</span></>
          : title}
      </h1>
      <span className="spacer" />
      {mobile
        ? <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
        : <button type="button" className="top-search" onClick={onPalette} aria-label="Search documents" title="Search (⌘K or /)"><Icon name="search" size={16} /><span className="grow">Search documents…</span><kbd>⌘K</kbd></button>}
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
