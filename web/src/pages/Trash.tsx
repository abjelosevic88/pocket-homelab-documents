import { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Document } from '../lib/types'
import { ago, plural } from '../lib/format'
import { Card, Confirm, Empty, Loading, PageHeader, useAsync, useToast } from '../components/ui'
import { Thumb } from '../components/docs'

export default function Trash() {
  const { stamp, bump } = useApp()
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<{ results: Document[]; count: number; trash_days: number }>(`${V1}/trash`), [])
  const [empty, setEmpty] = useState(false)
  const [purge, setPurge] = useState<Document | null>(null)
  useEffect(() => { reload() }, [stamp]) // eslint-disable-line react-hooks/exhaustive-deps
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast(msg); reload(); bump() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <>
      <PageHeader title="Trash" sub={data ? (data.trash_days ? `Deleted documents are removed for good after ${data.trash_days} days.` : 'Deleted documents stay here until you empty the trash.') : ''} actions={data && data.count > 0 && <button className="btn danger sm" onClick={() => setEmpty(true)}>Empty trash</button>} />
      {!data ? <Loading /> : data.results.length === 0 ? <Empty title="The trash is empty" /> : (
        <Card flush>
          <table className="table">
            <thead><tr><th /><th>Title</th><th>Deleted</th><th /></tr></thead>
            <tbody>{data.results.map(d => <tr key={d.id}>
              <td className="thumb-cell"><Thumb d={d} size="sm" /></td>
              <td>{d.title}<div className="muted small">{d.original_name} · {d.correspondent_name}</div></td>
              <td className="muted nowrap">{ago(d.deleted_at || d.modified_at)}</td>
              <td className="right nowrap"><button className="btn sm" onClick={() => act(() => api.post(`${V1}/documents/${d.id}/restore`), 'Restored')}>Restore</button> <button className="btn ghost sm danger-text" onClick={() => setPurge(d)}>Delete for good</button></td>
            </tr>)}</tbody>
          </table>
        </Card>
      )}
      {empty && <Confirm title={`Delete ${plural(data?.count || 0, 'document')} for good?`} message="Their files are removed from disk. This cannot be undone." onCancel={() => setEmpty(false)} onConfirm={() => { setEmpty(false); act(() => api.del(`${V1}/trash`), 'Trash emptied') }} label="Empty trash" />}
      {purge && <Confirm title={`Delete “${purge.title}” for good?`} message="The file is removed from disk. This cannot be undone." onCancel={() => setPurge(null)} onConfirm={() => { const p = purge; setPurge(null); act(() => api.del(`${V1}/documents/${p.id}/purge`), 'Deleted') }} label="Delete" />}
    </>
  )
}
