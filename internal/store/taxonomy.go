package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ---- tags ----

const tagCols = `t.id, t.user_id, t.name, t.color, t.match, t.matching_algorithm, t.is_insensitive, t.is_inbox, t.created_at,
	(SELECT COUNT(1) FROM document_tags dt JOIN documents d ON d.id = dt.document_id WHERE dt.tag_id = t.id AND d.deleted_at IS NULL)`

func scanTag(row interface{ Scan(...any) error }) (*Tag, error) {
	var t Tag
	var ins, inbox int
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Color, &t.Match, &t.MatchingAlgorithm, &ins, &inbox, &t.CreatedAt, &t.DocumentCount); err != nil {
		return nil, notFound(err)
	}
	t.IsInsensitive, t.IsInbox = ins == 1, inbox == 1
	return &t, nil
}

// ListTags lists a person's tags by name.
func (s *Store) ListTags(ctx context.Context, userID int64) ([]Tag, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+tagCols+` FROM tags t WHERE t.user_id = ? ORDER BY t.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetTag returns one of a person's tags.
func (s *Store) GetTag(ctx context.Context, userID, id int64) (*Tag, error) {
	return scanTag(s.DB.QueryRowContext(ctx, `SELECT `+tagCols+` FROM tags t WHERE t.id = ? AND t.user_id = ?`, id, userID))
}

// FindTag finds a tag by name (case-insensitive).
func (s *Store) FindTag(ctx context.Context, userID int64, name string) (*Tag, error) {
	return scanTag(s.DB.QueryRowContext(ctx, `SELECT `+tagCols+` FROM tags t WHERE t.user_id = ? AND t.name = ? COLLATE NOCASE`, userID, strings.TrimSpace(name)))
}

// CreateTag inserts a tag.
func (s *Store) CreateTag(ctx context.Context, t *Tag) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return errors.New("a tag needs a name")
	}
	if t.Color == "" {
		t.Color = "#a6cee3"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO tags (user_id, name, color, match, matching_algorithm, is_insensitive, is_inbox) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.UserID, t.Name, t.Color, t.Match, t.MatchingAlgorithm, boolInt(t.IsInsensitive), boolInt(t.IsInbox))
	if err != nil {
		return conflict(err)
	}
	t.ID, _ = res.LastInsertId()
	t.CreatedAt = Now()
	return nil
}

// UpdateTag saves a tag.
func (s *Store) UpdateTag(ctx context.Context, t *Tag) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return errors.New("a tag needs a name")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE tags SET name = ?, color = ?, match = ?, matching_algorithm = ?, is_insensitive = ?, is_inbox = ? WHERE id = ? AND user_id = ?`,
		t.Name, t.Color, t.Match, t.MatchingAlgorithm, boolInt(t.IsInsensitive), boolInt(t.IsInbox), t.ID, t.UserID)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTag removes a tag from every document and deletes it.
