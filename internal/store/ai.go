package store

import (
	"context"
	"encoding/json"
	"strings"
)

// AIExample is one filed document the AI sees as an example of how the
// person files their papers.
type AIExample struct {
	Title         string
	Created       string
	Correspondent string
	Type          string
	Path          string
	Tags          []string
}

// AIExamples returns the person's most recently added documents that carry
// a correspondent or a type, newest first, without the document being filled.
func (s *Store) AIExamples(ctx context.Context, userID, exceptID int64, limit int) []AIExample {
	rows, err := s.DB.QueryContext(ctx, `SELECT d.title, d.created, COALESCE(c.name, ''), COALESCE(t.name, ''), COALESCE(p.name, ''),
		COALESCE((SELECT group_concat(g.name, '|') FROM document_tags dt JOIN tags g ON g.id = dt.tag_id WHERE dt.document_id = d.id), '')
		FROM documents d
		LEFT JOIN correspondents c ON c.id = d.correspondent_id
		LEFT JOIN document_types t ON t.id = d.document_type_id
		LEFT JOIN storage_paths p ON p.id = d.storage_path_id
		WHERE d.user_id = ? AND d.id <> ? AND d.deleted_at IS NULL AND (d.correspondent_id IS NOT NULL OR d.document_type_id IS NOT NULL)
		ORDER BY d.added_at DESC, d.id DESC LIMIT ?`, userID, exceptID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AIExample
	for rows.Next() {
		var e AIExample
		var tags string
		if rows.Scan(&e.Title, &e.Created, &e.Correspondent, &e.Type, &e.Path, &tags) != nil {
			continue
		}
		if tags != "" {
			e.Tags = strings.Split(tags, "|")
		}
		out = append(out, e)
	}
	return out
}

// DocAI is what the AI did with one document, or is waiting to do.
type DocAI struct {
	DocumentID int64           `json:"document_id"`
	UserID     int64           `json:"-"`
	Status     string          `json:"status"` // pending | done | error | skipped
	Params     json.RawMessage `json:"-"`
	Summary    string          `json:"summary"`
	Reason     string          `json:"reason"`
	Applied    []string        `json:"applied"`
	Model      string          `json:"model"`
	Error      string          `json:"error"`
	Attempts   int             `json:"attempts"`
	NextTryAt  string          `json:"next_try_at"`
	UpdatedAt  string          `json:"updated_at"`
}

const docAICols = `document_id, user_id, status, params, summary, reason, applied, model, error, attempts, next_try_at, updated_at`

func scanDocAI(sc interface{ Scan(...any) error }) (*DocAI, error) {
	var a DocAI
	var params, applied string
	if err := sc.Scan(&a.DocumentID, &a.UserID, &a.Status, &params, &a.Summary, &a.Reason, &applied, &a.Model, &a.Error, &a.Attempts, &a.NextTryAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Params = json.RawMessage(params)
	_ = json.Unmarshal([]byte(applied), &a.Applied)
	if a.Applied == nil {
		a.Applied = []string{}
	}
	return &a, nil
}

// GetDocAI returns the AI record of a document.
func (s *Store) GetDocAI(ctx context.Context, documentID int64) (*DocAI, error) {
	a, err := scanDocAI(s.DB.QueryRowContext(ctx, `SELECT `+docAICols+` FROM document_ai WHERE document_id = ?`, documentID))
	return a, notFound(err)
}

// SaveDocAI writes the AI record of a document.
func (s *Store) SaveDocAI(ctx context.Context, a *DocAI) error {
	if len(a.Params) == 0 {
		a.Params = json.RawMessage("{}")
	}
	if a.Applied == nil {
		a.Applied = []string{}
	}
	applied, _ := json.Marshal(a.Applied)
	a.UpdatedAt = Now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO document_ai (`+docAICols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(document_id) DO UPDATE SET user_id=excluded.user_id, status=excluded.status, params=excluded.params, summary=excluded.summary, reason=excluded.reason,
		applied=excluded.applied, model=excluded.model, error=excluded.error, attempts=excluded.attempts, next_try_at=excluded.next_try_at, updated_at=excluded.updated_at`,
		a.DocumentID, a.UserID, a.Status, string(a.Params), a.Summary, a.Reason, string(applied), a.Model, a.Error, a.Attempts, a.NextTryAt, a.UpdatedAt)
	return err
}

// DueDocAI lists records waiting for the AI whose time has come, for
// documents that are read (OCR done) and not in the trash, oldest first.
func (s *Store) DueDocAI(ctx context.Context, now string, limit int) ([]DocAI, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+prefixCols("a.", docAICols)+` FROM document_ai a JOIN documents d ON d.id = a.document_id
		WHERE a.status IN ('pending','error') AND a.next_try_at <> '' AND a.next_try_at <= ? AND d.status = 'done' AND d.deleted_at IS NULL
		ORDER BY a.next_try_at, a.document_id LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DocAI
	for rows.Next() {
		a, err := scanDocAI(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func prefixCols(p, cols string) string {
	parts := strings.Split(cols, ", ")
	for i := range parts {
		parts[i] = p + parts[i]
	}
	return strings.Join(parts, ", ")
}

// AICounts is how far the AI got with one person's documents.
type AICounts struct {
	Done    int `json:"done"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Never   int `json:"never"` // documents with text the AI has not read
}

// DocAICounts counts the person's documents by AI status.
func (s *Store) DocAICounts(ctx context.Context, userID int64) AICounts {
	var c AICounts
	rows, err := s.DB.QueryContext(ctx, `SELECT a.status, COUNT(*) FROM document_ai a JOIN documents d ON d.id = a.document_id
		WHERE a.user_id = ? AND d.deleted_at IS NULL GROUP BY a.status`, userID)
	if err == nil {
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) != nil {
				continue
			}
			switch st {
			case "done":
				c.Done = n
			case "pending":
				c.Pending = n
			case "error":
				c.Failed = n
			case "skipped":
				c.Skipped = n
			}
		}
		rows.Close()
	}
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents d WHERE d.user_id = ? AND d.deleted_at IS NULL AND d.status = 'done' AND trim(d.content) <> ''
		AND NOT EXISTS (SELECT 1 FROM document_ai a WHERE a.document_id = d.id)`, userID).Scan(&c.Never)
	return c
}

// QueueDocAI puts the person's documents the AI has not read yet (and,
// with failed, the ones it gave up on) in the AI's queue with params.
func (s *Store) QueueDocAI(ctx context.Context, userID int64, params string, failed bool) (int, error) {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO document_ai (document_id, user_id, status, params, next_try_at, updated_at)
		SELECT d.id, d.user_id, 'pending', ?, ?, ? FROM documents d
		WHERE d.user_id = ? AND d.deleted_at IS NULL AND trim(d.content) <> '' AND NOT EXISTS (SELECT 1 FROM document_ai a WHERE a.document_id = d.id)`, params, now, now, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if failed {
		res, err := s.DB.ExecContext(ctx, `UPDATE document_ai SET status = 'pending', attempts = 0, error = '', next_try_at = ?, updated_at = ? WHERE user_id = ? AND status = 'error'`, now, now, userID)
		if err != nil {
			return int(n), err
		}
		m, _ := res.RowsAffected()
		n += m
	}
	return int(n), nil
}
