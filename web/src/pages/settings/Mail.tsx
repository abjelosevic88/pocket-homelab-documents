import { useState } from 'react'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import type { MailAccount, MailAccountInput, MailFetchResult, MailRule, MailRuleInput, MailTestResult } from '../../lib/mail-types'
import { ago } from '../../lib/format'
import { Card, Confirm, Field, Loading, Modal, Segmented, Toggle, useAsync, useToast } from '../../components/ui'
import { Icon } from '../../components/icons'
import { Picker } from '../../components/Picker'
import { Select } from '../../components/Select'

const CONSUME: Record<MailRule['consume'], string> = { attachments: 'attachments', eml: 'the mail as .eml', both: 'attachments + .eml' }
const ACTION: Record<MailRule['action'], string> = { mark_read: 'mark read', move: 'move', delete: 'delete', flag: 'flag', none: 'leave' }

export default function MailSettings() {
  const { toast } = useToast()
  const { bump, tax } = useApp()
  const accounts = useAsync(() => api.get<MailAccount[]>(`${V1}/mail/accounts`), [])
  const rules = useAsync(() => api.get<MailRule[]>(`${V1}/mail/rules`), [])
  const [editAcct, setEditAcct] = useState<Partial<MailAccount> | null>(null)
  const [delAcct, setDelAcct] = useState<MailAccount | null>(null)
  const [editRule, setEditRule] = useState<Partial<MailRule> | null>(null)
  const [delRule, setDelRule] = useState<MailRule | null>(null)
  const [busy, setBusy] = useState<number | null>(null)
  const run = async (fn: () => Promise<unknown>, msg: string | ((r: unknown) => string)) => {
    try { const r = await fn(); toast(typeof msg === 'function' ? msg(r) : msg); accounts.reload(); rules.reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const test = async (a: MailAccount) => {
    setBusy(a.id)
    try { const r = await api.post<MailTestResult>(`${V1}/mail/accounts/${a.id}/test`); toast(`Signed in. ${r.folders.length} folder${r.folders.length === 1 ? '' : 's'}: ${r.folders.slice(0, 6).join(', ')}${r.folders.length > 6 ? '…' : ''}`) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(null) }
  }
  const fetchNow = async (a: MailAccount) => {
    setBusy(a.id)
    try {
      const r = await api.post<MailFetchResult>(`${V1}/mail/accounts/${a.id}/fetch`)
      toast(`${r.consumed} document${r.consumed === 1 ? '' : 's'} taken${r.errors.length ? `, ${r.errors.length} error${r.errors.length === 1 ? '' : 's'}: ${r.errors[0]}` : ''}`, r.errors.length ? 'err' : 'ok')
      accounts.reload(); bump()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(null) }
  }
  const acctName = (id: number) => accounts.data?.find(a => a.id === id)?.name || `#${id}`
  const names = (ids: number[], list: { id: number; name: string }[]) => ids.map(id => list.find(x => x.id === id)?.name).filter(Boolean).join(', ')
  const assignments = (r: MailRule) => {
    const parts: string[] = []
    if (r.assign_tags.length) parts.push(`tags ${names(r.assign_tags, tax.tags)}`)
    if (r.assign_correspondent) parts.push(`from ${names([r.assign_correspondent], tax.correspondents)}`)
    else if (r.correspondent_from !== 'nothing') parts.push(`correspondent from sender ${r.correspondent_from}`)
    if (r.assign_document_type) parts.push(`type ${names([r.assign_document_type], tax.document_types)}`)
    if (r.assign_storage_path) parts.push(`path ${names([r.assign_storage_path], tax.storage_paths)}`)
    return parts.join(' · ')
  }
  const filters = (r: MailRule) => [r.filter_from && `from “${r.filter_from}”`, r.filter_to && `to “${r.filter_to}”`, r.filter_subject && `subject “${r.filter_subject}”`, r.filter_body && `body “${r.filter_body}”`, r.filter_attachment && `files ${r.filter_attachment}`, r.max_age_days > 0 && `≤ ${r.max_age_days} days`].filter(Boolean).join(', ')
  if (!accounts.data || !rules.data) return <Loading />
  return (
    <div className="grid" style={{ gap: 16 }}>
      <Card title="Mail accounts" actions={<button type="button" className="btn primary sm" onClick={() => setEditAcct({ security: 'ssl', port: 993, enabled: true, poll_minutes: 10 })}><Icon name="plus" size={15} />Add account</button>}>
        <p className="muted">Mailboxes checked over IMAP. Rules below say which messages become documents: attachments (PDFs, scans, office files) or the whole mail as <code>.eml</code>. A message is taken once per rule.</p>
        {accounts.data.length === 0 ? <p className="muted">No mail accounts yet.</p> : (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Server</th><th>Checked</th><th /><th /></tr></thead>
            <tbody>
              {accounts.data.map(a => (
                <tr key={a.id}>
                  <td><b>{a.name}</b><div className="muted small">{a.username}</div></td>
                  <td className="mono small">{a.host}:{a.port} <span className="muted">{a.security}</span><div className="muted small">every {a.poll_minutes} min</div></td>
                  <td className="small">{a.last_checked_at ? ago(a.last_checked_at) : <span className="muted">not yet</span>}{a.last_error && <div className="bad small" title={a.last_error}>{a.last_error.length > 80 ? a.last_error.slice(0, 80) + '…' : a.last_error}</div>}</td>
                  <td><Toggle checked={a.enabled} onChange={v => run(() => api.put(`${V1}/mail/accounts/${a.id}`, { enabled: v }), v ? 'Account enabled' : 'Account paused')} /></td>
                  <td className="right"><span className="row" style={{ justifyContent: 'flex-end' }}>
                    <button type="button" className="btn ghost sm" disabled={busy === a.id} onClick={() => test(a)}>{busy === a.id ? '…' : 'Test'}</button>
                    <button type="button" className="btn ghost sm" disabled={busy === a.id || !a.enabled} onClick={() => fetchNow(a)}>Fetch now</button>
                    <button type="button" className="btn ghost sm" onClick={() => setEditAcct(a)}>Edit</button>
                    <button type="button" className="btn ghost sm danger-text" onClick={() => setDelAcct(a)}>Delete</button>
                  </span></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      <Card title="Mail rules" actions={<button type="button" className="btn primary sm" disabled={accounts.data.length === 0} onClick={() => setEditRule({ account_id: accounts.data?.[0]?.id, folder: 'INBOX', enabled: true, only_unseen: true, consume: 'attachments', title_from: 'subject', correspondent_from: 'nothing', action: 'mark_read', assign_tags: [], max_age_days: 0 })}><Icon name="plus" size={15} />Add rule</button>}>
        {rules.data.length === 0 ? <p className="muted">{accounts.data.length === 0 ? 'Add an account first.' : 'No rules yet: nothing is taken from the mailbox until a rule says so.'}</p> : (
          <table className="table compact">
            <thead><tr><th>Rule</th><th>Where</th><th>Takes</th><th>Assigns</th><th /><th /></tr></thead>
            <tbody>
              {rules.data.map(r => (
                <tr key={r.id}>
                  <td><b>{r.name}</b>{filters(r) && <div className="muted small">{filters(r)}</div>}</td>
                  <td className="small">{acctName(r.account_id)} <span className="muted">/ {r.folder}</span>{r.only_unseen && <div className="muted small">unread only</div>}</td>
                  <td className="small">{CONSUME[r.consume]}<div className="muted small">then {ACTION[r.action]}{r.action === 'move' && ` → ${r.action_folder}`}</div></td>
                  <td className="small">{assignments(r) || <span className="muted">—</span>}</td>
                  <td><Toggle checked={r.enabled} onChange={v => run(() => api.put(`${V1}/mail/rules/${r.id}`, { enabled: v }), v ? 'Rule enabled' : 'Rule paused')} /></td>
                  <td className="right"><span className="row" style={{ justifyContent: 'flex-end' }}>
                    <button type="button" className="btn ghost sm" onClick={() => setEditRule(r)}>Edit</button>
                    <button type="button" className="btn ghost sm danger-text" onClick={() => setDelRule(r)}>Delete</button>
                  </span></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      {editAcct && <AccountModal account={editAcct} onClose={() => setEditAcct(null)} onSaved={() => { setEditAcct(null); accounts.reload() }} />}
      {editRule && <RuleModal rule={editRule} accounts={accounts.data} onClose={() => setEditRule(null)} onSaved={() => { setEditRule(null); rules.reload() }} />}
      {delAcct && <Confirm title={`Delete “${delAcct.name}”?`} message="The account, its rules and the record of which messages were taken are removed. Documents already taken stay." onCancel={() => setDelAcct(null)} onConfirm={() => { const a = delAcct; setDelAcct(null); run(() => api.del(`${V1}/mail/accounts/${a.id}`), 'Account deleted') }} />}
      {delRule && <Confirm title={`Delete rule “${delRule.name}”?`} message="Messages it already took stay as documents; nothing else changes." onCancel={() => setDelRule(null)} onConfirm={() => { const r = delRule; setDelRule(null); run(() => api.del(`${V1}/mail/rules/${r.id}`), 'Rule deleted') }} />}
    </div>
  )
}

function AccountModal({ account, onClose, onSaved }: { account: Partial<MailAccount>; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState<MailAccountInput>({ name: account.name || '', host: account.host || '', port: account.port || 993, security: account.security || 'ssl', username: account.username || '', password: '', enabled: account.enabled ?? true, poll_minutes: account.poll_minutes || 10 })
  const [busy, setBusy] = useState(false)
  const [folders, setFolders] = useState<string[] | null>(null)
  const set = (p: Partial<MailAccountInput>) => { setF({ ...f, ...p }); setFolders(null) }
  const setSecurity = (v: MailAccountInput['security']) => set({ security: v, port: f.port === 993 || f.port === 143 ? (v === 'ssl' ? 993 : 143) : f.port })
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try {
      if (account.id) await api.put(`${V1}/mail/accounts/${account.id}`, f); else await api.post(`${V1}/mail/accounts`, f)
      toast(account.id ? 'Account saved' : 'Account added'); onSaved()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const test = async () => {
    setBusy(true)
    try { const r = await api.post<MailTestResult>(`${V1}/mail/accounts/${account.id}/test`, f); setFolders(r.folders) } catch (e) { toast((e as Error).message, 'err'); setFolders(null) } finally { setBusy(false) }
  }
  return (
    <Modal title={account.id ? 'Edit mail account' : 'Add mail account'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name" help="How the account is called here, e.g. “Work” or “Gmail”."><input value={f.name} onChange={e => set({ name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <div className="two-col tight">
          <Field label="IMAP server"><input value={f.host} onChange={e => set({ host: e.target.value })} placeholder="imap.example.com" required spellCheck={false} /></Field>
          <Field label="Port"><input type="number" min={1} max={65535} value={f.port} onChange={e => set({ port: Number(e.target.value) })} /></Field>
        </div>
        <Field label="Security" group><Segmented value={f.security} onChange={setSecurity} options={[{ value: 'ssl', label: 'SSL/TLS' }, { value: 'starttls', label: 'STARTTLS' }, { value: 'none', label: 'None' }]} size="sm" /></Field>
        <div className="two-col tight">
          <Field label="Username"><input value={f.username} onChange={e => set({ username: e.target.value })} autoComplete="off" spellCheck={false} /></Field>
          <Field label="Password" help="Gmail and others want an app password."><input type="password" value={f.password} onChange={e => set({ password: e.target.value })} placeholder={account.password_set ? 'saved — type to replace' : ''} autoComplete="new-password" required={!account.password_set} /></Field>
        </div>
        <div className="two-col tight">
          <Field label="Check every (minutes)"><input type="number" min={1} max={1440} value={f.poll_minutes} onChange={e => set({ poll_minutes: Number(e.target.value) })} /></Field>
          <Field label="State" group><Toggle checked={f.enabled} onChange={v => set({ enabled: v })} label="Enabled" /></Field>
        </div>
        {folders && <div className="callout success">Signed in. Folders: {folders.join(', ')}</div>}
        <div className="form-actions">
          {account.id && <button type="button" className="btn" onClick={test} disabled={busy}>Test</button>}
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn primary" disabled={busy}>{account.id ? 'Save' : 'Add account'}</button>
        </div>
        {!account.id && <p className="muted small">Save first, then Test signs in and lists the folders.</p>}
      </form>
    </Modal>
  )
}

function RuleModal({ rule, accounts, onClose, onSaved }: { rule: Partial<MailRule>; accounts: MailAccount[]; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const { tax } = useApp()
  const [f, setF] = useState<MailRuleInput>({
    account_id: rule.account_id || accounts[0]?.id || 0, name: rule.name || '', enabled: rule.enabled ?? true, position: rule.position || 0, folder: rule.folder || 'INBOX',
    filter_from: rule.filter_from || '', filter_to: rule.filter_to || '', filter_subject: rule.filter_subject || '', filter_body: rule.filter_body || '', filter_attachment: rule.filter_attachment || '', max_age_days: rule.max_age_days || 0,
    consume: rule.consume || 'attachments', title_from: rule.title_from || 'subject', assign_tags: rule.assign_tags || [], assign_correspondent: rule.assign_correspondent ?? null, assign_document_type: rule.assign_document_type ?? null, assign_storage_path: rule.assign_storage_path ?? null,
    correspondent_from: rule.correspondent_from || 'nothing', action: rule.action || 'mark_read', action_folder: rule.action_folder || '', only_unseen: rule.only_unseen ?? true,
  })
  const [busy, setBusy] = useState(false)
  const [folders, setFolders] = useState<string[] | null>(null)
  const set = (p: Partial<MailRuleInput>) => setF({ ...f, ...p })
  const loadFolders = async () => {
    if (!f.account_id) return
    setBusy(true)
    try { setFolders((await api.post<MailTestResult>(`${V1}/mail/accounts/${f.account_id}/test`)).folders) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try {
      if (rule.id) await api.put(`${V1}/mail/rules/${rule.id}`, f); else await api.post(`${V1}/mail/rules`, f)
      toast(rule.id ? 'Rule saved' : 'Rule added'); onSaved()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const folderInput = (value: string, onChange: (v: string) => void, placeholder: string) => folders
    ? <Select value={folders.includes(value) ? value : ''} onChange={x => onChange(x)} placeholder={value && !folders.includes(value) ? value : placeholder} options={folders.map(x => ({ value: x, label: x }))} />
    : <input value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder} spellCheck={false} />
  return (
    <Modal title={rule.id ? 'Edit mail rule' : 'Add mail rule'} onClose={onClose} size="lg">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <div className="two-col tight">
          <Field label="Name"><input value={f.name} onChange={e => set({ name: e.target.value })} autoFocus required maxLength={80} /></Field>
          <Field group label="Account"><Select value={String(f.account_id)} onChange={v => { set({ account_id: Number(v) }); setFolders(null) }} options={accounts.map(a => ({ value: String(a.id), label: a.name, hint: a.username }))} /></Field>
        </div>
        <div className="two-col tight">
          <Field label="Folder" help={<>Where to look. <button type="button" className="btn ghost sm" onClick={loadFolders} disabled={busy || !f.account_id}>{folders ? 'Reload folders' : 'List folders'}</button></>}>{folderInput(f.folder, v => set({ folder: v }), 'INBOX')}</Field>
          <Field label="Only unread messages" group><Toggle checked={f.only_unseen} onChange={v => set({ only_unseen: v })} label="Skip messages already read" /></Field>
        </div>
        <hr />
        <p className="muted small">Filters: every one must match; a message passes an empty filter. Text filters are “contains”, case does not matter.</p>
        <div className="two-col tight">
          <Field label="From contains"><input value={f.filter_from} onChange={e => set({ filter_from: e.target.value })} placeholder="racuni@epbih.ba" /></Field>
          <Field label="To contains"><input value={f.filter_to} onChange={e => set({ filter_to: e.target.value })} placeholder="me+bills@" /></Field>
        </div>
        <div className="two-col tight">
          <Field label="Subject contains"><input value={f.filter_subject} onChange={e => set({ filter_subject: e.target.value })} placeholder="invoice" /></Field>
          <Field label="Body contains"><input value={f.filter_body} onChange={e => set({ filter_body: e.target.value })} /></Field>
        </div>
        <div className="two-col tight">
          <Field label="Attachment name" help="A pattern like *.pdf; several with commas. Empty = any."><input value={f.filter_attachment} onChange={e => set({ filter_attachment: e.target.value })} placeholder="*.pdf" className="mono" /></Field>
          <Field label="Not older than (days)" help="0 = any age."><input type="number" min={0} value={f.max_age_days} onChange={e => set({ max_age_days: Number(e.target.value) })} /></Field>
        </div>
        <hr />
        <div className="two-col tight">
          <Field label="Take" group help={{ attachments: 'PDFs, images, office files and text attachments; tiny images (signatures) are skipped.', eml: 'The whole message as one .eml document; its text is searchable.', both: 'Attachments and the message itself.' }[f.consume]}><Segmented value={f.consume} onChange={v => set({ consume: v })} options={[{ value: 'attachments', label: 'Attachments' }, { value: 'eml', label: 'Mail as .eml' }, { value: 'both', label: 'Both' }]} size="sm" /></Field>
          <Field label="Title from" group><Segmented value={f.title_from} onChange={v => set({ title_from: v })} options={[{ value: 'subject', label: 'Subject' }, { value: 'filename', label: 'File name' }, { value: 'none', label: 'Default' }]} size="sm" /></Field>
        </div>
        <Field label="Tags" group><Picker multi options={tax.tags} value={f.assign_tags} onChange={v => set({ assign_tags: (v as number[]) || [] })} placeholder="Add tags…" /></Field>
        <div className="two-col tight">
          <Field label="Correspondent" group><Picker options={tax.correspondents} value={f.assign_correspondent} onChange={v => set({ assign_correspondent: v as number | null })} placeholder="Choose…" /></Field>
          <Field label="…or from the sender" group help="When no correspondent is chosen above."><Segmented value={f.correspondent_from} onChange={v => set({ correspondent_from: v })} options={[{ value: 'nothing', label: 'Nothing' }, { value: 'name', label: 'Sender name' }, { value: 'email', label: 'Sender address' }]} size="sm" /></Field>
        </div>
        <div className="two-col tight">
          <Field label="Document type" group><Picker options={tax.document_types} value={f.assign_document_type} onChange={v => set({ assign_document_type: v as number | null })} placeholder="Choose…" /></Field>
          <Field label="Storage path" group><Picker options={tax.storage_paths} value={f.assign_storage_path} onChange={v => set({ assign_storage_path: v as number | null })} placeholder="Choose…" /></Field>
        </div>
        <hr />
        <div className="two-col tight">
          <Field label="After taking" group><Segmented value={f.action} onChange={v => set({ action: v })} options={[{ value: 'mark_read', label: 'Mark read' }, { value: 'flag', label: 'Flag' }, { value: 'move', label: 'Move' }, { value: 'delete', label: 'Delete' }, { value: 'none', label: 'Nothing' }]} size="sm" /></Field>
          {f.action === 'move' && <Field label="Move to folder">{folderInput(f.action_folder, v => set({ action_folder: v }), 'Archive')}</Field>}
        </div>
        <Toggle checked={f.enabled} onChange={v => set({ enabled: v })} label="Rule enabled" />
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary" disabled={busy}>{rule.id ? 'Save' : 'Add rule'}</button></div>
      </form>
    </Modal>
  )
}