func (s *Store) DeleteTag(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// InboxTagIDs lists the ids of a person's inbox tags.
func (s *Store) InboxTagIDs(ctx context.Context, userID int64) []int64 {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND is_inbox = 1`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// ---- correspondents / document types / storage paths ----
//
// The three share one shape; "kind" picks the table.

type namedTable struct{ table, docCol string }

var (
	kindCorrespondent = namedTable{"correspondents", "correspondent_id"}
	kindDocumentType  = namedTable{"document_types", "document_type_id"}
	kindStoragePath   = namedTable{"storage_paths", "storage_path_id"}
)

// Named is the common shape of a correspondent, document type or storage path.
type Named struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"-"`
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"` // storage paths only
	Matchable
	DocumentCount int    `json:"document_count"`
	LastCreated   string `json:"last_created,omitempty"`
	CreatedAt     string `json:"created_at"`
}

func (s *Store) listNamed(ctx context.Context, k namedTable, userID int64) ([]Named, error) {
	pathCol := "''"
	if k == kindStoragePath {
		pathCol = "n.path"
	}
	q := fmt.Sprintf(`SELECT n.id, n.user_id, n.name, %s, n.match, n.matching_algorithm, n.is_insensitive, n.created_at,
		(SELECT COUNT(1) FROM documents d WHERE d.%s = n.id AND d.deleted_at IS NULL),
		COALESCE((SELECT MAX(d.created) FROM documents d WHERE d.%s = n.id AND d.deleted_at IS NULL), '')
		FROM %s n WHERE n.user_id = ? ORDER BY n.name COLLATE NOCASE`, pathCol, k.docCol, k.docCol, k.table)
	rows, err := s.DB.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Named{}
	for rows.Next() {
		var n Named
		var ins int
		if err := rows.Scan(&n.ID, &n.UserID, &n.Name, &n.Path, &n.Match, &n.MatchingAlgorithm, &ins, &n.CreatedAt, &n.DocumentCount, &n.LastCreated); err != nil {
			return nil, err
		}
		n.IsInsensitive = ins == 1
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) getNamed(ctx context.Context, k namedTable, userID, id int64) (*Named, error) {
	pathCol := "''"
	if k == kindStoragePath {
		pathCol = "path"
	}
	var n Named
	var ins int
	err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT id, user_id, name, %s, match, matching_algorithm, is_insensitive, created_at FROM %s WHERE id = ? AND user_id = ?`, pathCol, k.table), id, userID).
		Scan(&n.ID, &n.UserID, &n.Name, &n.Path, &n.Match, &n.MatchingAlgorithm, &ins, &n.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	n.IsInsensitive = ins == 1
	return &n, nil
}

func (s *Store) findNamed(ctx context.Context, k namedTable, userID int64, name string) (*Named, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE user_id = ? AND name = ? COLLATE NOCASE`, k.table), userID, strings.TrimSpace(name)).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.getNamed(ctx, k, userID, id)
}

