import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Job } from '../lib/types'
import { ago, stamp } from '../lib/format'
import { Card, Empty, Loading, Modal, PageHeader, Segmented, useAsync, useToast } from '../components/ui'

const KIND: Record<string, string> = { process: 'OCR', reprocess: 'OCR again', import_paperless: 'Paperless import', reindex: 'Rebuild search index', mirror_all: 'Mirror folder', run_rule: 'Run rule' }

export default function Jobs() {
  const { stamp: st, queue } = useApp()
  const { toast } = useToast()
  const [filter, setFilter] = useState<'all' | 'active' | 'failed'>('all')
  const { data, reload } = useAsync(() => api.get<{ jobs: Job[]; queued: number; running: number; failed: number }>(`${V1}/jobs?status=${filter === 'all' ? '' : filter}&limit=200`), [filter])
  const [open, setOpen] = useState<Job | null>(null)
  useEffect(() => { reload() }, [st]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!open || (open.status !== 'running' && open.status !== 'queued')) return
    const id = setInterval(() => api.get<Job>(`${V1}/jobs/${open.id}`).then(setOpen).catch(() => {}), 2000)
    return () => clearInterval(id)
  }, [open?.id, open?.status]) // eslint-disable-line react-hooks/exhaustive-deps
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast(msg); reload() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <>
      <PageHeader title="Processing" sub={`${queue.queued} waiting · ${queue.running} running${data?.failed ? ` · ${data.failed} failed` : ''}`} actions={<>
        <Segmented size="sm" value={filter} onChange={setFilter} options={[{ value: 'all', label: 'All' }, { value: 'active', label: 'Active' }, { value: 'failed', label: 'Failed' }]} />
        <button className="btn sm" onClick={() => act(() => api.del(`${V1}/jobs`), 'Finished jobs cleared')}>Clear finished</button>
      </>} />
      {!data ? <Loading /> : data.jobs.length === 0 ? <Empty title="Nothing here">Jobs appear when documents are uploaded or OCR is run again.</Empty> : (
        <Card flush>
          <table className="table jobs-table">
            <thead><tr><th>Job</th><th>Document</th><th>Status</th><th>Progress</th><th>When</th><th /></tr></thead>
            <tbody>{data.jobs.map(j => <tr key={j.id} className={j.status}>
              <td>{KIND[j.kind] || j.kind}</td>
              <td className="ellipsis">{j.document_id ? <Link to={`/documents/${j.document_id}`}>{j.document_title || `#${j.document_id}`}</Link> : <span className="muted">—</span>}</td>
              <td><span className={`badge st-${j.status}`}>{j.status}</span>{j.attempts > 1 && <span className="muted small"> ×{j.attempts}</span>}</td>
              <td className="muted small ellipsis" style={{ maxWidth: 360 }}>{j.status === 'failed' ? <span className="bad">{j.error}</span> : j.progress}</td>
              <td className="muted small nowrap" title={stamp(j.finished_at || j.started_at || j.created_at)}>{ago(j.finished_at || j.started_at || j.created_at)}</td>
              <td className="right nowrap">
                <button className="btn ghost xs" onClick={() => api.get<Job>(`${V1}/jobs/${j.id}`).then(setOpen)}>Log</button>
                {(j.status === 'failed' || j.status === 'cancelled') && <button className="btn ghost xs" onClick={() => act(() => api.post(`${V1}/jobs/${j.id}/retry`), 'Queued again')}>Retry</button>}
                {j.status === 'queued' && <button className="btn ghost xs danger-text" onClick={() => act(() => api.post(`${V1}/jobs/${j.id}/cancel`), 'Cancelled')}>Cancel</button>}
              </td>
            </tr>)}</tbody>
          </table>
        </Card>
      )}
      {open && <Modal title={`${KIND[open.kind] || open.kind} · ${open.document_title || '#' + open.id}`} onClose={() => setOpen(null)} size="lg">
        <p className="small"><span className={`badge st-${open.status}`}>{open.status}</span> {open.progress && <span className="muted">{open.progress}</span>} {open.error && <span className="bad">{open.error}</span>}</p>
        <pre className="joblog">{open.log || 'No log lines yet.'}</pre>
      </Modal>}
    </>
  )
}
