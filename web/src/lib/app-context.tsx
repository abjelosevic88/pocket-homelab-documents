import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { api, V1 } from './api'
import { setDateFormat } from './format'
import type { Settings, Taxonomy, User } from './types'

interface AuthStatus { needs_setup: boolean; auth_mode: string; authenticated: boolean; user: User | null; version: string; title: string; oidc?: boolean; oidc_name?: string }
interface AppState {
  status: AuthStatus | null
  user: User | null
  settings: Settings | null
  users: User[]
  tax: Taxonomy
  theme: 'light' | 'dark'
  toggleTheme: () => void
  refresh: () => Promise<void>
  reloadTax: () => Promise<void>
  setUser: (u: User | null) => void
  setSettings: (s: Settings) => void
  stamp: string
  queue: { queued: number; running: number }
  bump: () => void
}
const empty: Taxonomy = { tags: [], correspondents: [], document_types: [], storage_paths: [], custom_fields: [], saved_views: [] }
const Ctx = createContext<AppState>(null!)
export function useApp() { return useContext(Ctx) }

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [user, setUser] = useState<User | null>(null)
  const [settings, setSettingsRaw] = useState<Settings | null>(null)
  const [users, setUsers] = useState<User[]>([])
  const [tax, setTax] = useState<Taxonomy>(empty)
  const [stamp, setStamp] = useState('')
  const [queue, setQueue] = useState({ queued: 0, running: 0 })
  const [theme, setTheme] = useState<'light' | 'dark'>(() => (localStorage.getItem('pd-theme') as 'light' | 'dark') || (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'))
  const lastStamp = useRef('')
  useEffect(() => { document.documentElement.dataset.theme = theme; localStorage.setItem('pd-theme', theme) }, [theme])
  useEffect(() => { if (settings?.title) document.title = settings.title }, [settings?.title])
  const setSettings = useCallback((s: Settings) => { setSettingsRaw(s); setDateFormat(s.date_format) }, [])
  const reloadTax = useCallback(async () => {
    const [t, u] = await Promise.all([api.get<Taxonomy>(`${V1}/taxonomy`), api.get<User[]>(`${V1}/users`).catch(() => [] as User[])])
    setTax(t); setUsers(u)
  }, [])
  const refresh = useCallback(async () => {
    const st = await api.get<AuthStatus>(`${V1}/auth/status`)
    setStatus(st)
    setUser(st.user)
    if (st.authenticated) {
      setSettings(await api.get<Settings>(`${V1}/settings`))
      await reloadTax()
    }
  }, [reloadTax, setSettings])
  useEffect(() => { refresh().catch(() => setStatus({ needs_setup: false, auth_mode: 'local', authenticated: false, user: null, version: '', title: 'Pocket Documents' })) }, [refresh])
  useEffect(() => {
    const h = () => { setUser(null); setStatus(s => s ? { ...s, authenticated: false, user: null } : s) }
    window.addEventListener('pd:unauthorized', h)
    return () => window.removeEventListener('pd:unauthorized', h)
  }, [])
  // Pages reload their lists when the stamp moves (a worker finished, something changed elsewhere).
  useEffect(() => {
    if (!user) return
    let stop = false
    const tick = async () => {
      if (document.hidden) return
      try {
        const c = await api.get<{ stamp: string; queued: number; running: number }>(`${V1}/changes`)
        if (stop) return
        setQueue({ queued: c.queued, running: c.running })
        if (c.stamp !== lastStamp.current) { lastStamp.current = c.stamp; setStamp(c.stamp) }
      } catch {}
    }
    tick()
    const id = setInterval(tick, 5000)
    const vis = () => { if (!document.hidden) tick() }
    document.addEventListener('visibilitychange', vis)
    return () => { stop = true; clearInterval(id); document.removeEventListener('visibilitychange', vis) }
  }, [user])
  const bump = useCallback(() => setStamp(s => s + '.'), [])
  return <Ctx.Provider value={{ status, user, settings, users, tax, theme, toggleTheme: () => setTheme(t => t === 'dark' ? 'light' : 'dark'), refresh, reloadTax, setUser, setSettings, stamp, queue, bump }}>{children}</Ctx.Provider>
}
