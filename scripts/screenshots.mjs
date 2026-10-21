// Takes the README screenshots over the Chrome DevTools protocol against a demo instance on :18190
// (scripts/demo-docs builds the demo archive; see docs/screenshots/README.md).
// Run: docker run -d --name pdchrome --network host --shm-size=1g zenika/alpine-chrome:124 --no-sandbox --headless --disable-gpu --disable-dev-shm-usage --hide-scrollbars --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1 --remote-allow-origins=* --window-size=1280,860 about:blank
//      EMAIL=… PASSWORD=… node scripts/screenshots.mjs   (SHOTS=home,document limits it); output in docs/screenshots
import fs from 'node:fs'
const BASE = process.env.BASE || 'http://127.0.0.1:18190'
const OUT = process.env.OUT || new URL('../docs/screenshots/', import.meta.url).pathname
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a)
fs.mkdirSync(OUT, { recursive: true })
const login = await fetch(BASE + '/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: process.env.EMAIL, password: process.env.PASSWORD }) })
log('login', login.status)
const cookie = login.headers.get('set-cookie').match(/pd_session=([^;]+)/)[1]
const api = async p => (await fetch(BASE + p, { headers: { Cookie: 'pd_session=' + cookie } })).json()
const docs = (await api('/api/v1/documents?limit=100&no_facets=1')).results
const byTitle = t => docs.find(d => d.title.includes(t))
const lease = byTitle('zakupu'), porez = docs.find(d => d.title.includes('porez') || d.title.includes('пореза')), polisa = byTitle('Polisa'), nalaz = docs.find(d => d.title.includes('налаз'))
const ver = await (await fetch('http://127.0.0.1:9222/json/version')).json()
const ws = new WebSocket(ver.webSocketDebuggerUrl)
await new Promise(r => ws.onopen = r)
let id = 0; const waits = new Map()
ws.onmessage = m => { const d = JSON.parse(m.data); if (d.id && waits.has(d.id)) { waits.get(d.id)(d); waits.delete(d.id) } }
const send = (method, params = {}, sessionId) => new Promise((res, rej) => { const i = ++id; const t = setTimeout(() => rej(new Error('timeout ' + method)), 30000); waits.set(i, d => { clearTimeout(t); res(d) }); ws.send(JSON.stringify({ id: i, method, params, sessionId })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId } } = await send('Target.attachToTarget', { targetId, flatten: true })
const s = (m, p) => send(m, p, sessionId)
await s('Page.enable'); await s('Runtime.enable'); await s('Network.enable')
await s('Network.setCookie', { name: 'pd_session', value: cookie, url: BASE, path: '/' })
const sleep = ms => new Promise(r => setTimeout(r, ms))
const evalJS = async expr => (await s('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true })).result?.result?.value
const click = sel => evalJS(`(() => { const el = document.querySelector(${JSON.stringify(sel)}); if (!el) return 'missing ' + ${JSON.stringify(sel)}; el.click(); return 'ok' })()`)
const clickText = (sel, text) => evalJS(`(() => { const el = [...document.querySelectorAll(${JSON.stringify(sel)})].find(b => b.textContent.includes(${JSON.stringify(text)})); if (!el) return 'missing ' + ${JSON.stringify(text)}; el.click(); return 'ok' })()`)
const type = async (sel, text) => { await click(sel); await evalJS(`document.querySelector(${JSON.stringify(sel)}).focus()`); await s('Input.insertText', { text }) }
const ONLY = (process.env.SHOTS || '').split(',').filter(Boolean)
let themeScript = null
async function shot(name, path, { w = 1280, h = 860, dark = false, mobile = false, before = null, wait = 2200, prefs = {} } = {}) {
  if (ONLY.length && !ONLY.includes(name)) return
  log('start', name)
  await s('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: mobile ? 2 : 1, mobile })
  if (themeScript) await s('Page.removeScriptToEvaluateOnNewDocument', { identifier: themeScript })
  const ls = { 'pd-theme-mode': dark ? 'dark' : 'light', 'pd-view': 'table', 'pd-filters': '1', 'pd-preview': 'pages', 'pd-cols': JSON.stringify(['asn', 'correspondent', 'title', 'type', 'tags', 'created', 'cf:1']), ...prefs }
  themeScript = (await s('Page.addScriptToEvaluateOnNewDocument', { source: Object.entries(ls).map(([k, v]) => `localStorage.setItem(${JSON.stringify(k)}, ${JSON.stringify(v)});`).join('') })).result.identifier
  await s('Page.navigate', { url: BASE + path }); await sleep(wait)
  if (before) { log(' ', await before()); await sleep(1300) }
  const cap = await s('Page.captureScreenshot', { format: 'png' })
  if (!cap.result) { log('capture failed', JSON.stringify(cap).slice(0, 300)); return }
  fs.writeFileSync(`${OUT}/${name}.png`, Buffer.from(cap.result.data, 'base64')); log('shot', name)
}
await shot('home', '/')
await shot('documents', '/documents?q=' + encodeURIComponent('račun'))
await shot('cards', '/documents', { prefs: { 'pd-view': 'cards' } })
await shot('document', `/documents/${polisa.id}`, { wait: 3500 })
await shot('suggestions', `/documents/${porez.id}`, { wait: 3500 })
await shot('serbian', '/documents?q=' + encodeURIComponent('Бања Лука'), { prefs: { 'pd-filters': '0' } })
await shot('pages', `/documents/${lease.id}`, { wait: 3500, before: async () => { await clickText('.pn', '1'); return 'selected' } })
await shot('upload', '/upload')
await shot('rules', '/manage?tab=rules')
await shot('ocr', '/settings/ocr')
await shot('folder', '/settings/folder', { before: () => clickText('.storage-kind', 'Nextcloud') })
await shot('mail', '/settings/mail')
await shot('jobs', '/jobs')
await shot('dark', `/documents/${nalaz.id}`, { dark: true, wait: 3500 })
await shot('mobile-list', '/documents', { w: 390, h: 844, mobile: true, prefs: { 'pd-view': 'list', 'pd-filters': '0' } })
await shot('mobile-document', `/documents/${polisa.id}`, { w: 390, h: 844, mobile: true, wait: 3500 })
ws.close(); process.exit(0)
