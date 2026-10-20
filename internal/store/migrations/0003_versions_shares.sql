-- Earlier files of a document: the original before a rotate, a page edit or a replacement.
CREATE TABLE document_versions (
  id          INTEGER PRIMARY KEY,
  document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  n           INTEGER NOT NULL,
  ext         TEXT NOT NULL DEFAULT '',
  size        INTEGER NOT NULL DEFAULT 0,
  checksum    TEXT NOT NULL DEFAULT '',
  reason      TEXT NOT NULL DEFAULT '',
  actor       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (document_id, n)
);

-- A document its owner shares with another account on this box: level view | edit.
CREATE TABLE document_shares (
  document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  level       TEXT NOT NULL DEFAULT 'view',
  shared_by   INTEGER NOT NULL,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (document_id, user_id)
);
CREATE INDEX document_shares_user ON document_shares(user_id);

-- Which scheduled rule already fired for which document.
CREATE TABLE rule_runs (
  rule_id     INTEGER NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
  document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  ran_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (rule_id, document_id)
);
