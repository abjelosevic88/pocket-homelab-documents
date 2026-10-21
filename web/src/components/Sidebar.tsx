// Left column: upload, sections, saved views, tags, settings — the Pocket Calendar sidebar.
import { useEffect, useState } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { docsURL } from '../lib/format'
import { Icon } from './icons'
import { type Anchor, type MenuItem, Menu, rectOf } from './ui'

export type UploadKind = 'files' | 'camera' | 'page'

export function Sidebar({ onUpload, onNavigate }: { onUpload: (k: UploadKind) => void; onNavigate: () => void }) {
  const { tax, users, queue, status, settings } = useApp()
  const loc = useLocation()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Documents'
  const inbox = tax.tags.filter(t => t.is_inbox).reduce((n, t) => n + t.document_count, 0)
  const hasInbox = tax.tags.some(t => t.is_inbox)
  const views = tax.saved_views.filter(v => v.in_sidebar)
  const busy = queue.queued + queue.running
  const search = new URLSearchParams(loc.search)
  const onDocs = loc.pathname === '/documents'
  const is = (k: string) => onDocs && search.get(k) === '1'
  const uploadItems: MenuItem[] = [
    { label: 'Choose files…', icon: 'upload', onSelect: () => onUpload('files') },
    { label: 'Take a photo', icon: 'camera', onSelect: () => onUpload('camera') },
    'sep',
    { label: 'Upload with tags & details…', icon: 'sliders', hint: 'U', onSelect: () => onUpload('page') },
  ]
  const nav: { to: string; label: string; icon: string; active: boolean; badge?: number; count?: number; show?: boolean }[] = [
    { to: '/', label: 'Dashboard', icon: 'home', active: loc.pathname === '/' },
    { to: '/documents', label: 'Documents', icon: 'file-text', active: onDocs && !loc.search },
    { to: '/documents?inbox=1', label: 'Inbox', icon: 'inbox', active: is('inbox'), badge: inbox, show: hasInbox },
    { to: '/documents?favorite=1', label: 'Favourites', icon: 'star', active: is('favorite') },
    { to: '/documents?shared=1', label: 'Shared with me', icon: 'share', active: is('shared'), show: users.length > 1 },
  ]
  const more: { to: string; label: string; icon: string; badge?: number; busy?: boolean }[] = [
    { to: '/manage', label: 'Manage', icon: 'tag' },
    { to: '/jobs', label: 'Processing', icon: 'cpu', badge: busy, busy: true },
    { to: '/trash', label: 'Trash', icon: 'trash' },
  ]
  return (
    <aside className="sidebar" aria-label="Documents">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Documents' ? title : <>Pocket <b>Documents</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^\d/.test(status.version) ? 'v' + status.version : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="upload" size={17} strokeWidth={2.1} />Upload<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={uploadItems} onClose={() => setMenu(null)} width={248} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.filter(n => n.show !== false).map(n => (
          <Link key={n.to} to={n.to} onClick={onNavigate} className={`sb-nav-item ${n.active ? 'active' : ''}`} aria-current={n.active ? 'page' : undefined}>
            <Icon name={n.icon} size={16} />{n.label}{!!n.badge && <span className="badge accent" title={`${n.badge} in the inbox`}>{n.badge}</span>}
          </Link>
        ))}
      </nav>

      {views.length > 0 && <Group id="views" title="Saved views">
        <ul className="cal-list">{views.map(v => {
          const active = onDocs && search.get('view') === String(v.id)
          return (
            <li key={v.id} className="cal-row sb-row">
              <Link to={docsURL({ ...v.query, view: v.id })} onClick={onNavigate} className={`cal-check ${active ? 'is-active' : ''}`} title={v.name}>
                <Icon name="bookmark" size={15} /><span className="cal-name ellipsis">{v.name}</span>
              </Link>
            </li>
          )
        })}</ul>
      </Group>}

      <TagsGroup onNavigate={onNavigate} />

      <nav className="sb-nav" aria-label="Archive">
        {more.map(n => (
          <NavLink key={n.to} to={n.to} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}{!!n.badge && <span className={`badge ${n.busy ? 'busy' : ''}`} title={`${n.badge} waiting or running`}>{n.badge}</span>}
          </NavLink>
        ))}
      </nav>

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pd:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

function Group({ id, title, children, action }: { id: string; title: string; children: React.ReactNode; action?: React.ReactNode }) {
  const key = 'pd-sb:' + id
  const [open, setOpen] = useState(() => localStorage.getItem(key) !== '0')
  useEffect(() => { localStorage.setItem(key, open ? '1' : '0') }, [open, key])
  return (
    <section className="sb-sec">
      <h4 className="sb-h">
        <button type="button" className="sb-h-btn" aria-expanded={open} onClick={() => setOpen(o => !o)}><Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} />{title}</button>
        {action}
      </h4>
      {open && children}
    </section>
  )
}

function TagsGroup({ onNavigate }: { onNavigate: () => void }) {
  const { tax } = useApp()
  const loc = useLocation()
  const tags = [...tax.tags].sort((a, b) => b.document_count - a.document_count || a.name.localeCompare(b.name))
  const shown = tags.slice(0, 8)
  const current = loc.pathname === '/documents' ? new URLSearchParams(loc.search).get('tags') : null
  return (
    <Group id="tags" title="Tags" action={<Link to="/manage?tab=tags" onClick={onNavigate} className="btn ghost icon-only xs" aria-label="Manage tags" title="Manage tags"><Icon name="settings" size={13} /></Link>}>
      {tags.length === 0
        ? <p className="sb-empty">No tags yet. <Link to="/manage?tab=tags" onClick={onNavigate}>Add one</Link></p>
        : <ul className="cal-list">
          {shown.map(t => (
            <li key={t.id} className="cal-row sb-row">
              <Link to={docsURL({ tags: [t.id] })} onClick={onNavigate} className={`cal-check ${current === String(t.id) && loc.search === `?tags=${t.id}` ? 'is-active' : ''}`} title={t.name}>
                <span className="sb-tag-dot" style={{ background: t.color }} /><span className="cal-name ellipsis">{t.name}</span>
                {t.document_count > 0 && <span className="sb-meta">{t.document_count}</span>}
              </Link>
            </li>
          ))}
          {tags.length > shown.length && <li><Link to="/manage?tab=tags" onClick={onNavigate} className="sb-more">All {tags.length} tags</Link></li>}
        </ul>}
    </Group>
  )
}
