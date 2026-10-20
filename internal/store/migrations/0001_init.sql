CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'admin',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, OCR defaults the admin changes) and per-person preferences.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);
CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

-- Everything below belongs to one person (user_id). Nobody else sees it.

-- Tags, correspondents, document types and storage paths share the matching
-- columns Paperless uses: a document that matches gets the item assigned when
-- it is consumed. matching_algorithm: 0 none, 1 any word, 2 all words,
-- 3 exact phrase, 4 regular expression, 5 fuzzy, 6 auto (learned from your documents).
CREATE TABLE tags (
  id                 INTEGER PRIMARY KEY,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               TEXT NOT NULL,
  color              TEXT NOT NULL DEFAULT '#a6cee3',
  match              TEXT NOT NULL DEFAULT '',
  matching_algorithm INTEGER NOT NULL DEFAULT 6,
  is_insensitive     INTEGER NOT NULL DEFAULT 1,
  is_inbox           INTEGER NOT NULL DEFAULT 0,
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX tags_user_name ON tags(user_id, name COLLATE NOCASE);

CREATE TABLE correspondents (
  id                 INTEGER PRIMARY KEY,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               TEXT NOT NULL,
  match              TEXT NOT NULL DEFAULT '',
  matching_algorithm INTEGER NOT NULL DEFAULT 6,
  is_insensitive     INTEGER NOT NULL DEFAULT 1,
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX correspondents_user_name ON correspondents(user_id, name COLLATE NOCASE);

CREATE TABLE document_types (
  id                 INTEGER PRIMARY KEY,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               TEXT NOT NULL,
  match              TEXT NOT NULL DEFAULT '',
  matching_algorithm INTEGER NOT NULL DEFAULT 6,
  is_insensitive     INTEGER NOT NULL DEFAULT 1,
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX document_types_user_name ON document_types(user_id, name COLLATE NOCASE);

-- A storage path is a folder layout: {created_year}/{correspondent}/{title}.
-- It names where the document sits in the mirror folder and in exports.
CREATE TABLE storage_paths (
  id                 INTEGER PRIMARY KEY,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               TEXT NOT NULL,
  path               TEXT NOT NULL DEFAULT '{created_year}/{title}',
  match              TEXT NOT NULL DEFAULT '',
  matching_algorithm INTEGER NOT NULL DEFAULT 6,
  is_insensitive     INTEGER NOT NULL DEFAULT 1,
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX storage_paths_user_name ON storage_paths(user_id, name COLLATE NOCASE);

-- Custom fields: data_type string | url | date | boolean | integer | float | monetary | select | documentlink
CREATE TABLE custom_fields (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  data_type  TEXT NOT NULL DEFAULT 'string',
  extra      TEXT NOT NULL DEFAULT '{}',   -- JSON: {"options": [...], "currency": "EUR"}
  position   INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX custom_fields_user_name ON custom_fields(user_id, name COLLATE NOCASE);

-- status: pending | processing | done | failed
-- source: web | api | consume | import | paperless | share
CREATE TABLE documents (
  id                INTEGER PRIMARY KEY,
  user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title             TEXT NOT NULL DEFAULT '',
  content           TEXT NOT NULL DEFAULT '',
  correspondent_id  INTEGER REFERENCES correspondents(id) ON DELETE SET NULL,
  document_type_id  INTEGER REFERENCES document_types(id) ON DELETE SET NULL,
  storage_path_id   INTEGER REFERENCES storage_paths(id) ON DELETE SET NULL,
  asn               INTEGER,
  created           TEXT NOT NULL DEFAULT (date('now')),   -- the date on the paper, YYYY-MM-DD
  added_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  modified_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  original_name     TEXT NOT NULL DEFAULT '',
  original_ext      TEXT NOT NULL DEFAULT '',
  mime              TEXT NOT NULL DEFAULT '',
  checksum          TEXT NOT NULL DEFAULT '',
  size              INTEGER NOT NULL DEFAULT 0,
  archive_checksum  TEXT NOT NULL DEFAULT '',
  archive_size      INTEGER NOT NULL DEFAULT 0,
  has_archive       INTEGER NOT NULL DEFAULT 0,
  has_thumb         INTEGER NOT NULL DEFAULT 0,
  page_count        INTEGER NOT NULL DEFAULT 0,
  language          TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT 'pending',
  status_message    TEXT NOT NULL DEFAULT '',
  source            TEXT NOT NULL DEFAULT 'web',
  favorite          INTEGER NOT NULL DEFAULT 0,
  content_edited    INTEGER NOT NULL DEFAULT 0,  -- a person corrected the text: reprocessing keeps it
  mirror_path       TEXT NOT NULL DEFAULT '',   -- where the mirror folder has it now
  deleted_at        TEXT
);
CREATE INDEX documents_user_created ON documents(user_id, deleted_at, created DESC, id DESC);
CREATE INDEX documents_user_added ON documents(user_id, deleted_at, added_at DESC);
CREATE INDEX documents_user_status ON documents(user_id, status);
CREATE INDEX documents_checksum ON documents(user_id, checksum);
CREATE UNIQUE INDEX documents_user_asn ON documents(user_id, asn) WHERE asn IS NOT NULL;

CREATE TABLE document_tags (
  document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  tag_id      INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (document_id, tag_id)
);
CREATE INDEX document_tags_tag ON document_tags(tag_id);

CREATE TABLE document_fields (
  document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  field_id    INTEGER NOT NULL REFERENCES custom_fields(id) ON DELETE CASCADE,
  value       TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (document_id, field_id)
);

CREATE TABLE notes (
  id          INTEGER PRIMARY KEY,
  document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  text        TEXT NOT NULL,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX notes_document ON notes(document_id);

-- What happened to a document: action added | updated | processed | failed | note | deleted | restored | reprocessed | shared | ...
CREATE TABLE history (
  id          INTEGER PRIMARY KEY,
  document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  user_id     INTEGER NOT NULL,
  actor       TEXT NOT NULL DEFAULT '',   -- who or what: a person's name, "consume folder", "api", the app acting through Core
  action      TEXT NOT NULL,
  changes     TEXT NOT NULL DEFAULT '{}', -- JSON {field: [old, new]}
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX history_document ON history(document_id, id DESC);

-- Full text. Not an external-content table: the row carries joined names
-- (correspondent, type, tags, custom fields) so one MATCH finds them all.
-- Weights at query time: title > extra > content.
CREATE VIRTUAL TABLE documents_fts USING fts5(
  title, content, extra, notes,
  tokenize='unicode61 remove_diacritics 2'
);
-- Substring matches ("nvoic" finds "Invoice") when word search finds nothing.
CREATE VIRTUAL TABLE documents_trigram USING fts5(
  title, content,
  tokenize='trigram'
);

-- Processing queue. kind: process | reprocess | mirror | import_paperless | export
CREATE TABLE jobs (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  document_id INTEGER REFERENCES documents(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'queued',  -- queued | running | done | failed | cancelled
  attempts    INTEGER NOT NULL DEFAULT 0,
  progress    TEXT NOT NULL DEFAULT '',
  log         TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  params      TEXT NOT NULL DEFAULT '{}',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  started_at  TEXT,
  finished_at TEXT
);
CREATE INDEX jobs_status ON jobs(status, id);
CREATE INDEX jobs_user ON jobs(user_id, id DESC);

-- A saved view is a search (query string + filters + sort) with a name.
CREATE TABLE saved_views (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  query         TEXT NOT NULL DEFAULT '{}',  -- JSON of the list parameters
  on_dashboard  INTEGER NOT NULL DEFAULT 0,
  in_sidebar    INTEGER NOT NULL DEFAULT 1,
  position      INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Rules run when a document comes in (or is changed): if the filters match,
-- the actions apply. Filters and actions are JSON so new ones need no migration.
CREATE TABLE rules (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  enabled    INTEGER NOT NULL DEFAULT 1,
  trigger    TEXT NOT NULL DEFAULT 'added',   -- added | updated
  position   INTEGER NOT NULL DEFAULT 0,
  filters    TEXT NOT NULL DEFAULT '{}',
  actions    TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Public links to one document: anyone with the link may view and download.
CREATE TABLE shares (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  token       TEXT NOT NULL UNIQUE,
  file        TEXT NOT NULL DEFAULT 'archive', -- archive | original
  expires_at  TEXT,
  views       INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX shares_document ON shares(document_id);
