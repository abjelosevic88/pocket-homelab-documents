package store

import (
	"context"
	"errors"
)

// Version is an earlier file of a document.
type Version struct {
	ID         int64  `json:"id"`
	DocumentID int64  `json:"document_id"`
	N          int    `json:"n"`
	Ext        string `json:"ext"`
	Size       int64  `json:"size"`
	Checksum   string `json:"checksum"`
	Reason     string `json:"reason"`
	Actor      string `json:"actor"`
	CreatedAt  string `json:"created_at"`
}

// AddVersion records that the current file was put aside (the caller moved it).
func (s *Store) AddVersion(ctx context.Context, v *Version) error {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(n), 0) FROM document_versions WHERE document_id = ?`, v.DocumentID).Scan(&n)
	v.N = n + 1
	res, err := s.DB.ExecContext(ctx, `INSERT INTO document_versions (document_id, n, ext, size, checksum, reason, actor) VALUES (?, ?, ?, ?, ?, ?, ?)`, v.DocumentID, v.N, v.Ext, v.Size, v.Checksum, v.Reason, v.Actor)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	v.CreatedAt = Now()
	return nil
}

// ListVersions lists a document's earlier files, newest first.
func (s *Store) ListVersions(ctx context.Context, documentID int64) ([]Version, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, document_id, n, ext, size, checksum, reason, actor, created_at FROM document_versions WHERE document_id = ? ORDER BY n DESC`, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.DocumentID, &v.N, &v.Ext, &v.Size, &v.Checksum, &v.Reason, &v.Actor, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetVersion returns one earlier file's record.
func (s *Store) GetVersion(ctx context.Context, documentID int64, n int) (*Version, error) {
	var v Version
	err := s.DB.QueryRowContext(ctx, `SELECT id, document_id, n, ext, size, checksum, reason, actor, created_at FROM document_versions WHERE document_id = ? AND n = ?`, documentID, n).
		Scan(&v.ID, &v.DocumentID, &v.N, &v.Ext, &v.Size, &v.Checksum, &v.Reason, &v.Actor, &v.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &v, nil
}

// ReplaceFile sets the document's current file details after its bytes changed.
func (s *Store) ReplaceFile(ctx context.Context, id int64, name, ext, mime, checksum string, size int64) error {
	if ext == "" {
		return errors.New("no extension")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE documents SET original_name = ?, original_ext = ?, mime = ?, checksum = ?, size = ?, has_archive = 0, archive_checksum = '', archive_size = 0, has_thumb = 0, page_count = 0, content_edited = 0, modified_at = ? WHERE id = ?`,
		name, ext, mime, checksum, size, Now(), id)
	return err
}

// ---- sharing between accounts ----

// Share is one grant of a document to another account.
type DocShare struct {
	DocumentID int64  `json:"document_id"`
	UserID     int64  `json:"user_id"`
	Level      string `json:"level"` // view | edit
	SharedBy   int64  `json:"shared_by"`
	CreatedAt  string `json:"created_at"`
}

// ShareDocument grants (or changes) another account's access.
func (s *Store) ShareDocument(ctx context.Context, documentID, userID, by int64, level string) error {
	if level != "edit" {
		level = "view"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO document_shares (document_id, user_id, level, shared_by) VALUES (?, ?, ?, ?) ON CONFLICT(document_id, user_id) DO UPDATE SET level = excluded.level`, documentID, userID, level, by)
	return err
}

// UnshareDocument takes the access back.
func (s *Store) UnshareDocument(ctx context.Context, documentID, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM document_shares WHERE document_id = ? AND user_id = ?`, documentID, userID)
	return err
}

// ListDocShares lists who a document is shared with.
func (s *Store) ListDocShares(ctx context.Context, documentID int64) ([]DocShare, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT document_id, user_id, level, shared_by, created_at FROM document_shares WHERE document_id = ? ORDER BY created_at`, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DocShare{}
	for rows.Next() {
		var sh DocShare
		if err := rows.Scan(&sh.DocumentID, &sh.UserID, &sh.Level, &sh.SharedBy, &sh.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// SharedLevel says how a document is open to a user: "" (not at all), view or edit.
func (s *Store) SharedLevel(ctx context.Context, documentID, userID int64) string {
	var level string
	_ = s.DB.QueryRowContext(ctx, `SELECT level FROM document_shares WHERE document_id = ? AND user_id = ?`, documentID, userID).Scan(&level)
	return level
}

// GetSharedDocument returns a document another account shared with userID,
// with the owner's names for tags, correspondent and type filled in.
func (s *Store) GetSharedDocument(ctx context.Context, userID, id int64, withContent bool) (*Document, string, error) {
	level := s.SharedLevel(ctx, id, userID)
	if level == "" {
		return nil, "", ErrNotFound
	}
	d, err := s.GetDocumentAny(ctx, id, withContent)
	if err != nil || d.DeletedAt != nil {
		return nil, "", ErrNotFound
	}
	s.fillShared(ctx, []*Document{d}, userID)
	return d, level, nil
}

// fillShared annotates documents owned by someone else with the owner and
// readable names (the viewer has no access to the owner's tag ids).
func (s *Store) fillShared(ctx context.Context, docs []*Document, viewer int64) {
	for _, d := range docs {
		if d.UserID == viewer {
			d.Access = "owner"
			continue
		}
		d.Access = s.SharedLevel(ctx, d.ID, viewer)
		if u, err := s.GetUser(ctx, d.UserID); err == nil {
			d.OwnerName = firstNonEmpty(u.Name, u.Email)
		}
		d.SharedTags = []string{}
		for _, t := range d.Tags {
			var name string
			if s.DB.QueryRowContext(ctx, `SELECT name FROM tags WHERE id = ?`, t).Scan(&name) == nil {
				d.SharedTags = append(d.SharedTags, name)
			}
		}
	}
}

// ListSharedWith lists the live documents other accounts shared with userID.
func (s *Store) ListSharedWith(ctx context.Context, userID int64, limit int) ([]Document, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+docCols+` FROM documents d JOIN document_shares x ON x.document_id = d.id WHERE x.user_id = ? AND d.deleted_at IS NULL ORDER BY x.created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		d, err := scanDoc(rows, false, false)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	ptrs := make([]*Document, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	s.fillRelations(ctx, ptrs)
	s.fillShared(ctx, ptrs, userID)
	return out, rows.Err()
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---- scheduled rules ----

// RuleRan reports whether a scheduled rule already fired for a document.
func (s *Store) RuleRan(ctx context.Context, ruleID, documentID int64) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM rule_runs WHERE rule_id = ? AND document_id = ?`, ruleID, documentID).Scan(&n)
	return n > 0
}

// MarkRuleRan records that it did.
func (s *Store) MarkRuleRan(ctx context.Context, ruleID, documentID int64) {
	_, _ = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO rule_runs (rule_id, document_id) VALUES (?, ?)`, ruleID, documentID)
}

// ResetRuleRuns forgets a rule's past firings (after it was changed).
func (s *Store) ResetRuleRuns(ctx context.Context, ruleID int64) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM rule_runs WHERE rule_id = ?`, ruleID)
}
