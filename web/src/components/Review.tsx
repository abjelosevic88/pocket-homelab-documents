import React, { createContext, useContext, useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import type { Document } from '../lib/types'
import { Field, Modal, Segmented, Toggle, useToast } from './ui'
import { Icon } from './icons'

export interface ReviewHit { id: number; title: string; status: string; url: string; revisions: number; open_threads: number; open_issues: number; updated_at: string; my_role: string }
export const reviewStatusLabel: Record<string, string> = { draft: 'Review draft', in_review: 'In review', changes_requested: 'Changes requested', approved: 'Approved', rejected: 'Rejected', cancelled: 'Review cancelled' }
const finished = (s: string) => ['approved', 'rejected', 'cancelled'].includes(s)

const Ctx = createContext<Record<string, ReviewHit>>({})
export function useReview(id: number): ReviewHit | undefined { return useContext(Ctx)[String(id)] }

/** Loads review badges for a list of documents in one call. */
export function ReviewsProvider({ ids, children }: { ids: number[]; children: React.ReactNode }) {
  const [map, setMap] = useState<Record<string, ReviewHit>>({})
  const key = ids.join(',')
  useEffect(() => {
    if (!key) { setMap({}); return }
    api.get<{ available: boolean; reviews: Record<string, ReviewHit> }>(`${V1}/collab/lookup?ids=${key}`).then(r => setMap(r.reviews || {})).catch(() => setMap({}))
  }, [key])
  return <Ctx.Provider value={map}>{children}</Ctx.Provider>
}

export function ReviewBadge({ review, small }: { review?: ReviewHit | null; small?: boolean }) {
  if (!review) return null
  return <a className={`review-badge ${review.status} ${small ? 'sm' : ''}`} href={review.url} target="_blank" rel="noreferrer" onClick={e => e.stopPropagation()} title={`${review.title} — v${review.revisions}, ${reviewStatusLabel[review.status] || review.status}. Open in Pocket Collaborator`}>
    <span className="dot" />{reviewStatusLabel[review.status] || review.status}{!small && review.open_threads > 0 ? ` · ${review.open_threads} open` : ''}
  </a>
}

/** The header control on a document: its review badge and "Send for review". */
export function ReviewAction({ doc }: { doc: Document }) {
  const [st, setSt] = useState<{ available: boolean; error?: string; review?: ReviewHit } | null>(null)
  const [open, setOpen] = useState(false)
  const load = () => api.get<{ available: boolean; error?: string; reviews: Record<string, ReviewHit> }>(`${V1}/collab/lookup?ids=${doc.id}`).then(r => setSt({ available: r.available, error: r.error, review: r.reviews?.[String(doc.id)] })).catch(() => setSt({ available: false }))
  useEffect(() => { load() }, [doc.id]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!st?.available && !st?.review) return null
  const active = st.review && !finished(st.review.status) ? st.review : undefined
  return <>
    {st.review && <ReviewBadge review={st.review} />}
    {st.available && <button type="button" className="btn sm" onClick={() => setOpen(true)} title="Send this document's PDF to Pocket Collaborator">{active ? 'New revision…' : 'Send for review…'}</button>}
    {open && <SendReview doc={doc} existing={active} onClose={() => { setOpen(false); load() }} />}
  </>
}

function SendReview({ doc, existing, onClose }: { doc: Document; existing?: ReviewHit; onClose: () => void }) {
  const { toast } = useToast()
  const [mode, setMode] = useState<'revision' | 'new'>(existing ? 'revision' : 'new')
  const [f, setF] = useState({ title: doc.title, description: '', note: '', due_date: '', start: true })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [done, setDone] = useState<{ url: string; created: boolean; revision: number } | null>(null)
  const send = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setErr('')
    try { const r = await api.post<{ url: string; created: boolean; revision: number }>(`${V1}/documents/${doc.id}/collab`, { ...f, mode: existing ? mode : 'new' }); setDone(r); toast(r.created ? 'Review started in Pocket Collaborator' : `Sent as revision v${r.revision}`) } catch (e) { setErr((e as Error).message) } finally { setBusy(false) }
  }
  return (
    <Modal title={`Send “${doc.title}” for review`} onClose={onClose}>
      {done ? <div className="grid" style={{ gap: 12 }}>
        <div className="callout success">{done.created ? 'A review was created.' : `Revision v${done.revision} was added.`}</div>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Close</button><a className="btn primary" href={done.url} target="_blank" rel="noreferrer">Open in Collaborator<Icon name="external" size={14} /></a></div>
      </div> : <form onSubmit={send} className="grid" style={{ gap: 14 }}>
        {existing && <Field label="This document is already in review" group>
          <div className="row wrap" style={{ gap: 8 }}><ReviewBadge review={existing} /><span className="muted small">{existing.title} · v{existing.revisions}</span></div>
          <Segmented value={mode} onChange={setMode} options={[{ value: 'revision', label: `Add as revision v${existing.revisions + 1}` }, { value: 'new', label: 'Start a separate review' }]} />
        </Field>}
        {(!existing || mode === 'new') && <>
          <Field label="Review title"><input value={f.title} onChange={e => setF(x => ({ ...x, title: e.target.value }))} required /></Field>
          <Field label="What should people look at?"><textarea value={f.description} onChange={e => setF(x => ({ ...x, description: e.target.value }))} rows={3} placeholder="Optional" /></Field>
          <div className="two-col tight">
            <Field label="Due date"><input type="date" value={f.due_date} onChange={e => setF(x => ({ ...x, due_date: e.target.value }))} /></Field>
            <span style={{ alignSelf: 'end', paddingBottom: 8 }}><Toggle checked={f.start} onChange={v => setF(x => ({ ...x, start: v }))} label="Start the review right away" /></span>
          </div>
        </>}
        {existing && mode === 'revision' && <Field label="What changed?"><input value={f.note} onChange={e => setF(x => ({ ...x, note: e.target.value }))} placeholder="Optional note for the reviewers" /></Field>}
        <p className="muted small">The {doc.has_archive ? 'PDF/A twin' : 'PDF'} is sent; the review links back to this document. People and roles are added in Collaborator.</p>
        {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary" disabled={busy}>{busy ? 'Sending…' : existing && mode === 'revision' ? 'Send revision' : 'Send for review'}</button></div>
      </form>}
    </Modal>
  )
}
