package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// ListSavedViews lists a person's saved views in order.
func (s *Store) ListSavedViews(ctx context.Context, userID int64) ([]SavedView, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, name, query, on_dashboard, in_sidebar, position, created_at FROM saved_views WHERE user_id = ? ORDER BY position, name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedView{}
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func scanView(row interface{ Scan(...any) error }) (*SavedView, error) {
	var v SavedView
	var q string
	var d, sb int
	if err := row.Scan(&v.ID, &v.UserID, &v.Name, &q, &d, &sb, &v.Position, &v.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	v.Query = json.RawMessage(q)
	v.OnDashboard, v.InSidebar = d == 1, sb == 1
	return &v, nil
}

// GetSavedView returns one view.
func (s *Store) GetSavedView(ctx context.Context, userID, id int64) (*SavedView, error) {
	return scanView(s.DB.QueryRowContext(ctx, `SELECT id, user_id, name, query, on_dashboard, in_sidebar, position, created_at FROM saved_views WHERE id = ? AND user_id = ?`, id, userID))
}

func (v *SavedView) check() error {
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" {
		return errors.New("a view needs a name")
	}
	if len(v.Query) == 0 || !json.Valid(v.Query) {
		v.Query = json.RawMessage("{}")
	}
	return nil
}

// CreateSavedView inserts a view.
func (s *Store) CreateSavedView(ctx context.Context, v *SavedView) error {
	if err := v.check(); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO saved_views (user_id, name, query, on_dashboard, in_sidebar, position) VALUES (?, ?, ?, ?, ?, ?)`, v.UserID, v.Name, string(v.Query), boolInt(v.OnDashboard), boolInt(v.InSidebar), v.Position)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	v.CreatedAt = Now()
	return nil
}

// UpdateSavedView saves a view.
func (s *Store) UpdateSavedView(ctx context.Context, v *SavedView) error {
	if err := v.check(); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE saved_views SET name = ?, query = ?, on_dashboard = ?, in_sidebar = ?, position = ? WHERE id = ? AND user_id = ?`, v.Name, string(v.Query), boolInt(v.OnDashboard), boolInt(v.InSidebar), v.Position, v.ID, v.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSavedView removes a view.
func (s *Store) DeleteSavedView(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM saved_views WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- rules ----

func scanRule(row interface{ Scan(...any) error }) (*Rule, error) {
	var r Rule
	var en int
	var f, a string
	if err := row.Scan(&r.ID, &r.UserID, &r.Name, &en, &r.Trigger, &r.Position, &f, &a, &r.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	r.Enabled = en == 1
	_ = json.Unmarshal([]byte(f), &r.Filters)
	_ = json.Unmarshal([]byte(a), &r.Actions)
	if r.Filters.Tags == nil {
		r.Filters.Tags = []int64{}
	}
	if r.Actions.AddTags == nil {
		r.Actions.AddTags = []int64{}
	}
	if r.Actions.RemoveTags == nil {
		r.Actions.RemoveTags = []int64{}
	}
	if r.Actions.Fields == nil {
		r.Actions.Fields = map[string]string{}
	}
	return &r, nil
}

// ListRules lists a person's rules in order.
func (s *Store) ListRules(ctx context.Context, userID int64) ([]Rule, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, name, enabled, trigger, position, filters, actions, created_at FROM rules WHERE user_id = ? ORDER BY position, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetRule returns one rule.
func (s *Store) GetRule(ctx context.Context, userID, id int64) (*Rule, error) {
	return scanRule(s.DB.QueryRowContext(ctx, `SELECT id, user_id, name, enabled, trigger, position, filters, actions, created_at FROM rules WHERE id = ? AND user_id = ?`, id, userID))
}

func (r *Rule) check() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errors.New("a rule needs a name")
	}
	if r.Trigger != "updated" {
		r.Trigger = "added"
	}
	return nil
}

// CreateRule inserts a rule.
func (s *Store) CreateRule(ctx context.Context, r *Rule) error {
	if err := r.check(); err != nil {
		return err
	}
	f, _ := json.Marshal(r.Filters)
	a, _ := json.Marshal(r.Actions)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO rules (user_id, name, enabled, trigger, position, filters, actions) VALUES (?, ?, ?, ?, ?, ?, ?)`, r.UserID, r.Name, boolInt(r.Enabled), r.Trigger, r.Position, string(f), string(a))
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	r.CreatedAt = Now()
	return nil
}

// UpdateRule saves a rule.
func (s *Store) UpdateRule(ctx context.Context, r *Rule) error {
	if err := r.check(); err != nil {
		return err
	}
	f, _ := json.Marshal(r.Filters)
	a, _ := json.Marshal(r.Actions)
	res, err := s.DB.ExecContext(ctx, `UPDATE rules SET name = ?, enabled = ?, trigger = ?, position = ?, filters = ?, actions = ? WHERE id = ? AND user_id = ?`, r.Name, boolInt(r.Enabled), r.Trigger, r.Position, string(f), string(a), r.ID, r.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRule removes a rule.
func (s *Store) DeleteRule(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM rules WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- shares ----

const shareCols = `s.id, s.user_id, s.document_id, COALESCE((SELECT d.title FROM documents d WHERE d.id = s.document_id), ''), s.token, s.file, s.expires_at, s.views, s.created_at`

func scanShare(row interface{ Scan(...any) error }) (*Share, error) {
	var sh Share
	if err := row.Scan(&sh.ID, &sh.UserID, &sh.DocumentID, &sh.DocumentTitle, &sh.Token, &sh.File, &sh.ExpiresAt, &sh.Views, &sh.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	return &sh, nil
}

// CreateShare makes a public link.
func (s *Store) CreateShare(ctx context.Context, sh *Share) error {
	if sh.File != "original" {
		sh.File = "archive"
	}
	sh.Token = RandomToken(16)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO shares (user_id, document_id, token, file, expires_at) VALUES (?, ?, ?, ?, ?)`, sh.UserID, sh.DocumentID, sh.Token, sh.File, sh.ExpiresAt)
	if err != nil {
		return err
	}
	sh.ID, _ = res.LastInsertId()
	sh.CreatedAt = Now()
	return nil
}

// ListShares lists a person's links (optionally of one document).
func (s *Store) ListShares(ctx context.Context, userID, documentID int64) ([]Share, error) {
	q := `SELECT ` + shareCols + ` FROM shares s WHERE s.user_id = ?`
	args := []any{userID}
	if documentID > 0 {
		q += ` AND s.document_id = ?`
		args = append(args, documentID)
	}
	q += ` ORDER BY s.id DESC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sh)
	}
	return out, rows.Err()
}

// ShareByToken resolves a public link that has not expired and counts the view.
func (s *Store) ShareByToken(ctx context.Context, token string) (*Share, error) {
	sh, err := scanShare(s.DB.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares s WHERE s.token = ?`, token))
	if err != nil {
		return nil, err
	}
	if sh.ExpiresAt != nil && *sh.ExpiresAt != "" && *sh.ExpiresAt < Now() {
		return nil, ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE shares SET views = views + 1 WHERE id = ?`, sh.ID)
	return sh, nil
}

// DeleteShare revokes a link.
func (s *Store) DeleteShare(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shares WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
