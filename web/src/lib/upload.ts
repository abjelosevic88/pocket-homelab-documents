import { V1 } from './api'
import type { Document } from './types'

export interface UploadItem { file: File; status: 'waiting' | 'uploading' | 'done' | 'error'; progress: number; document?: Document; error?: string }

/** Upload files one by one with progress (XHR, so progress events work). */
export async function uploadFiles(files: File[], meta: Record<string, string>, onChange: (items: UploadItem[]) => void): Promise<UploadItem[]> {
  const items: UploadItem[] = files.map(f => ({ file: f, status: 'waiting', progress: 0 }))
  const push = () => onChange(items.map(i => ({ ...i })))
  push()
  for (const it of items) {
    it.status = 'uploading'; push()
    await new Promise<void>(resolve => {
      const fd = new FormData()
      for (const [k, v] of Object.entries(meta)) if (v !== '' && v != null) fd.append(k, v)
      fd.append('document', it.file, it.file.name)
      const xhr = new XMLHttpRequest()
      xhr.open('POST', `${V1}/documents`)
      xhr.upload.onprogress = e => { if (e.lengthComputable) { it.progress = e.loaded / e.total; push() } }
      xhr.onload = () => {
        try {
          const j = JSON.parse(xhr.responseText)
          if (xhr.status >= 200 && xhr.status < 300) { it.status = 'done'; it.document = j }
          else { it.status = 'error'; it.error = j.error || xhr.statusText; if (j.duplicate_of) it.document = { id: j.duplicate_of } as Document }
        } catch { it.status = 'error'; it.error = xhr.statusText }
        push(); resolve()
      }
      xhr.onerror = () => { it.status = 'error'; it.error = 'network error'; push(); resolve() }
      xhr.send(fd)
    })
  }
  return items
}
