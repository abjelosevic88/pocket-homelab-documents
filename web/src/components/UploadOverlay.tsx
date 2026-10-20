import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Modal } from './ui'
import { useApp } from '../lib/app-context'
import { uploadFiles, type UploadItem } from '../lib/upload'

/** Shows the progress of files dropped anywhere on the page. */
export function UploadOverlay({ files, onClose }: { files: File[]; onClose: () => void }) {
  const [items, setItems] = useState<UploadItem[]>(() => files.map(f => ({ file: f, status: 'waiting', progress: 0 })))
  const { bump } = useApp()
  useEffect(() => { uploadFiles(files, {}, setItems).then(() => bump()) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const done = items.every(i => i.status === 'done' || i.status === 'error')
  return (
    <Modal title={done ? 'Uploaded' : 'Uploading…'} onClose={onClose}>
      <ul className="upload-list">
        {items.map((it, i) => <li key={i} className={it.status}>
          <span className="grow ellipsis">{it.file.name}</span>
          {it.status === 'uploading' && <span className="muted small">{Math.round(it.progress * 100)}%</span>}
          {it.status === 'done' && it.document && <Link to={`/documents/${it.document.id}`}>open</Link>}
          {it.status === 'error' && <span className="bad small">{it.error}</span>}
        </li>)}
      </ul>
      {done && <p className="muted small mt">Processing continues in the background; the documents appear with their text once OCR is through.</p>}
      <div className="form-actions"><button className="btn primary" onClick={onClose}>Close</button></div>
    </Modal>
  )
}
