export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export interface Matchable { match: string; matching_algorithm: number; is_insensitive: boolean }
export interface Tag extends Matchable { id: number; name: string; color: string; is_inbox: boolean; document_count: number; created_at: string }
export interface Named extends Matchable { id: number; name: string; path?: string; document_count: number; last_created?: string; created_at: string }
export type FieldType = 'string' | 'url' | 'date' | 'boolean' | 'integer' | 'float' | 'monetary' | 'select' | 'documentlink'
export interface CustomField { id: number; name: string; data_type: FieldType; extra: { options?: string[]; currency?: string }; position: number; document_count: number; created_at: string }
export interface FieldValue { field: number; value: string }

export interface Document {
  id: number
  title: string
  content?: string
  correspondent: number | null
  document_type: number | null
  storage_path: number | null
  asn: number | null
  created: string
  added_at: string
  modified_at: string
  original_name: string
  mime: string
  checksum: string
  size: number
  archive_size: number
  has_archive: boolean
  has_thumb: boolean
  page_count: number
  language: string
  status: 'pending' | 'processing' | 'done' | 'failed'
  status_message: string
  source: string
  favorite: boolean
  content_edited: boolean
  mirror_path?: string
  deleted_at?: string | null
  tags: number[]
  custom_fields: FieldValue[]
  notes_count: number
  snippet?: string
  rank?: number
  correspondent_name?: string
  document_type_name?: string
  access?: 'owner' | 'view' | 'edit'
  owner_name?: string
  shared_tags?: string[]
}
export interface Version { id: number; document_id: number; n: number; ext: string; size: number; checksum: string; reason: string; actor: string; created_at: string }
export interface DocShare { user_id: number; level: 'view' | 'edit'; name?: string; email?: string; created_at: string }
export interface Language { code: string; name: string; installed: boolean; source?: 'system' | 'downloaded' }

export interface Facets { tags: Record<string, number>; correspondents: Record<string, number>; document_types: Record<string, number>; storage_paths: Record<string, number>; status: Record<string, number>; untagged: number; no_correspondent: number; no_type: number }
export interface ListResult { results: Document[]; count: number; facets: Facets | null; offset: number; limit: number }

export interface Note { id: number; document_id: number; user_id: number; text: string; created_at: string }
export interface HistoryEntry { id: number; document_id: number; actor: string; action: string; changes: Record<string, unknown>; created_at: string }
export interface Job { id: number; user_id: number; document_id: number | null; kind: string; status: 'queued' | 'running' | 'done' | 'failed' | 'cancelled'; attempts: number; progress: string; log?: string; error: string; params: Record<string, unknown>; created_at: string; started_at: string | null; finished_at: string | null; document_title?: string }
export interface SavedView { id: number; name: string; query: Record<string, unknown>; on_dashboard: boolean; in_sidebar: boolean; position: number; created_at: string }
export interface RuleFilters { filename: string; path: string; source: string; mime: string; content: string; correspondent: number | null; document_type: number | null; tags: number[]; schedule_field?: string; schedule_offset_days?: number }
export interface RuleActions { title: string; add_tags: number[]; remove_tags: number[]; correspondent: number | null; document_type: number | null; storage_path: number | null; fields: Record<string, string>; favorite: boolean | null; skip_matching: boolean; notify?: boolean; notify_message?: string }
export interface Rule { id: number; name: string; enabled: boolean; trigger: 'added' | 'updated' | 'scheduled'; position: number; filters: RuleFilters; actions: RuleActions; created_at: string }
export interface Share { id: number; document_id: number; document_title?: string; token: string; file: 'archive' | 'original'; expires_at: string | null; views: number; created_at: string; url: string }
export interface Suggestions { tags: number[]; correspondents: number[]; document_types: number[]; storage_paths: number[]; dates: string[]; confidence: Record<string, number>; trained_on: number }

export interface Taxonomy { tags: Tag[]; correspondents: Named[]; document_types: Named[]; storage_paths: Named[]; custom_fields: CustomField[]; saved_views: SavedView[] }

export interface Tools { ocrmypdf: boolean; tesseract: boolean; pdftotext: boolean; pdftoppm: boolean; pdfinfo: boolean; qpdf: boolean; unpaper: boolean; zbar: boolean; languages: string[]; gotenberg: boolean; versions: Record<string, string> }