func (s *Store) createNamed(ctx context.Context, k namedTable, n *Named) error {
	n.Name = strings.TrimSpace(n.Name)
	if n.Name == "" {
		return errors.New("a name is required")
	}
	var res sql.Result
	var err error
	if k == kindStoragePath {
		if strings.TrimSpace(n.Path) == "" {
			n.Path = "{created_year}/{title}"
		}
		res, err = s.DB.ExecContext(ctx, `INSERT INTO storage_paths (user_id, name, path, match, matching_algorithm, is_insensitive) VALUES (?, ?, ?, ?, ?, ?)`,
			n.UserID, n.Name, n.Path, n.Match, n.MatchingAlgorithm, boolInt(n.IsInsensitive))
	} else {
		res, err = s.DB.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (user_id, name, match, matching_algorithm, is_insensitive) VALUES (?, ?, ?, ?, ?)`, k.table),
			n.UserID, n.Name, n.Match, n.MatchingAlgorithm, boolInt(n.IsInsensitive))
	}
	if err != nil {
		return conflict(err)
	}
	n.ID, _ = res.LastInsertId()
	n.CreatedAt = Now()
	return nil
}

func (s *Store) updateNamed(ctx context.Context, k namedTable, n *Named) error {
	n.Name = strings.TrimSpace(n.Name)
	if n.Name == "" {
		return errors.New("a name is required")
	}
	var res sql.Result
	var err error
	if k == kindStoragePath {
		res, err = s.DB.ExecContext(ctx, `UPDATE storage_paths SET name = ?, path = ?, match = ?, matching_algorithm = ?, is_insensitive = ? WHERE id = ? AND user_id = ?`,
			n.Name, n.Path, n.Match, n.MatchingAlgorithm, boolInt(n.IsInsensitive), n.ID, n.UserID)
	} else {
		res, err = s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET name = ?, match = ?, matching_algorithm = ?, is_insensitive = ? WHERE id = ? AND user_id = ?`, k.table),
			n.Name, n.Match, n.MatchingAlgorithm, boolInt(n.IsInsensitive), n.ID, n.UserID)
	}
	if err != nil {
		return conflict(err)
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) deleteNamed(ctx context.Context, k namedTable, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ? AND user_id = ?`, k.table), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Correspondents, document types and storage paths: the public wrappers.

func (s *Store) ListCorrespondents(ctx context.Context, userID int64) ([]Named, error) {
	return s.listNamed(ctx, kindCorrespondent, userID)
}
func (s *Store) GetCorrespondent(ctx context.Context, userID, id int64) (*Named, error) {
	return s.getNamed(ctx, kindCorrespondent, userID, id)
}
func (s *Store) FindCorrespondent(ctx context.Context, userID int64, name string) (*Named, error) {
	return s.findNamed(ctx, kindCorrespondent, userID, name)
}
func (s *Store) CreateCorrespondent(ctx context.Context, n *Named) error {
	return s.createNamed(ctx, kindCorrespondent, n)
}
func (s *Store) UpdateCorrespondent(ctx context.Context, n *Named) error {
	return s.updateNamed(ctx, kindCorrespondent, n)
}
func (s *Store) DeleteCorrespondent(ctx context.Context, userID, id int64) error {
	return s.deleteNamed(ctx, kindCorrespondent, userID, id)
}

func (s *Store) ListDocumentTypes(ctx context.Context, userID int64) ([]Named, error) {
	return s.listNamed(ctx, kindDocumentType, userID)
}
func (s *Store) GetDocumentType(ctx context.Context, userID, id int64) (*Named, error) {
	return s.getNamed(ctx, kindDocumentType, userID, id)
}
func (s *Store) FindDocumentType(ctx context.Context, userID int64, name string) (*Named, error) {
	return s.findNamed(ctx, kindDocumentType, userID, name)
}
func (s *Store) CreateDocumentType(ctx context.Context, n *Named) error {
	return s.createNamed(ctx, kindDocumentType, n)
}
func (s *Store) UpdateDocumentType(ctx context.Context, n *Named) error {
	return s.updateNamed(ctx, kindDocumentType, n)
}
func (s *Store) DeleteDocumentType(ctx context.Context, userID, id int64) error {
	return s.deleteNamed(ctx, kindDocumentType, userID, id)
}

func (s *Store) ListStoragePaths(ctx context.Context, userID int64) ([]Named, error) {
	return s.listNamed(ctx, kindStoragePath, userID)
}
func (s *Store) GetStoragePath(ctx context.Context, userID, id int64) (*Named, error) {
	return s.getNamed(ctx, kindStoragePath, userID, id)
}
func (s *Store) FindStoragePath(ctx context.Context, userID int64, name string) (*Named, error) {
	return s.findNamed(ctx, kindStoragePath, userID, name)
}
func (s *Store) CreateStoragePath(ctx context.Context, n *Named) error {
	return s.createNamed(ctx, kindStoragePath, n)
}
func (s *Store) UpdateStoragePath(ctx context.Context, n *Named) error {
	return s.updateNamed(ctx, kindStoragePath, n)
}
func (s *Store) DeleteStoragePath(ctx context.Context, userID, id int64) error {
	return s.deleteNamed(ctx, kindStoragePath, userID, id)
}

// ---- custom fields ----

// FieldTypes are the data types a custom field may have.
var FieldTypes = []string{"string", "url", "date", "boolean", "integer", "float", "monetary", "select", "documentlink"}

func validFieldType(t string) bool {
	for _, x := range FieldTypes {
		if x == t {
			return true
		}
	}
	return false
}

// ListCustomFields lists a person's custom fields.
func (s *Store) ListCustomFields(ctx context.Context, userID int64) ([]CustomField, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT f.id, f.user_id, f.name, f.data_type, f.extra, f.position, f.created_at,
		(SELECT COUNT(1) FROM document_fields df JOIN documents d ON d.id = df.document_id WHERE df.field_id = f.id AND d.deleted_at IS NULL)
		FROM custom_fields f WHERE f.user_id = ? ORDER BY f.position, f.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CustomField{}
	for rows.Next() {
		var f CustomField
		var extra string
		if err := rows.Scan(&f.ID, &f.UserID, &f.Name, &f.DataType, &extra, &f.Position, &f.CreatedAt, &f.DocumentCount); err != nil {
			return nil, err
		}
		f.Extra = json.RawMessage(extra)
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetCustomField returns one field.
func (s *Store) GetCustomField(ctx context.Context, userID, id int64) (*CustomField, error) {
	var f CustomField
	var extra string
	err := s.DB.QueryRowContext(ctx, `SELECT id, user_id, name, data_type, extra, position, created_at FROM custom_fields WHERE id = ? AND user_id = ?`, id, userID).
		Scan(&f.ID, &f.UserID, &f.Name, &f.DataType, &extra, &f.Position, &f.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	f.Extra = json.RawMessage(extra)
	return &f, nil
}

// FindCustomField finds a field by name.
func (s *Store) FindCustomField(ctx context.Context, userID int64, name string) (*CustomField, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM custom_fields WHERE user_id = ? AND name = ? COLLATE NOCASE`, userID, strings.TrimSpace(name)).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.GetCustomField(ctx, userID, id)
}

