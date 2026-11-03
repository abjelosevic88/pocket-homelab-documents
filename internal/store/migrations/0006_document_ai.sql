-- What the AI did with each document, or is still waiting to do: new uploads
-- when no AI was reachable, older documents queued from Settings → AI, and
-- failed runs that are tried again later.
CREATE TABLE document_ai(
  document_id INTEGER PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending', -- pending | done | error | skipped
  params TEXT NOT NULL DEFAULT '{}',      -- what the AI may change (server.aiFillMode)
  summary TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  applied TEXT NOT NULL DEFAULT '[]',
  model TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0,
  next_try_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE INDEX document_ai_due ON document_ai(status, next_try_at);
CREATE INDEX document_ai_user ON document_ai(user_id, status);