export interface Settings {
  title: string
  setup_complete: boolean
  ocr_language: string
  ocr_mode: 'skip' | 'redo' | 'force'
  ocr_deskew: boolean
  ocr_rotate: boolean
  ocr_clean: boolean
  ocr_max_pages: number
  ocr_output_type: 'pdfa' | 'pdf'
  trash_days: number
  consume_dir: string
  consume_subdirs_as_tags: boolean
  date_format: 'iso' | 'dmy-dot' | 'long'
  default_view: 'table' | 'cards' | 'list'
  page_size: number
  default_sort: string
  title_from_name: boolean
  date_from_text: boolean
  date_from_name: boolean
  suggest_on_open: boolean
  barcodes_enabled: boolean
  barcode_asn_prefix: string
  barcode_separator: string
  barcode_keep_separator: boolean
  barcode_max_pages: number
  money_mode: 'core' | 'direct' | 'off'
  money_url: string
  money_token_set: boolean
  money_account: number
  notify_url: string
  /** AI fills in new documents; picked like Pocket Money: automatic prefers Pocket AI */
  ai_auto: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'
  ai_pocket_model: string
  ai_status?: { provider: string; selected: string; ready: boolean; detail: string }
  ai_configured: boolean
  ai_url: string
  ai_key_set: boolean
  ai_model: string
  ai_model_effective: string
  ai_context: string
  core: boolean
  /** configured and registered: Core-only choices are greyed out otherwise */
  core_ready: boolean
  tools: Tools
  max_upload_bytes: number
  ocr_workers: number
}

export type FolderKind = 'none' | 'folder' | 'webdav' | 'nextcloud' | 'files'
export interface FolderStatus { where?: string; kind?: FolderKind; error?: string; last_write?: string; last_inbox?: string; inbox_error?: string; taken?: number; inbox_path?: string }
export interface FolderInfo {
  kind: FolderKind; path: string; url: string; user: string; folder: string; via_core: boolean; password_set: boolean
  file: 'archive' | 'original'; inbox: boolean; inbox_name: string
  roots: string[] | null; core: boolean; core_nextcloud?: { url: string; user: string }; core_nextcloud_error?: string
  core_files?: boolean; core_files_error?: string; core_files_url?: string
  status: FolderStatus
}

export interface Stats {
  documents: number; inbox: number; untagged: number; pending: number; failed: number; favorites: number; trash: number
  added_today: number; added_week: number; added_month: number; pages: number; bytes: number; archive_bytes: number; characters: number
  tags: number; correspondents: number; document_types: number
  by_month: { month: string; count: number }[]
  by_type: NameCount[]; by_correspondent: NameCount[]; by_tag: NameCount[]; by_mime: NameCount[]
  oldest: string; newest: string
}
export interface NameCount { id: number; name: string; count: number; color?: string }

export interface Dashboard {
  documents: number; inbox: number; untagged: number; pending: number; failed: number; added_today: number; added_week: number; added_month: number
  pages: number; bytes: number; archive_bytes: number; storage_mb: number; trash: number; favorites: number; queued: number; running: number; jobs_failed: number
  recent: Document[]; views: { view: SavedView; documents: Document[]; count: number }[]; stats: Stats
}

export const ALGORITHMS: { value: number; label: string; help: string }[] = [
  { value: 6, label: 'Auto', help: 'Learned from the documents you already filed with it (needs a few examples).' },
  { value: 1, label: 'Any word', help: 'One of the words appears in the document.' },
  { value: 2, label: 'All words', help: 'Every word appears somewhere.' },
  { value: 3, label: 'Exact phrase', help: 'The words appear together, in this order.' },
  { value: 4, label: 'Regular expression', help: 'A Go regular expression matched against the text.' },
  { value: 5, label: 'Fuzzy', help: 'The phrase appears with small typos (OCR mistakes).' },
  { value: 0, label: 'None', help: 'Never assigned automatically.' },
]

export const FIELD_TYPES: { value: FieldType; label: string }[] = [
  { value: 'string', label: 'Text' }, { value: 'monetary', label: 'Money' }, { value: 'date', label: 'Date' }, { value: 'integer', label: 'Whole number' },
  { value: 'float', label: 'Number' }, { value: 'boolean', label: 'Yes / no' }, { value: 'url', label: 'Link' }, { value: 'select', label: 'Choice' }, { value: 'documentlink', label: 'Document link' },
]
