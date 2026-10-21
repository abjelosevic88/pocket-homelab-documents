import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ALGORITHMS, FIELD_TYPES, type CustomField, type Named, type Rule, type SavedView, type Tag } from '../lib/types'
import { docsURL, fmtDate, inkFor, plural } from '../lib/format'
import { Card, Confirm, Field, Modal, PageHeader, Segmented, Tabs, Toggle, useToast } from '../components/ui'
import { Picker } from '../components/Picker'
import { Select } from '../components/Select'
import { Icon } from '../components/icons'

type Tab = 'tags' | 'correspondents' | 'document_types' | 'storage_paths' | 'custom_fields' | 'rules' | 'saved_views'
const KIND_LABEL: Record<Tab, [string, string]> = { tags: ['Tag', 'Tags'], correspondents: ['Correspondent', 'Correspondents'], document_types: ['Document type', 'Document types'], storage_paths: ['Storage path', 'Storage paths'], custom_fields: ['Custom field', 'Custom fields'], rules: ['Rule', 'Rules'], saved_views: ['Saved view', 'Saved views'] }

export default function Manage() {
  const [tab, setTab] = useState<Tab>(() => (new URLSearchParams(window.location.search).get('tab') as Tab) || 'tags')
  const { tax } = useApp()
  return (
    <>
      <PageHeader title="Manage" sub="How documents are labelled and filed, and what happens to them when they come in." />
      <Tabs className="underline" value={tab} onChange={setTab} tabs={(Object.keys(KIND_LABEL) as Tab[]).map(k => ({ id: k, label: <>{KIND_LABEL[k][1]}{k !== 'rules' && <span className="muted"> {(tax[k] as unknown[]).length}</span>}</> }))} />
      <div className="mt">
        {(tab === 'tags' || tab === 'correspondents' || tab === 'document_types' || tab === 'storage_paths') && <Taxonomy kind={tab} />}
        {tab === 'custom_fields' && <Fields />}
        {tab === 'rules' && <Rules />}
        {tab === 'saved_views' && <Views />}
      </div>
    </>
  )
}

const COLORS = ['#a6cee3', '#1f78b4', '#b2df8a', '#33a02c', '#fb9a99', '#e31a1c', '#fdbf6f', '#ff7f00', '#cab2d6', '#6a3d9a', '#ffff99', '#b15928', '#0f766e', '#64748b', '#111827']

function Taxonomy({ kind }: { kind: 'tags' | 'correspondents' | 'document_types' | 'storage_paths' }) {
  const { tax, reloadTax } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<Tag & Named> | null>(null)
  const [del, setDel] = useState<(Tag | Named) | null>(null)
  const [q, setQ] = useState('')
  const items = (tax[kind] as (Tag | Named)[]).filter(i => !q || i.name.toLowerCase().includes(q.toLowerCase()))
  const [one, many] = KIND_LABEL[kind]
  const filterKey = { tags: 'tags', correspondents: 'correspondent', document_types: 'document_type', storage_paths: 'storage_path' }[kind]
  const remove = async () => {
    if (!del) return
    const d = del; setDel(null)
    try { await api.del(`${V1}/${kind}/${d.id}`); toast(`${one} removed`); reloadTax() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Card title={many} actions={<><input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter…" className="sm" style={{ width: 160 }} aria-label="Filter" /><button type="button" className="btn primary sm" onClick={() => setEdit({ matching_algorithm: 6, is_insensitive: true })}><Icon name="plus" size={15} />{one}</button></>}>
      {kind === 'storage_paths' && <p className="muted small">A storage path is a folder layout for the mirror folder and exports: <code>{'{created_year}/{correspondent}/{title}'}</code>. Placeholders: <code>{'{title} {created} {created_year} {created_month} {created_month_name} {created_day} {added_year} {correspondent} {document_type} {tag_list} {tag} {asn} {original_name} {id}'}</code>.</p>}
      {kind === 'tags' && <p className="muted small">An <b>inbox</b> tag is put on every new document; remove it once the document is filed. Matching rules assign tags automatically; “Auto” learns from the documents you tag yourself.</p>}
      {items.length === 0 ? <p className="muted">None yet.</p> : (
        <table className="table compact">
          <thead><tr><th>Name</th>{kind === 'storage_paths' && <th>Path</th>}<th>Matching</th><th className="num">Documents</th>{kind !== 'tags' && <th>Last</th>}<th /></tr></thead>
          <tbody>{items.map(i => <tr key={i.id}>
            <td>{'color' in i ? <span className="chip-tag sm" style={{ background: i.color, color: inkFor(i.color), borderColor: i.color }}>{i.is_inbox && <Icon name="inbox" size={11} strokeWidth={2.2} />}{i.name}</span> : i.name}</td>
            {kind === 'storage_paths' && <td className="mono small muted">{(i as Named).path}</td>}
            <td className="muted small">{ALGORITHMS.find(a => a.value === i.matching_algorithm)?.label}{i.match && i.matching_algorithm !== 6 && i.matching_algorithm !== 0 && <>: <code>{i.match}</code></>}</td>
            <td className="num"><Link to={docsURL({ [filterKey]: i.id })}>{i.document_count}</Link></td>
            {kind !== 'tags' && <td className="muted small">{(i as Named).last_created ? fmtDate((i as Named).last_created as string) : ''}</td>}
            <td className="right nowrap"><button type="button" className="btn ghost sm" onClick={() => setEdit(i)}>Edit</button><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(i)}>Delete</button></td>
          </tr>)}</tbody>
        </table>
      )}
      {edit && <TaxModal kind={kind} item={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadTax() }} />}
      {del && <Confirm title={`Delete the ${one.toLowerCase()} “${del.name}”?`} message={`It is removed from ${plural(del.document_count, 'document')}. The documents stay.`} onCancel={() => setDel(null)} onConfirm={remove} label="Delete" />}
    </Card>
  )
}

