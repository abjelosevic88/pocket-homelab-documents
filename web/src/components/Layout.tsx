import { useEffect, useRef, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { docsURL } from '../lib/format'
import { Avatar } from './people'
import { ErrorBoundary } from './ErrorBoundary'
import { UploadOverlay } from './UploadOverlay'
import { QuickSearch, type QuickSearchHandle } from './QuickSearch'

const section = (path: string) => path.split('/')[1] || 'dashboard'

export default function Layout() {
  const { user, settings, status, theme, toggleTheme, setUser, tax, queue, users } = useApp()
  const [open, setOpen] = useState(false)
  const [drop, setDrop] = useState(false)
  const dragDepth = useRef(0)
  const [dropped, setDropped] = useState<File[] | null>(null)
  const navigate = useNavigate()
  const loc = useLocation()
  const title = settings?.title || 'Pocket Documents'
  const search = useRef<QuickSearchHandle>(null)
  useEffect(() => { setOpen(false) }, [loc.pathname, loc.search])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey
      const typing = ['INPUT', 'TEXTAREA', 'SELECT'].includes((e.target as HTMLElement)?.tagName) || (e.target as HTMLElement)?.isContentEditable
      if (mod && e.key.toLowerCase() === 'k') { e.preventDefault(); search.current?.focus() }
      else if (!typing && !mod && e.key === '/') { e.preventDefault(); search.current?.focus() }
      else if (!typing && !mod && e.key === 'u') { e.preventDefault(); navigate('/upload') }
      else if (!typing && !mod && e.key === 'g') { (window as unknown as { _pdG?: number })._pdG = Date.now() }
      else if (!typing && !mod && (window as unknown as { _pdG?: number })._pdG && Date.now() - ((window as unknown as { _pdG?: number })._pdG || 0) < 800) {
        const map: Record<string, string> = { d: '/', l: '/documents', i: '/documents?inbox=1', j: '/jobs', s: '/settings', m: '/manage', t: '/trash' }
        if (map[e.key]) { e.preventDefault(); navigate(map[e.key]) }
        ;(window as unknown as { _pdG?: number })._pdG = 0
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])
  // Drop files anywhere to upload them.
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
  const logout = async () => { await api.post(`${V1}/auth/logout`); setUser(null); navigate('/login') }
  const inbox = tax.tags.filter(t => t.is_inbox)
  const views = tax.saved_views.filter(v => v.in_sidebar)
  const busy = queue.queued + queue.running
  return (
    <div className="app">
      <aside className={`sidebar ${open ? 'open' : ''}`}>
        <div className="brand"><span className="logo"><img src="/favicon.svg" alt="" /></span><span className="brand-name">{title}</span></div>
        <div className="side-actions">
          <NavLink to="/upload" className="btn primary grow">⇡ Upload</NavLink>
        </div>
        <nav className="side-nav">
          <NavLink to="/" end className="nav-item"><span className="ico">◫</span>Dashboard</NavLink>
          <NavLink to="/documents" className={() => `nav-item ${loc.pathname === '/documents' && !loc.search ? 'active' : ''}`} end><span className="ico">▤</span>Documents</NavLink>
          {inbox.length > 0 && <NavLink to="/documents?inbox=1" className={() => `nav-item ${loc.search.includes('inbox=1') ? 'active' : ''}`}><span className="ico">⌾</span>Inbox{inbox.reduce((n, t) => n + t.document_count, 0) > 0 && <span className="count">{inbox.reduce((n, t) => n + t.document_count, 0)}</span>}</NavLink>}
          <NavLink to="/documents?favorite=1" className={() => `nav-item ${loc.search.includes('favorite=1') ? 'active' : ''}`}><span className="ico">★</span>Favourites</NavLink>
          {users.length > 1 && <NavLink to="/documents?shared=1" className={() => `nav-item ${loc.search.includes('shared=1') ? 'active' : ''}`}><span className="ico">⇄</span>Shared with me</NavLink>}
        </nav>
        {views.length > 0 && <div className="side-scroll">
          <div className="side-head"><span>Views</span></div>
          {views.map(v => <NavLink key={v.id} to={docsURL({ ...v.query, view: v.id })} className={() => `nav-item sm ${loc.search.includes(`view=${v.id}`) ? 'active' : ''}`}><span className="ico">⌕</span><span className="ellipsis">{v.name}</span></NavLink>)}
        </div>}
        <nav className="side-nav bottom">
          <NavLink to="/manage" className="nav-item"><span className="ico">⌸</span>Manage</NavLink>
          <NavLink to="/jobs" className="nav-item"><span className="ico">⟳</span>Processing{busy > 0 && <span className="count busy">{busy}</span>}</NavLink>
          <NavLink to="/trash" className="nav-item"><span className="ico">🗑</span>Trash</NavLink>
          <NavLink to="/settings" className="nav-item"><span className="ico">⚙</span>Settings</NavLink>
        </nav>
        <div className="foot">
          <span className="row" title={user?.email}>{user && <Avatar user={user} />}<span className="foot-id"><span>{user?.name || user?.email}</span>{status?.version && <a className="ver" href="https://github.com/abjelosevic88/pocket-homelab-documents/releases" target="_blank" rel="noreferrer">v{status.version.replace(/^v/, '')}</a>}</span></span>
          <span className="row">
            <button className="btn ghost sm" onClick={toggleTheme} title="Toggle theme">{theme === 'dark' ? '☀' : '☾'}</button>
            {status?.auth_mode === 'local' && <button className="btn ghost sm" onClick={logout} title="Sign out">⎋</button>}
          </span>
        </div>
      </aside>
      {open && <div className="scrim" onClick={() => setOpen(false)} />}
      <div className="main">
        <header className="topbar">
          <button className="btn ghost icon menu-btn" onClick={() => setOpen(o => !o)} aria-label="Menu">☰</button>
          <QuickSearch ref={search} />
          <NavLink to="/upload" className="btn ghost icon hide-lg" aria-label="Upload">⇡</NavLink>
        </header>
        <main className="content"><ErrorBoundary key={section(loc.pathname)} where={loc.pathname}><Outlet /></ErrorBoundary></main>
        <nav className="tabbar">
          <NavLink to="/" end><span className="ico">◫</span><span>Home</span></NavLink>
          <NavLink to="/documents"><span className="ico">▤</span><span>Documents</span></NavLink>
          <NavLink to="/upload"><span className="ico">⇡</span><span>Upload</span></NavLink>
          <NavLink to="/manage"><span className="ico">⌸</span><span>Manage</span></NavLink>
          <NavLink to="/settings"><span className="ico">⚙</span><span>Settings</span></NavLink>
        </nav>
      </div>
      {drop && <div className="drop-veil"><div>Drop to upload</div></div>}
      {dropped && <UploadOverlay files={dropped} onClose={() => setDropped(null)} />}
    </div>
  )
}
