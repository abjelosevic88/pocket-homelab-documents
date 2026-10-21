/** Unix seconds or ISO → "just now", "5 min ago", "3 h ago", "Yesterday", "Mon", "14 Mar", "14 Mar 2025". */
export function ago(t: string | number): string {
  if (!t) return ''
  const ms = typeof t === 'number' ? t * 1000 : new Date(t).getTime()
  if (isNaN(ms)) return ''
  const s = Math.max(0, (Date.now() - ms) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  const d = new Date(ms), now = new Date()
  const days = Math.round((new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() - new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()) / 86400000)
  if (days === 0) return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  if (days === 1) return 'Yesterday'
  if (days < 7) return d.toLocaleDateString(undefined, { weekday: 'long' })
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric' })
}

export function stamp(t: string | number): string {
  if (!t) return ''
  const d = typeof t === 'number' ? new Date(t * 1000) : new Date(t)
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

let dateFormat: 'iso' | 'dmy-dot' | 'long' = 'iso'
export function setDateFormat(f: 'iso' | 'dmy-dot' | 'long') { dateFormat = f }

/** A YYYY-MM-DD date in the person's chosen format. */
export function fmtDate(s: string): string {
  if (!s) return ''
  const [y, m, d] = s.slice(0, 10).split('-')
  if (!y || !m || !d) return s
  if (dateFormat === 'dmy-dot') return `${d}.${m}.${y}`
  if (dateFormat === 'long') return new Date(Number(y), Number(m) - 1, Number(d)).toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' })
  return `${y}-${m}-${d}`
}

export function bytes(n: number): string {
  if (!n) return '0 B'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1048576).toFixed(1)} MB`
  return `${(n / 1073741824).toFixed(2)} GB`
}

export function plural(n: number, one: string, many = one + 's'): string { return `${n} ${n === 1 ? one : many}` }

export function initials(name: string): string {
  const parts = name.trim().split(/[\s@._-]+/).filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

const PALETTE = ['#2563eb', '#7c3aed', '#0891b2', '#059669', '#d97706', '#dc2626', '#db2777', '#4b5563', '#b45309', '#0d9488']
export function colorFor(id: number): string { return PALETTE[Math.abs(id) % PALETTE.length] }

/** Black or white text for a background colour. */
export function inkFor(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex || '')
  if (!m) return '#fff'
  const n = parseInt(m[1], 16)
  const r = (n >> 16) & 255, g = (n >> 8) & 255, b = n & 255
  return (r * 299 + g * 587 + b * 114) / 1000 > 150 ? '#111' : '#fff'
}

export function today(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/** Snippets mark the words found with [[[ ]]]; split them for highlighting. */
export function snippetParts(s: string): { text: string; hit: boolean }[] {
  const out: { text: string; hit: boolean }[] = []
  const re = /\[\[\[(.*?)\]\]\]/g
  let last = 0, m: RegExpExecArray | null
  while ((m = re.exec(s))) {
    if (m.index > last) out.push({ text: s.slice(last, m.index), hit: false })
    out.push({ text: m[1], hit: true })
    last = m.index + m[0].length
  }
  if (last < s.length) out.push({ text: s.slice(last), hit: false })
  return out
}

export function fileIcon(mime: string, name = ''): string {
  if (mime === 'application/pdf' || /\.pdf$/i.test(name)) return '📄'
  if (mime.startsWith('image/')) return '🖼'
  if (/word|odt|rtf/.test(mime) || /\.(docx?|odt|rtf)$/i.test(name)) return '📝'
  if (/sheet|excel|ods|csv/.test(mime) || /\.(xlsx?|ods|csv)$/i.test(name)) return '📊'
  if (/presentation|powerpoint|odp/.test(mime) || /\.(pptx?|odp)$/i.test(name)) return '📽'
  if (mime === 'message/rfc822') return '✉️'
  return '📃'
}

export function money(v: string, cur?: string): string {
  const n = Number(String(v).replace(',', '.'))
  if (isNaN(n)) return v
  try { return n.toLocaleString(undefined, { style: 'currency', currency: cur || 'EUR' }) } catch { return `${n.toFixed(2)} ${cur || ''}`.trim() }
}

/** Build a documents URL from list parameters. */
export function docsURL(q: Record<string, unknown>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === '' || v === false) continue
    if (Array.isArray(v)) { if (v.length) p.set(k, v.join(',')) }
    else p.set(k, String(v))
  }
  const s = p.toString()
  return '/documents' + (s ? '?' + s : '')
}

/** The stroke icon for a file type (the UI chrome uses icons, not emoji). */
export function fileIconName(mime: string, name = ''): string {
  if (mime === 'application/pdf' || /\.pdf$/i.test(name)) return 'file-text'
  if (mime.startsWith('image/')) return 'image'
  if (/sheet|excel|ods|csv/.test(mime) || /\.(xlsx?|ods|csv)$/i.test(name)) return 'sheet'
  if (/presentation|powerpoint|odp/.test(mime) || /\.(pptx?|odp)$/i.test(name)) return 'slides'
  if (mime === 'message/rfc822') return 'mail'
  return 'file-text'
}