function TaxModal({ kind, item, onClose, onSaved }: { kind: 'tags' | 'correspondents' | 'document_types' | 'storage_paths'; item: Partial<Tag & Named>; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: item.name || '', color: item.color || COLORS[Math.floor(Math.random() * 10)], path: item.path || '{created_year}/{title}', match: item.match || '', matching_algorithm: item.matching_algorithm ?? 6, is_insensitive: item.is_insensitive ?? true, is_inbox: item.is_inbox || false })
  const [busy, setBusy] = useState(false)
  const [one] = KIND_LABEL[kind]
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (item.id) await api.put(`${V1}/${kind}/${item.id}`, f); else await api.post(`${V1}/${kind}`, f); toast('Saved'); onSaved() } catch (err) { toast((err as Error).message, 'err') } finally { setBusy(false) }
  }
  const alg = ALGORITHMS.find(a => a.value === f.matching_algorithm)
  return (
    <Modal title={item.id ? `Edit ${one.toLowerCase()}` : `New ${one.toLowerCase()}`} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required /></Field>
        {kind === 'tags' && <Field label="Colour" group><div className="row wrap">{COLORS.map(c => <button type="button" key={c} className={`color-dot ${f.color === c ? 'active' : ''}`} style={{ background: c }} onClick={() => setF({ ...f, color: c })} aria-label={c} />)}<input type="color" value={f.color} onChange={e => setF({ ...f, color: e.target.value })} style={{ width: 40, padding: 2 }} /></div></Field>}
        {kind === 'tags' && <Toggle checked={f.is_inbox} onChange={v => setF({ ...f, is_inbox: v })} label="Inbox tag: put on every new document" />}
        {kind === 'storage_paths' && <Field label="Path" help="Folders with /; the file name is the last part."><input value={f.path} onChange={e => setF({ ...f, path: e.target.value })} className="mono" required /></Field>}
        <Field label="Assign automatically" group help={alg?.help}>
          <Select value={String(f.matching_algorithm)} onChange={v => setF({ ...f, matching_algorithm: Number(v) })} options={ALGORITHMS.map(a => ({ value: String(a.value), label: a.label }))} />
        </Field>
        {f.matching_algorithm !== 6 && f.matching_algorithm !== 0 && <>
          <Field label={f.matching_algorithm === 4 ? 'Regular expression' : 'Words'}><input value={f.match} onChange={e => setF({ ...f, match: e.target.value })} className="mono" placeholder={f.matching_algorithm === 4 ? '(?i)invoice\\s+no' : 'stadtwerke rechnung'} /></Field>
          <Toggle checked={f.is_insensitive} onChange={v => setF({ ...f, is_insensitive: v })} label="Ignore upper/lower case" />
        </>}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary" disabled={busy}>Save</button></div>
      </form>
    </Modal>
  )
}