func normalizeExtra(f *CustomField) {
	if len(f.Extra) == 0 || !json.Valid(f.Extra) {
		f.Extra = json.RawMessage("{}")
	}
}

// CreateCustomField inserts a field.
func (s *Store) CreateCustomField(ctx context.Context, f *CustomField) error {
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" {
		return errors.New("a field needs a name")
	}
	if !validFieldType(f.DataType) {
		return fmt.Errorf("unknown field type %q", f.DataType)
	}
	normalizeExtra(f)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO custom_fields (user_id, name, data_type, extra, position) VALUES (?, ?, ?, ?, ?)`, f.UserID, f.Name, f.DataType, string(f.Extra), f.Position)
	if err != nil {
		return conflict(err)
	}
	f.ID, _ = res.LastInsertId()
	f.CreatedAt = Now()
	return nil
}

// UpdateCustomField saves a field (its type cannot change once used).
func (s *Store) UpdateCustomField(ctx context.Context, f *CustomField) error {
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" {
		return errors.New("a field needs a name")
	}
	if !validFieldType(f.DataType) {
		return fmt.Errorf("unknown field type %q", f.DataType)
	}
	normalizeExtra(f)
	res, err := s.DB.ExecContext(ctx, `UPDATE custom_fields SET name = ?, data_type = ?, extra = ?, position = ? WHERE id = ? AND user_id = ?`, f.Name, f.DataType, string(f.Extra), f.Position, f.ID, f.UserID)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCustomField removes a field and its values.
func (s *Store) DeleteCustomField(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM custom_fields WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// NameMaps returns id → name for a person's tags, correspondents, types and
// paths, for rendering and indexing.
type NameMaps struct {
	Tags, Correspondents, Types, Paths map[int64]string
	Fields                             map[int64]CustomField
	PathTemplates                      map[int64]string
}

// Names loads the NameMaps of a person.
func (s *Store) Names(ctx context.Context, userID int64) NameMaps {
	m := NameMaps{Tags: map[int64]string{}, Correspondents: map[int64]string{}, Types: map[int64]string{}, Paths: map[int64]string{}, Fields: map[int64]CustomField{}, PathTemplates: map[int64]string{}}
	fill := func(table string, into map[int64]string) {
		rows, err := s.DB.QueryContext(ctx, `SELECT id, name FROM `+table+` WHERE user_id = ?`, userID)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var name string
			if rows.Scan(&id, &name) == nil {
				into[id] = name
			}
		}
	}
	fill("tags", m.Tags)
	fill("correspondents", m.Correspondents)
	fill("document_types", m.Types)
	fill("storage_paths", m.Paths)
	if rows, err := s.DB.QueryContext(ctx, `SELECT id, path FROM storage_paths WHERE user_id = ?`, userID); err == nil {
		for rows.Next() {
			var id int64
			var p string
			if rows.Scan(&id, &p) == nil {
				m.PathTemplates[id] = p
			}
		}
		rows.Close()
	}
	if fields, err := s.ListCustomFields(ctx, userID); err == nil {
		for _, f := range fields {
			m.Fields[f.ID] = f
		}
	}
	return m
}
