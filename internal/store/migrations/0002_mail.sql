-- Mail: IMAP accounts polled for documents, rules that say which messages
-- (and which parts of them) become documents, and the set of messages each
-- rule has already consumed. Everything belongs to one person (user_id).

-- security: ssl | starttls | none
CREATE TABLE mail_accounts (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  host            TEXT NOT NULL,
  port            INTEGER NOT NULL DEFAULT 993,
  security        TEXT NOT NULL DEFAULT 'ssl',
  username        TEXT NOT NULL DEFAULT '',
  password        TEXT NOT NULL DEFAULT '',
  enabled         INTEGER NOT NULL DEFAULT 1,
  poll_minutes    INTEGER NOT NULL DEFAULT 10,
  last_checked_at TEXT,
  last_error      TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX mail_accounts_user ON mail_accounts(user_id, id);

-- Filters are "contains", case-insensitive; empty = any. filter_attachment is
-- a glob on the file name. consume: attachments | eml | both.
-- title_from: subject | filename | none. correspondent_from: nothing | email | name
-- (used when assign_correspondent is empty). action: mark_read | move | delete | flag | none.
CREATE TABLE mail_rules (
  id                     INTEGER PRIMARY KEY,
  user_id                INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  account_id             INTEGER NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
  name                   TEXT NOT NULL,
  enabled                INTEGER NOT NULL DEFAULT 1,
  position               INTEGER NOT NULL DEFAULT 0,
  folder                 TEXT NOT NULL DEFAULT 'INBOX',
  filter_from            TEXT NOT NULL DEFAULT '',
  filter_to              TEXT NOT NULL DEFAULT '',
  filter_subject         TEXT NOT NULL DEFAULT '',
  filter_body            TEXT NOT NULL DEFAULT '',
  filter_attachment      TEXT NOT NULL DEFAULT '',
  max_age_days           INTEGER NOT NULL DEFAULT 0,
  consume                TEXT NOT NULL DEFAULT 'attachments',
  title_from             TEXT NOT NULL DEFAULT 'subject',
  assign_tags            TEXT NOT NULL DEFAULT '[]',
  assign_correspondent   INTEGER REFERENCES correspondents(id) ON DELETE SET NULL,
  assign_document_type   INTEGER REFERENCES document_types(id) ON DELETE SET NULL,
  assign_storage_path    INTEGER REFERENCES storage_paths(id) ON DELETE SET NULL,
  correspondent_from     TEXT NOT NULL DEFAULT 'nothing',
  action                 TEXT NOT NULL DEFAULT 'mark_read',
  action_folder          TEXT NOT NULL DEFAULT '',
  only_unseen            INTEGER NOT NULL DEFAULT 1,
  created_at             TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX mail_rules_user ON mail_rules(user_id, position, id);
CREATE INDEX mail_rules_account ON mail_rules(account_id);

-- A message is consumed once per rule: the IMAP UID is only unique within a
-- folder's UIDVALIDITY, so that is part of the key.
CREATE TABLE mail_seen (
  account_id   INTEGER NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
  rule_id      INTEGER NOT NULL REFERENCES mail_rules(id) ON DELETE CASCADE,
  uid_validity INTEGER NOT NULL,
  uid          INTEGER NOT NULL,
  message_id   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (account_id, rule_id, uid_validity, uid)
);