function Fields() {
  const { tax, reloadTax } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<CustomField> | null>(null)
  const [del, setDel] = useState<CustomField | null>(null)
  return (
    <Card title="Custom fields" actions={<button type="button" className="btn primary sm" onClick={() => setEdit({ data_type: 'string' })}><Icon name="plus" size={15} />Field</button>}>
      <p className="muted small">Typed values on a document: an amount, a due date, a contract number, a link to another document. Searchable with <code>field:name=value</code>; a money field can be booked straight into Pocket Money.</p>
      {tax.custom_fields.length === 0 ? <p className="muted">No fields yet.</p> : <table className="table compact"><thead><tr><th>Name</th><th>Type</th><th>Options</th><th className="num">Used</th><th /></tr></thead><tbody>{tax.custom_fields.map(f => <tr key={f.id}><td>{f.name}</td><td className="muted">{FIELD_TYPES.find(t => t.value === f.data_type)?.label}</td><td className="muted small">{f.extra?.options?.join(', ')}{f.extra?.currency}</td><td className="num">{f.document_count}</td><td className="right nowrap"><button type="button" className="btn ghost sm" onClick={() => setEdit(f)}>Edit</button><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(f)}>Delete</button></td></tr>)}</tbody></table>}
      {edit && <FieldModal item={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadTax() }} />}
      {del && <Confirm title={`Delete the field “${del.name}”?`} message={`Its values on ${plural(del.document_count, 'document')} are removed.`} onCancel={() => setDel(null)} onConfirm={async () => { const d = del; setDel(null); try { await api.del(`${V1}/custom_fields/${d.id}`); toast('Field removed'); reloadTax() } catch (e) { toast((e as Error).message, 'err') } }} label="Delete" />}
    </Card>
  )
}

function FieldModal({ item, onClose, onSaved }: { item: Partial<CustomField>; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: item.name || '', data_type: item.data_type || 'string', options: (item.extra?.options || []).join('\n'), currency: item.extra?.currency || '' })
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const extra: Record<string, unknown> = {}
    if (f.data_type === 'select') extra.options = f.options.split('\n').map(s => s.trim()).filter(Boolean)
    if (f.data_type === 'monetary' && f.currency) extra.currency = f.currency.toUpperCase()
    try { if (item.id) await api.put(`${V1}/custom_fields/${item.id}`, { name: f.name, data_type: f.data_type, extra }); else await api.post(`${V1}/custom_fields`, { name: f.name, data_type: f.data_type, extra }); toast('Saved'); onSaved() } catch (err) { toast((err as Error).message, 'err') }
  }
  return (
    <Modal title={item.id ? 'Edit field' : 'New field'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required /></Field>
        <Field group label="Type" help={item.id && item.document_count ? 'The type cannot change while documents use the field.' : undefined}><Select value={f.data_type} disabled={!!(item.id && item.document_count)} onChange={v => setF({ ...f, data_type: v as CustomField['data_type'] })} options={FIELD_TYPES.map(t => ({ value: t.value, label: t.label }))} /></Field>
        {f.data_type === 'select' && <Field label="Choices, one per line"><textarea value={f.options} onChange={e => setF({ ...f, options: e.target.value })} rows={4} /></Field>}
        {f.data_type === 'monetary' && <Field label="Currency"><input value={f.currency} onChange={e => setF({ ...f, currency: e.target.value })} placeholder="EUR" maxLength={3} /></Field>}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

function Rules() {
  const { reloadTax, tax } = useApp()
  const { toast } = useToast()
  const [rules, setRules] = useState<Rule[] | null>(null)
  const [edit, setEdit] = useState<Partial<Rule> | null>(null)
  const [del, setDel] = useState<Rule | null>(null)
  const load = () => api.get<Rule[]>(`${V1}/rules`).then(setRules)
  useEffect(() => { load() }, [])
  const name = (list: { id: number; name: string }[], id: number | null | undefined) => list.find(x => x.id === id)?.name
  const describe = (r: Rule) => {
    const f: string[] = []
    if (r.filters.filename) f.push(`file name like ${r.filters.filename}`)
    if (r.filters.path) f.push(`consume folder ${r.filters.path}`)
    if (r.filters.source) f.push(`from ${r.filters.source}`)
    if (r.filters.mime) f.push(`type ${r.filters.mime}`)
    if (r.filters.content) f.push(`text contains “${r.filters.content}”`)
    if (r.filters.correspondent) f.push(`correspondent ${name(tax.correspondents, r.filters.correspondent)}`)
    if (r.filters.document_type) f.push(`type ${name(tax.document_types, r.filters.document_type)}`)
    if (r.filters.tags?.length) f.push(`tagged ${r.filters.tags.map(t => name(tax.tags, t)).join(', ')}`)
    const a: string[] = []
    if (r.trigger === 'scheduled') f.push(`${Math.abs(r.filters.schedule_offset_days || 0)} days ${(r.filters.schedule_offset_days || 0) < 0 ? 'before' : 'after'} ${r.filters.schedule_field === 'added' ? 'added' : r.filters.schedule_field?.startsWith('cf:') ? `“${name(tax.custom_fields, Number(r.filters.schedule_field.slice(3)))}”` : 'the document date'}`)
    if (r.actions.notify) a.push('send a message')
    if (r.actions.title) a.push(`title “${r.actions.title}”`)
    if (r.actions.add_tags?.length) a.push(`+ ${r.actions.add_tags.map(t => name(tax.tags, t)).join(', ')}`)
    if (r.actions.remove_tags?.length) a.push(`− ${r.actions.remove_tags.map(t => name(tax.tags, t)).join(', ')}`)
    if (r.actions.correspondent) a.push(`correspondent ${name(tax.correspondents, r.actions.correspondent)}`)
    if (r.actions.document_type) a.push(`type ${name(tax.document_types, r.actions.document_type)}`)
    if (r.actions.storage_path) a.push(`path ${name(tax.storage_paths, r.actions.storage_path)}`)
    if (r.actions.favorite) a.push('favourite')
    return { when: f.length ? f.join(', ') : 'every document', then: a.length ? a.join(', ') : 'nothing' }
  }
  return (
    <Card title="Rules" actions={<button type="button" className="btn primary sm" onClick={() => setEdit({ enabled: true, trigger: 'added', filters: { filename: '', path: '', source: '', mime: '', content: '', correspondent: null, document_type: null, tags: [] }, actions: { title: '', add_tags: [], remove_tags: [], correspondent: null, document_type: null, storage_path: null, fields: {}, favorite: null, skip_matching: false } })}><Icon name="plus" size={15} />Rule</button>}>
      <p className="muted small">When a document comes in (after OCR and the matching above) or is changed, rules that fit are applied in order. “Run now” applies a rule to everything already here.</p>
      {!rules ? null : rules.length === 0 ? <p className="muted">No rules yet. Example: file name <code>*ikea*</code> → correspondent IKEA, tag Receipts, title <code>{'IKEA {created}'}</code>.</p> : <table className="table compact"><thead><tr><th /><th>Rule</th><th>When</th><th>Then</th><th /></tr></thead><tbody>{rules.map(r => { const d = describe(r); return <tr key={r.id} className={r.enabled ? '' : 'muted'}><td><Toggle checked={r.enabled} onChange={async v => { await api.put(`${V1}/rules/${r.id}`, { ...r, enabled: v }); load() }} /></td><td>{r.name}<div className="muted small">{r.trigger === 'scheduled' ? 'on a date' : `on ${r.trigger}`}</div></td><td className="small">{d.when}</td><td className="small">{d.then}</td><td className="right nowrap"><button type="button" className="btn ghost sm" onClick={async () => { await api.post(`${V1}/rules/${r.id}/run`); toast('Running on all documents (see Processing)') }}>Run now</button><button type="button" className="btn ghost sm" onClick={() => setEdit(r)}>Edit</button><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(r)}>Delete</button></td></tr> })}</tbody></table>}
      {edit && <RuleModal item={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); load(); reloadTax() }} />}
      {del && <Confirm title={`Delete the rule “${del.name}”?`} message="Documents it already changed stay as they are." onCancel={() => setDel(null)} onConfirm={async () => { const d = del; setDel(null); await api.del(`${V1}/rules/${d.id}`); load() }} label="Delete" />}
    </Card>
  )
}

function RuleModal({ item, onClose, onSaved }: { item: Partial<Rule>; onClose: () => void; onSaved: () => void }) {
  const { tax } = useApp()
  const { toast } = useToast()
  const [r, setR] = useState<Partial<Rule>>(item)
  const f = r.filters!, a = r.actions!
  const setF = (p: Partial<Rule['filters']>) => setR({ ...r, filters: { ...f, ...p } })
  const setA = (p: Partial<Rule['actions']>) => setR({ ...r, actions: { ...a, ...p } })
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try { if (item.id) await api.put(`${V1}/rules/${item.id}`, r); else await api.post(`${V1}/rules`, r); toast('Saved'); onSaved() } catch (err) { toast((err as Error).message, 'err') }
  }
  return (
    <Modal title={item.id ? 'Edit rule' : 'New rule'} onClose={onClose} size="lg">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <div className="two-col tight">
          <Field label="Name"><input value={r.name || ''} onChange={e => setR({ ...r, name: e.target.value })} autoFocus required /></Field>
          <Field label="Runs when" group><Segmented value={r.trigger || 'added'} onChange={v => setR({ ...r, trigger: v as Rule['trigger'] })} options={[{ value: 'added', label: 'Comes in' }, { value: 'updated', label: 'Is changed' }, { value: 'scheduled', label: 'On a date' }]} /></Field>
        </div>
        {r.trigger === 'scheduled' && <div className="two-col tight">
          <Field group label="Relative to" help="Fires once per document, also for documents already past the date (up to a year late)."><Select value={f.schedule_field || 'created'} onChange={v => setF({ schedule_field: v })} options={[{ value: 'created', label: 'the document date' }, { value: 'added', label: 'the day it was added' }, ...tax.custom_fields.filter(c => c.data_type === 'date').map(c => ({ value: `cf:${c.id}`, label: `the field “${c.name}”`, group: 'Date fields' }))]} /></Field>
          <Field label="Offset in days" help="Negative = before that date (−30: a month before a contract expires), positive = after."><input type="number" value={f.schedule_offset_days ?? 0} onChange={e => setF({ schedule_offset_days: Number(e.target.value) })} /></Field>
        </div>}
        <h3>When all of these fit (empty = any)</h3>
        <div className="two-col tight">
          <Field label="File name" help="* and ? wildcards, case does not matter"><input value={f.filename} onChange={e => setF({ filename: e.target.value })} placeholder="*invoice*.pdf" className="mono" /></Field>
          <Field label="Text contains"><input value={f.content} onChange={e => setF({ content: e.target.value })} placeholder="Kundennummer 1234" /></Field>
          <Field group label="Source"><Select value={f.source} onChange={v => setF({ source: v })} placeholder="any" clearLabel="any" options={[{ value: 'web', label: 'web upload' }, { value: 'api', label: 'API' }, { value: 'consume', label: 'consume folder' }, { value: 'folder', label: 'documents folder Inbox' }, { value: 'nextcloud', label: 'Nextcloud Inbox' }, { value: 'files', label: 'Pocket Files Inbox' }, { value: 'mail', label: 'mail' }, { value: 'import', label: 'import' }, { value: 'paperless', label: 'Paperless import' }]} /></Field>
          <Field group label="File type"><Select value={f.mime} onChange={v => setF({ mime: v })} placeholder="any" clearLabel="any" options={[{ value: 'application/pdf', label: 'PDF' }, { value: 'image/', label: 'image' }, { value: 'application/vnd', label: 'office' }, { value: 'text/', label: 'text' }, { value: 'message/', label: 'e-mail' }]} /></Field>
          <Field label="Has correspondent" group><Picker options={tax.correspondents} value={f.correspondent} onChange={v => setF({ correspondent: v as number | null })} /></Field>
          <Field label="Has document type" group><Picker options={tax.document_types} value={f.document_type} onChange={v => setF({ document_type: v as number | null })} /></Field>
        </div>
        <Field label="Has all of these tags" group><Picker multi options={tax.tags} value={f.tags || []} onChange={v => setF({ tags: v as number[] })} /></Field>
        <h3>Then</h3>
        <Field label="Set the title" help="Placeholders: {original_name} {correspondent} {document_type} {created} {created_year} {asn} {tag_list}"><input value={a.title} onChange={e => setA({ title: e.target.value })} className="mono" placeholder="leave empty to keep it" /></Field>
        <div className="two-col tight">
          <Field label="Add tags" group><Picker multi options={tax.tags} value={a.add_tags || []} onChange={v => setA({ add_tags: v as number[] })} /></Field>
          <Field label="Remove tags" group><Picker multi options={tax.tags} value={a.remove_tags || []} onChange={v => setA({ remove_tags: v as number[] })} /></Field>
          <Field label="Correspondent" group><Picker options={tax.correspondents} value={a.correspondent} onChange={v => setA({ correspondent: v as number | null })} /></Field>
          <Field label="Document type" group><Picker options={tax.document_types} value={a.document_type} onChange={v => setA({ document_type: v as number | null })} /></Field>
          <Field label="Storage path" group><Picker options={tax.storage_paths} value={a.storage_path} onChange={v => setA({ storage_path: v as number | null })} /></Field>
          <Field label="Favourite" group><Segmented value={a.favorite === null || a.favorite === undefined ? 'keep' : a.favorite ? 'yes' : 'no'} onChange={v => setA({ favorite: v === 'keep' ? null : v === 'yes' })} options={[{ value: 'keep', label: 'keep' }, { value: 'yes', label: 'yes' }, { value: 'no', label: 'no' }]} size="sm" /></Field>
        </div>
        <Toggle checked={!!a.notify} onChange={v => setA({ notify: v })} label="Send a message (Settings → Notifications)" />
        {a.notify && <Field label="Message" help="Placeholders: {title} {created} {correspondent} {document_type} {asn} {rule}. A link to the document is added."><input value={a.notify_message || ''} onChange={e => setA({ notify_message: e.target.value })} placeholder="{title} expires in 30 days" /></Field>}
        {tax.custom_fields.length > 0 && <Field label="Set custom fields" group><div className="grid" style={{ gap: 6 }}>{tax.custom_fields.map(cf => <div key={cf.id} className="row"><span className="label" style={{ width: 140 }}>{cf.name}</span><input value={a.fields?.[String(cf.id)] || ''} onChange={e => { const fields = { ...(a.fields || {}) }; if (e.target.value) fields[String(cf.id)] = e.target.value; else delete fields[String(cf.id)]; setA({ fields }) }} placeholder="leave empty to not set" /></div>)}</div></Field>}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

function Views() {
  const { tax, reloadTax } = useApp()
  const { toast } = useToast()
  const [del, setDel] = useState<SavedView | null>(null)
  const up = async (v: SavedView, patch: Partial<SavedView>) => { try { await api.put(`${V1}/saved_views/${v.id}`, patch); reloadTax() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Card title="Saved views">
      <p className="muted small">A saved view is a search with its filters and sort. Make one from the Documents page (Save view). Views can sit in the sidebar and on the dashboard.</p>
      {tax.saved_views.length === 0 ? <p className="muted">No views yet.</p> : <table className="table compact"><thead><tr><th>Name</th><th>Query</th><th>Sidebar</th><th>Dashboard</th><th /></tr></thead><tbody>{tax.saved_views.map(v => <tr key={v.id}><td><Link to={docsURL({ ...v.query, view: v.id })}>{v.name}</Link></td><td className="mono small muted">{Object.entries(v.query).map(([k, x]) => `${k}=${x}`).join(' ') || 'all'}</td><td><Toggle checked={v.in_sidebar} onChange={x => up(v, { in_sidebar: x })} /></td><td><Toggle checked={v.on_dashboard} onChange={x => up(v, { on_dashboard: x })} /></td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(v)}>Delete</button></td></tr>)}</tbody></table>}
      {del && <Confirm title={`Delete the view “${del.name}”?`} message="Only the saved search goes; no documents are touched." onCancel={() => setDel(null)} onConfirm={async () => { const d = del; setDel(null); await api.del(`${V1}/saved_views/${d.id}`); reloadTax() }} label="Delete" />}
    </Card>
  )
}
