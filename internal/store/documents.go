package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DocFilter narrows ListDocuments. Everything is ANDed.
type DocFilter struct {
	UserID int64
	Query  string // free text; the parser already took the field filters out
	FTS    string // ready FTS5 expression (built by search.Build); "" = none

	Tags            []int64 // has every one of these
	AnyTags         []int64 // has at least one
	NotTags         []int64
	Correspondents  []int64
	NoCorrespondent bool
	Types           []int64
	NoType          bool
	Paths           []int64
	NoPath          bool
	ASN             *int64
	ASNFrom, ASNTo  *int64
	HasASN          *bool
	CreatedFrom     string // YYYY-MM-DD inclusive
	CreatedTo       string
	AddedFrom       string
	AddedTo         string
	Inbox           bool
	Untagged        bool
	Favorite        *bool
	Status          string
	Mime            string // prefix
	HasNotes        *bool
	Fields          map[int64]string // custom field id → value contains ("" = any value)
	NoField         []int64
	IDs             []int64
	TitleContains   string
	Trash           bool // the deleted ones instead of the live ones

	Sort   string // created | -created | added | title | asn | modified | size | pages | correspondent | type | rank
	Limit  int
	Offset int

	WithContent bool
	NoFacets    bool
}

// Facets count the documents of the filter per tag, correspondent, type and path.
type Facets struct {
	Tags            map[int64]int  `json:"tags"`
	Correspondents  map[int64]int  `json:"correspondents"`
	Types           map[int64]int  `json:"document_types"`
	Paths           map[int64]int  `json:"storage_paths"`
	Status          map[string]int `json:"status"`
	Untagged        int            `json:"untagged"`
	NoCorrespondent int            `json:"no_correspondent"`
	NoType          int            `json:"no_type"`
}

const docCols = `d.id, d.user_id, d.title, d.correspondent_id, d.document_type_id, d.storage_path_id, d.asn, d.created, d.added_at, d.modified_at,
	d.original_name, d.original_ext, d.mime, d.checksum, d.size, d.archive_checksum, d.archive_size, d.has_archive, d.has_thumb, d.page_count, d.language,
	d.status, d.status_message, d.source, d.favorite, d.content_edited, d.mirror_path, d.deleted_at,
	(SELECT COUNT(1) FROM notes n WHERE n.document_id = d.id),
	COALESCE((SELECT c.name FROM correspondents c WHERE c.id = d.correspondent_id), ''),
	COALESCE((SELECT t.name FROM document_types t WHERE t.id = d.document_type_id), '')`

func scanDoc(row interface{ Scan(...any) error }, content, snippet bool) (*Document, error) {
	var d Document
	var hasArchive, hasThumb, fav, edited int
	dest := []any{&d.ID, &d.UserID, &d.Title, &d.CorrespondentID, &d.DocumentTypeID, &d.StoragePathID, &d.ASN, &d.Created, &d.AddedAt, &d.ModifiedAt,
		&d.OriginalName, &d.OriginalExt, &d.Mime, &d.Checksum, &d.Size, &d.ArchiveChecksum, &d.ArchiveSize, &hasArchive, &hasThumb, &d.PageCount, &d.Language,
		&d.Status, &d.StatusMessage, &d.Source, &fav, &edited, &d.MirrorPath, &d.DeletedAt, &d.NotesCount, &d.CorrespondentName, &d.DocumentTypeName}
	if content {
		dest = append(dest, &d.Content)
	}
	if snippet {
		dest = append(dest, &d.Snippet, &d.Rank)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, notFound(err)
	}
	d.HasArchive, d.HasThumb, d.Favorite, d.ContentEdited = hasArchive == 1, hasThumb == 1, fav == 1, edited == 1
	d.Tags, d.Fields = []int64{}, []FieldValue{}
	return &d, nil
}

// GetDocument returns one of a person's documents (deleted ones too).
func (s *Store) GetDocument(ctx context.Context, userID, id int64, withContent bool) (*Document, error) {
	cols := docCols
	if withContent {
		cols += ", d.content"
	}
	d, err := scanDoc(s.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM documents d WHERE d.id = ? AND d.user_id = ?`, id, userID), withContent, false)
	if err != nil {
		return nil, err
	}
	s.fillRelations(ctx, []*Document{d})
	return d, nil
}

// GetDocumentAny returns a document by id regardless of owner (workers, shares).
func (s *Store) GetDocumentAny(ctx context.Context, id int64, withContent bool) (*Document, error) {
	cols := docCols
	if withContent {
		cols += ", d.content"
	}
	d, err := scanDoc(s.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM documents d WHERE d.id = ?`, id), withContent, false)
	if err != nil {
		return nil, err
	}
	s.fillRelations(ctx, []*Document{d})
	return d, nil
}

func (s *Store) fillRelations(ctx context.Context, docs []*Document) {
	if len(docs) == 0 {
		return
	}
	by := map[int64]*Document{}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		by[d.ID] = d
		ids = append(ids, strconv.FormatInt(d.ID, 10))
	}
	in := strings.Join(ids, ",")
	if rows, err := s.DB.QueryContext(ctx, `SELECT dt.document_id, dt.tag_id FROM document_tags dt JOIN tags t ON t.id = dt.tag_id WHERE dt.document_id IN (`+in+`) ORDER BY t.name COLLATE NOCASE`); err == nil {
		for rows.Next() {
			var did, tid int64
			if rows.Scan(&did, &tid) == nil {
				by[did].Tags = append(by[did].Tags, tid)
			}
		}
		rows.Close()
	}
	if rows, err := s.DB.QueryContext(ctx, `SELECT df.document_id, df.field_id, df.value FROM document_fields df JOIN custom_fields f ON f.id = df.field_id WHERE df.document_id IN (`+in+`) ORDER BY f.position, f.name COLLATE NOCASE`); err == nil {
		for rows.Next() {
			var did int64
			var fv FieldValue
			if rows.Scan(&did, &fv.FieldID, &fv.Value) == nil {
				by[did].Fields = append(by[did].Fields, fv)
			}
		}
		rows.Close()
	}
}

// CreateDocument inserts a document with its tags and fields. The caller has
// stored the file already.
func (s *Store) CreateDocument(ctx context.Context, d *Document) error {
	if strings.TrimSpace(d.Title) == "" {
		d.Title = strings.TrimSuffix(d.OriginalName, "."+d.OriginalExt)
		if d.Title == "" {
			d.Title = "Untitled"
		}
	}
	if d.Created == "" {
		d.Created = Today()
	}
	if d.Status == "" {
		d.Status = "pending"
	}
	if d.Source == "" {
		d.Source = "web"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO documents (user_id, title, content, correspondent_id, document_type_id, storage_path_id, asn, created, original_name, original_ext, mime, checksum, size, status, source, favorite, language)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.UserID, d.Title, d.Content, d.CorrespondentID, d.DocumentTypeID, d.StoragePathID, d.ASN, d.Created, d.OriginalName, d.OriginalExt, d.Mime, d.Checksum, d.Size, d.Status, d.Source, boolInt(d.Favorite), d.Language)
	if err != nil {
		return conflict(err)
	}
	d.ID, _ = res.LastInsertId()
	for _, t := range d.Tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO document_tags (document_id, tag_id) SELECT ?, id FROM tags WHERE id = ? AND user_id = ?`, d.ID, t, d.UserID); err != nil {
			return err
		}
	}
	for _, f := range d.Fields {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO document_fields (document_id, field_id, value) SELECT ?, id, ? FROM custom_fields WHERE id = ? AND user_id = ?`, d.ID, f.Value, f.FieldID, d.UserID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.AddedAt, d.ModifiedAt = Now(), Now()
	return s.IndexDocument(ctx, d.ID)
}

// DocChanges is what a save may change; nil pointers leave a column alone.
type DocChanges struct {
	Title           *string
	Content         *string
	CorrespondentID **int64
	DocumentTypeID  **int64
	StoragePathID   **int64
	ASN             **int64
	Created         *string
	Favorite        *bool
	Tags            *[]int64
	AddTags         []int64
	RemoveTags      []int64
	Fields          *[]FieldValue
	SetFields       []FieldValue
	RemoveFields    []int64
	Status          *string
	StatusMessage   *string
	Language        *string
}

// UpdateDocument applies changes and returns what actually changed as
// {field: [old, new]} for the history.
func (s *Store) UpdateDocument(ctx context.Context, userID, id int64, c DocChanges) (map[string][2]any, error) {
	cur, err := s.GetDocument(ctx, userID, id, c.Content != nil)
	if err != nil {
		return nil, err
	}
	changed := map[string][2]any{}
	set := []string{}
	args := []any{}
	str := func(col, name string, p *string, old string) {
		if p != nil && *p != old {
			set = append(set, col+" = ?")
			args = append(args, *p)
			changed[name] = [2]any{old, *p}
		}
	}
	ptr := func(col, name string, p **int64, old *int64) {
		if p == nil {
			return
		}
		ov, nv := int64(0), int64(0)
		if old != nil {
			ov = *old
		}
		if *p != nil {
			nv = **p
		}
		if ov != nv {
			set = append(set, col+" = ?")
			args = append(args, *p)
			changed[name] = [2]any{old, *p}
		}
	}
	str("title", "title", c.Title, cur.Title)
	str("created", "created", c.Created, cur.Created)
	str("status", "status", c.Status, cur.Status)
	str("status_message", "status_message", c.StatusMessage, cur.StatusMessage)
	str("language", "language", c.Language, cur.Language)
	if c.Content != nil && *c.Content != cur.Content {
		set = append(set, "content = ?", "content_edited = 1")
		args = append(args, *c.Content)
		changed["content"] = [2]any{len(cur.Content), len(*c.Content)}
	}
	ptr("correspondent_id", "correspondent", c.CorrespondentID, cur.CorrespondentID)
	ptr("document_type_id", "document_type", c.DocumentTypeID, cur.DocumentTypeID)
	ptr("storage_path_id", "storage_path", c.StoragePathID, cur.StoragePathID)
	ptr("asn", "asn", c.ASN, cur.ASN)
	if c.Favorite != nil && *c.Favorite != cur.Favorite {
		set = append(set, "favorite = ?")
		args = append(args, boolInt(*c.Favorite))
		changed["favorite"] = [2]any{cur.Favorite, *c.Favorite}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// tags
	want := map[int64]bool{}
	for _, t := range cur.Tags {
		want[t] = true
	}
	if c.Tags != nil {
		want = map[int64]bool{}
		for _, t := range *c.Tags {
			want[t] = true
		}
	}
	for _, t := range c.AddTags {
		want[t] = true
	}
	for _, t := range c.RemoveTags {
		delete(want, t)
	}
	newTags := []int64{}
	for t := range want {
		newTags = append(newTags, t)
	}
	sort.Slice(newTags, func(i, j int) bool { return newTags[i] < newTags[j] })
	oldTags := append([]int64{}, cur.Tags...)
	sort.Slice(oldTags, func(i, j int) bool { return oldTags[i] < oldTags[j] })
	if fmt.Sprint(oldTags) != fmt.Sprint(newTags) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM document_tags WHERE document_id = ?`, id); err != nil {
			return nil, err
		}
		for _, t := range newTags {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO document_tags (document_id, tag_id) SELECT ?, id FROM tags WHERE id = ? AND user_id = ?`, id, t, userID); err != nil {
				return nil, err
			}
		}
		changed["tags"] = [2]any{oldTags, newTags}
	}
	// fields
	if c.Fields != nil || len(c.SetFields) > 0 || len(c.RemoveFields) > 0 {
		vals := map[int64]string{}
		for _, f := range cur.Fields {
			vals[f.FieldID] = f.Value
		}
		if c.Fields != nil {
			vals = map[int64]string{}
			for _, f := range *c.Fields {
				vals[f.FieldID] = f.Value
			}
		}
		for _, f := range c.SetFields {
			vals[f.FieldID] = f.Value
		}
		for _, f := range c.RemoveFields {
			delete(vals, f)
		}
		old := map[int64]string{}
		for _, f := range cur.Fields {
			old[f.FieldID] = f.Value
		}
		if fmt.Sprint(old) != fmt.Sprint(vals) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM document_fields WHERE document_id = ?`, id); err != nil {
				return nil, err
			}
			for fid, v := range vals {
				if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO document_fields (document_id, field_id, value) SELECT ?, id, ? FROM custom_fields WHERE id = ? AND user_id = ?`, id, v, fid, userID); err != nil {
					return nil, err
				}
			}
			changed["custom_fields"] = [2]any{old, vals}
		}
	}
	if len(changed) == 0 {
		return changed, nil
	}
	set = append(set, "modified_at = ?")
	args = append(args, Now(), id, userID)
	if _, err := tx.ExecContext(ctx, `UPDATE documents SET `+strings.Join(set, ", ")+` WHERE id = ? AND user_id = ?`, args...); err != nil {
		return nil, conflict(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, s.IndexDocument(ctx, id)
}

// SetProcessed stores what the worker found. Content is kept when a person
// edited it unless force is set.
func (s *Store) SetProcessed(ctx context.Context, id int64, content string, pageCount int, hasArchive bool, archiveChecksum string, archiveSize int64, hasThumb bool, lang string, force bool) error {
	var edited int
	_ = s.DB.QueryRowContext(ctx, `SELECT content_edited FROM documents WHERE id = ?`, id).Scan(&edited)
	q := `UPDATE documents SET status = 'done', status_message = '', page_count = ?, has_archive = ?, archive_checksum = ?, archive_size = ?, has_thumb = ?, language = CASE WHEN ? = '' THEN language ELSE ? END, modified_at = ?`
	args := []any{pageCount, boolInt(hasArchive), archiveChecksum, archiveSize, boolInt(hasThumb), lang, lang, Now()}
	if edited == 0 || force {
		q += `, content = ?, content_edited = 0`
		args = append(args, content)
	}
	q += ` WHERE id = ?`
	args = append(args, id)
	if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
		return err
	}
	return s.IndexDocument(ctx, id)
}

// SetStatus moves a document between pending / processing / failed.
func (s *Store) SetStatus(ctx context.Context, id int64, status, msg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE documents SET status = ?, status_message = ? WHERE id = ?`, status, msg, id)
	return err
}

// SetMirrorPath remembers where the mirror folder holds a document.
func (s *Store) SetMirrorPath(ctx context.Context, id int64, p string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE documents SET mirror_path = ? WHERE id = ?`, p, id)
	return err
}

// SetTitleAndCreated is used by the worker for what it read from the paper.
func (s *Store) SetDetected(ctx context.Context, id int64, title, created string) error {
	set, args := []string{}, []any{}
	if title != "" {
		set = append(set, "title = ?")
		args = append(args, title)
	}
	if created != "" {
		set = append(set, "created = ?")
		args = append(args, created)
	}
	if len(set) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := s.DB.ExecContext(ctx, `UPDATE documents SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...)
	return err
}

// SoftDeleteDocument moves a document to the trash.
func (s *Store) SoftDeleteDocument(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE documents SET deleted_at = ?, modified_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, Now(), Now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM shares WHERE document_id = ?`, id)
	return s.IndexDocument(ctx, id)
}

// RestoreDocument brings a document back from the trash.
func (s *Store) RestoreDocument(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE documents SET deleted_at = NULL, modified_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NOT NULL`, Now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return s.IndexDocument(ctx, id)
}

// PurgeDocument deletes a document row for good (the caller removes files).
func (s *Store) PurgeDocument(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM documents WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM documents_fts WHERE rowid = ?`, id)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM documents_trigram WHERE rowid = ?`, id)
	return nil
}

// TrashOlderThan lists deleted documents past the retention.
func (s *Store) TrashOlderThan(ctx context.Context, days int) ([]Document, error) {
	if days <= 0 {
		return nil, nil
	}
	cut := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+docCols+` FROM documents d WHERE d.deleted_at IS NOT NULL AND d.deleted_at < ?`, cut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		d, err := scanDoc(rows, false, false)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// FindByChecksum finds a live document with the same original file.
func (s *Store) FindByChecksum(ctx context.Context, userID int64, sum string) (*Document, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM documents WHERE user_id = ? AND checksum = ? AND deleted_at IS NULL ORDER BY id LIMIT 1`, userID, sum).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.GetDocument(ctx, userID, id, false)
}

// NextASN is one more than the highest archive serial number in use.
func (s *Store) NextASN(ctx context.Context, userID int64) int64 {
	var n sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT MAX(asn) FROM documents WHERE user_id = ?`, userID).Scan(&n)
	return n.Int64 + 1
}

// ---- full text ----

// IndexDocument rebuilds the search rows of one document. Deleted documents
// are taken out of the index.
func (s *Store) IndexDocument(ctx context.Context, id int64) error {
	var userID int64
	var title, content, origName string
	var corr, typ, path sql.NullInt64
	var asn sql.NullInt64
	var deleted sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT user_id, title, content, original_name, correspondent_id, document_type_id, storage_path_id, asn, deleted_at FROM documents WHERE id = ?`, id).
		Scan(&userID, &title, &content, &origName, &corr, &typ, &path, &asn, &deleted)
	if err != nil {
		return notFound(err)
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM documents_fts WHERE rowid = ?`, id)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM documents_trigram WHERE rowid = ?`, id)
	if deleted.Valid {
		return nil
	}
	var extra []string
	extra = append(extra, origName)
	if asn.Valid {
		extra = append(extra, "asn"+strconv.FormatInt(asn.Int64, 10), strconv.FormatInt(asn.Int64, 10))
	}
	one := func(table string, idv sql.NullInt64) {
		if !idv.Valid {
			return
		}
		var name string
		if s.DB.QueryRowContext(ctx, `SELECT name FROM `+table+` WHERE id = ?`, idv.Int64).Scan(&name) == nil {
			extra = append(extra, name)
		}
	}
	one("correspondents", corr)
	one("document_types", typ)
	one("storage_paths", path)
	if rows, err := s.DB.QueryContext(ctx, `SELECT t.name FROM document_tags dt JOIN tags t ON t.id = dt.tag_id WHERE dt.document_id = ?`, id); err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				extra = append(extra, n)
			}
		}
		rows.Close()
	}
	if rows, err := s.DB.QueryContext(ctx, `SELECT f.name, df.value FROM document_fields df JOIN custom_fields f ON f.id = df.field_id WHERE df.document_id = ?`, id); err == nil {
		for rows.Next() {
			var n, v string
			if rows.Scan(&n, &v) == nil {
				extra = append(extra, n, v)
			}
		}
		rows.Close()
	}
	var notes []string
	if rows, err := s.DB.QueryContext(ctx, `SELECT text FROM notes WHERE document_id = ?`, id); err == nil {
		for rows.Next() {
			var t string
			if rows.Scan(&t) == nil {
				notes = append(notes, t)
			}
		}
		rows.Close()
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO documents_fts (rowid, title, content, extra, notes) VALUES (?, ?, ?, ?, ?)`, id, title, content, strings.Join(extra, "\n"), strings.Join(notes, "\n")); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO documents_trigram (rowid, title, content) VALUES (?, ?, ?)`, id, title, content)
	return err
}

// ReindexAll rebuilds the search index for every document of a person (0 = everyone).
func (s *Store) ReindexAll(ctx context.Context, userID int64) (int, error) {
	q, args := `SELECT id FROM documents`, []any{}
	if userID > 0 {
		q += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := s.IndexDocument(ctx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// ReindexRelated re-indexes every document that carries a tag, correspondent,
// type, path or field, after its name changed.
func (s *Store) ReindexRelated(ctx context.Context, col string, id int64) {
	var q string
	switch col {
	case "tag":
		q = `SELECT document_id FROM document_tags WHERE tag_id = ?`
	case "field":
		q = `SELECT document_id FROM document_fields WHERE field_id = ?`
	case "correspondent":
		q = `SELECT id FROM documents WHERE correspondent_id = ?`
	case "document_type":
		q = `SELECT id FROM documents WHERE document_type_id = ?`
	case "storage_path":
		q = `SELECT id FROM documents WHERE storage_path_id = ?`
	default:
		return
	}
	rows, err := s.DB.QueryContext(ctx, q, id)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var d int64
		if rows.Scan(&d) == nil {
			ids = append(ids, d)
		}
	}
	rows.Close()
	for _, d := range ids {
		_ = s.IndexDocument(ctx, d)
	}
}

// ---- listing ----

var sortCols = map[string]string{
	"created": "d.created", "added": "d.added_at", "modified": "d.modified_at", "title": "d.title COLLATE NOCASE", "asn": "d.asn",
	"size": "d.size", "pages": "d.page_count", "correspondent": "corr_name COLLATE NOCASE", "type": "type_name COLLATE NOCASE", "id": "d.id",
	"notes": "notes_count", "rank": "rank_",
}

func (f DocFilter) where() (string, []any, string, []any) {
	w := []string{"d.user_id = ?"}
	args := []any{f.UserID}
	if f.Trash {
		w = append(w, "d.deleted_at IS NOT NULL")
	} else {
		w = append(w, "d.deleted_at IS NULL")
	}
	inList := func(ids []int64) string {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = strconv.FormatInt(id, 10)
		}
		return strings.Join(parts, ",")
	}
	for _, t := range f.Tags {
		w = append(w, "EXISTS (SELECT 1 FROM document_tags x WHERE x.document_id = d.id AND x.tag_id = ?)")
		args = append(args, t)
	}
	if len(f.AnyTags) > 0 {
		w = append(w, "EXISTS (SELECT 1 FROM document_tags x WHERE x.document_id = d.id AND x.tag_id IN ("+inList(f.AnyTags)+"))")
	}
	for _, t := range f.NotTags {
		w = append(w, "NOT EXISTS (SELECT 1 FROM document_tags x WHERE x.document_id = d.id AND x.tag_id = ?)")
		args = append(args, t)
	}
	if len(f.Correspondents) > 0 {
		w = append(w, "d.correspondent_id IN ("+inList(f.Correspondents)+")")
	}
	if f.NoCorrespondent {
		w = append(w, "d.correspondent_id IS NULL")
	}
	if len(f.Types) > 0 {
		w = append(w, "d.document_type_id IN ("+inList(f.Types)+")")
	}
	if f.NoType {
		w = append(w, "d.document_type_id IS NULL")
	}
	if len(f.Paths) > 0 {
		w = append(w, "d.storage_path_id IN ("+inList(f.Paths)+")")
	}
	if f.NoPath {
		w = append(w, "d.storage_path_id IS NULL")
	}
	if f.ASN != nil {
		w = append(w, "d.asn = ?")
		args = append(args, *f.ASN)
	}
	if f.ASNFrom != nil {
		w = append(w, "d.asn >= ?")
		args = append(args, *f.ASNFrom)
	}
	if f.ASNTo != nil {
		w = append(w, "d.asn <= ?")
		args = append(args, *f.ASNTo)
	}
	if f.HasASN != nil {
		if *f.HasASN {
			w = append(w, "d.asn IS NOT NULL")
		} else {
			w = append(w, "d.asn IS NULL")
		}
	}
	if f.CreatedFrom != "" {
		w = append(w, "d.created >= ?")
		args = append(args, f.CreatedFrom)
	}
	if f.CreatedTo != "" {
		w = append(w, "d.created <= ?")
		args = append(args, f.CreatedTo)
	}
	if f.AddedFrom != "" {
		w = append(w, "d.added_at >= ?")
		args = append(args, f.AddedFrom)
	}
	if f.AddedTo != "" {
		w = append(w, "d.added_at <= ?")
		args = append(args, f.AddedTo+"T23:59:59.999Z")
	}
	if f.Inbox {
		w = append(w, "EXISTS (SELECT 1 FROM document_tags x JOIN tags t ON t.id = x.tag_id WHERE x.document_id = d.id AND t.is_inbox = 1)")
	}
	if f.Untagged {
		w = append(w, "NOT EXISTS (SELECT 1 FROM document_tags x WHERE x.document_id = d.id)")
	}
	if f.Favorite != nil {
		w = append(w, "d.favorite = ?")
		args = append(args, boolInt(*f.Favorite))
	}
	if f.Status != "" {
		w = append(w, "d.status = ?")
		args = append(args, f.Status)
	}
	if f.Mime != "" {
		w = append(w, "d.mime LIKE ?")
		args = append(args, f.Mime+"%")
	}
	if f.HasNotes != nil {
		if *f.HasNotes {
			w = append(w, "EXISTS (SELECT 1 FROM notes n WHERE n.document_id = d.id)")
		} else {
			w = append(w, "NOT EXISTS (SELECT 1 FROM notes n WHERE n.document_id = d.id)")
		}
	}
	for fid, v := range f.Fields {
		if v == "" {
			w = append(w, "EXISTS (SELECT 1 FROM document_fields x WHERE x.document_id = d.id AND x.field_id = ?)")
			args = append(args, fid)
		} else {
			w = append(w, "EXISTS (SELECT 1 FROM document_fields x WHERE x.document_id = d.id AND x.field_id = ? AND x.value LIKE ? ESCAPE '\\')")
			args = append(args, fid, LikePattern(v))
		}
	}
	for _, fid := range f.NoField {
		w = append(w, "NOT EXISTS (SELECT 1 FROM document_fields x WHERE x.document_id = d.id AND x.field_id = ?)")
		args = append(args, fid)
	}
	if len(f.IDs) > 0 {
		w = append(w, "d.id IN ("+inList(f.IDs)+")")
	}
	if f.TitleContains != "" {
		w = append(w, "d.title LIKE ? ESCAPE '\\'")
		args = append(args, LikePattern(f.TitleContains))
	}
	join, jargs := "", []any{}
	if f.FTS != "" {
		join = `JOIN documents_fts ON documents_fts.rowid = d.id AND documents_fts MATCH ?`
		jargs = append(jargs, f.FTS)
	}
	return strings.Join(w, " AND "), args, join, jargs
}

// ListDocuments returns a page of documents, the total and the facets.
func (s *Store) ListDocuments(ctx context.Context, f DocFilter) ([]Document, int, *Facets, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	docs, total, err := s.listOnce(ctx, f, false)
	if err != nil {
		return nil, 0, nil, err
	}
	trigram := false
	if total == 0 && f.FTS != "" && f.Query != "" && len([]rune(f.Query)) >= 3 {
		// nothing by words: try substrings
		docs, total, err = s.listOnce(ctx, f, true)
		if err != nil {
			return nil, 0, nil, err
		}
		trigram = true
	}
	var facets *Facets
	if !f.NoFacets {
		facets = s.facets(ctx, f, trigram)
	}
	return docs, total, facets, nil
}

func (s *Store) listOnce(ctx context.Context, f DocFilter, trigram bool) ([]Document, int, error) {
	where, args, join, jargs := f.where()
	rankExpr, snippetExpr := "0", "''"
	if f.FTS != "" {
		if trigram {
			join = `JOIN documents_trigram ON documents_trigram.rowid = d.id AND documents_trigram MATCH ?`
			jargs = []any{trigramQuery(f.Query)}
			rankExpr = "bm25(documents_trigram, 10, 1)"
			snippetExpr = "snippet(documents_trigram, 1, '[[[', ']]]', '…', 24)"
		} else {
			rankExpr = "bm25(documents_fts, 10, 1, 4, 2)"
			snippetExpr = "snippet(documents_fts, 1, '[[[', ']]]', '…', 24)"
		}
	}
	all := append(append([]any{}, jargs...), args...)
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM documents d `+join+` WHERE `+where, all...).Scan(&total); err != nil {
		return nil, 0, err
	}
	sortKey := strings.TrimPrefix(f.Sort, "-")
	desc := strings.HasPrefix(f.Sort, "-")
	if sortKey == "" {
		sortKey = "created"
		desc = true
	}
	if sortKey == "rank" && f.FTS == "" {
		sortKey, desc = "created", true
	}
	if f.FTS != "" && f.Sort == "" {
		sortKey, desc = "rank", false
	}
	col, ok := sortCols[sortKey]
	if !ok {
		col, desc = "d.created", true
	}
	order := col
	if desc {
		order += " DESC"
	}
	if sortKey != "id" {
		order += ", d.id DESC"
	}
	cols := docCols
	if f.WithContent {
		cols += ", d.content"
	}
	cols += ", " + snippetExpr + " AS snip, " + rankExpr + " AS rank_"
	q := `SELECT ` + cols + `, (SELECT COUNT(1) FROM notes n WHERE n.document_id = d.id) AS notes_count,
		COALESCE((SELECT c.name FROM correspondents c WHERE c.id = d.correspondent_id), '') AS corr_name,
		COALESCE((SELECT t.name FROM document_types t WHERE t.id = d.document_type_id), '') AS type_name
		FROM documents d ` + join + ` WHERE ` + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	all = append(all, f.Limit, f.Offset)
	rows, err := s.DB.QueryContext(ctx, q, all...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Document{}
	var ptrs []*Document
	for rows.Next() {
		d, err := scanDocWithTail(rows, f.WithContent)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *d)
	}
	for i := range out {
		ptrs = append(ptrs, &out[i])
	}
	s.fillRelations(ctx, ptrs)
	return out, total, rows.Err()
}

// scanDocWithTail scans docCols [+content] + snippet + rank + the three trailing sort helpers.
func scanDocWithTail(rows *sql.Rows, content bool) (*Document, error) {
	var d Document
	var hasArchive, hasThumb, fav, edited int
	var nc2 int
	var cn2, tn2 string
	dest := []any{&d.ID, &d.UserID, &d.Title, &d.CorrespondentID, &d.DocumentTypeID, &d.StoragePathID, &d.ASN, &d.Created, &d.AddedAt, &d.ModifiedAt,
		&d.OriginalName, &d.OriginalExt, &d.Mime, &d.Checksum, &d.Size, &d.ArchiveChecksum, &d.ArchiveSize, &hasArchive, &hasThumb, &d.PageCount, &d.Language,
		&d.Status, &d.StatusMessage, &d.Source, &fav, &edited, &d.MirrorPath, &d.DeletedAt, &d.NotesCount, &d.CorrespondentName, &d.DocumentTypeName}
	if content {
		dest = append(dest, &d.Content)
	}
	dest = append(dest, &d.Snippet, &d.Rank, &nc2, &cn2, &tn2)
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	d.HasArchive, d.HasThumb, d.Favorite, d.ContentEdited = hasArchive == 1, hasThumb == 1, fav == 1, edited == 1
	d.Tags, d.Fields = []int64{}, []FieldValue{}
	if d.Rank != 0 {
		d.Rank = -d.Rank // bm25 is negative: larger is better after the flip
	}
	return &d, nil
}

func (s *Store) facets(ctx context.Context, f DocFilter, trigram bool) *Facets {
	where, args, join, jargs := f.where()
	if trigram {
		join = `JOIN documents_trigram ON documents_trigram.rowid = d.id AND documents_trigram MATCH ?`
		jargs = []any{trigramQuery(f.Query)}
	}
	all := append(append([]any{}, jargs...), args...)
	fc := &Facets{Tags: map[int64]int{}, Correspondents: map[int64]int{}, Types: map[int64]int{}, Paths: map[int64]int{}, Status: map[string]int{}}
	base := `FROM documents d ` + join + ` WHERE ` + where
	count := func(q string, into map[int64]int) {
		rows, err := s.DB.QueryContext(ctx, q, all...)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id sql.NullInt64
			var n int
			if rows.Scan(&id, &n) == nil && id.Valid {
				into[id.Int64] = n
			}
		}
	}
	count(`SELECT x.tag_id, COUNT(1) FROM document_tags x WHERE x.document_id IN (SELECT d.id `+base+`) GROUP BY x.tag_id`, fc.Tags)
	count(`SELECT d.correspondent_id, COUNT(1) `+base+` GROUP BY d.correspondent_id`, fc.Correspondents)
	count(`SELECT d.document_type_id, COUNT(1) `+base+` GROUP BY d.document_type_id`, fc.Types)
	count(`SELECT d.storage_path_id, COUNT(1) `+base+` GROUP BY d.storage_path_id`, fc.Paths)
	if rows, err := s.DB.QueryContext(ctx, `SELECT d.status, COUNT(1) `+base+` GROUP BY d.status`, all...); err == nil {
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) == nil {
				fc.Status[st] = n
			}
		}
		rows.Close()
	}
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) `+base+` AND NOT EXISTS (SELECT 1 FROM document_tags x WHERE x.document_id = d.id)`, all...).Scan(&fc.Untagged)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) `+base+` AND d.correspondent_id IS NULL`, all...).Scan(&fc.NoCorrespondent)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) `+base+` AND d.document_type_id IS NULL`, all...).Scan(&fc.NoType)
	return fc
}

// trigramQuery quotes the free text for the trigram table (a phrase match).
func trigramQuery(q string) string {
	q = strings.ReplaceAll(strings.TrimSpace(q), `"`, " ")
	return `"` + q + `"`
}

// Similar finds documents sharing the most telling words with one document.
func (s *Store) Similar(ctx context.Context, userID, id int64, limit int) ([]Document, error) {
	d, err := s.GetDocument(ctx, userID, id, true)
	if err != nil {
		return nil, err
	}
	terms := TopTerms(d.Title+"\n"+d.Content, 14)
	if len(terms) == 0 {
		return []Document{}, nil
	}
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = `"` + strings.ReplaceAll(t, `"`, "") + `"`
	}
	f := DocFilter{UserID: userID, FTS: strings.Join(parts, " OR "), Query: strings.Join(terms, " "), Limit: limit + 1, NoFacets: true, Sort: "rank"}
	docs, _, err := s.listOnce(ctx, f, false)
	if err != nil {
		return nil, err
	}
	out := []Document{}
	for _, x := range docs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---- notes ----

// ListNotes lists the notes on a document, newest first.
func (s *Store) ListNotes(ctx context.Context, documentID int64) ([]Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, document_id, user_id, text, created_at FROM notes WHERE document_id = ? ORDER BY id DESC`, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.DocumentID, &n.UserID, &n.Text, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddNote writes a note.
func (s *Store) AddNote(ctx context.Context, n *Note) error {
	n.Text = strings.TrimSpace(n.Text)
	if n.Text == "" {
		return errors.New("an empty note")
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO notes (document_id, user_id, text) VALUES (?, ?, ?)`, n.DocumentID, n.UserID, n.Text)
	if err != nil {
		return err
	}
	n.ID, _ = res.LastInsertId()
	n.CreatedAt = Now()
	_, _ = s.DB.ExecContext(ctx, `UPDATE documents SET modified_at = ? WHERE id = ?`, Now(), n.DocumentID)
	return s.IndexDocument(ctx, n.DocumentID)
}

// DeleteNote removes a note from a document.
func (s *Store) DeleteNote(ctx context.Context, documentID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM notes WHERE id = ? AND document_id = ?`, id, documentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return s.IndexDocument(ctx, documentID)
}

// ---- history ----

// AddHistory records what happened to a document.
func (s *Store) AddHistory(ctx context.Context, documentID, userID int64, actor, action string, changes any) {
	b := []byte("{}")
	if changes != nil {
		if j, err := json.Marshal(changes); err == nil {
			b = j
		}
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO history (document_id, user_id, actor, action, changes) VALUES (?, ?, ?, ?, ?)`, documentID, userID, actor, action, string(b))
}

// ListHistory lists a document's history, newest first.
func (s *Store) ListHistory(ctx context.Context, documentID int64, limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, document_id, actor, action, changes, created_at FROM history WHERE document_id = ? ORDER BY id DESC LIMIT ?`, documentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var ch string
		if err := rows.Scan(&h.ID, &h.DocumentID, &h.Actor, &h.Action, &ch, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.Changes = json.RawMessage(ch)
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---- training data / statistics ----

// TrainingDoc is what the classifier learns from.
type TrainingDoc struct {
	ID              int64
	Text            string
	CorrespondentID int64
	DocumentTypeID  int64
	StoragePathID   int64
	Tags            []int64
}

// TrainingDocs lists a person's processed documents with their labels.
func (s *Store) TrainingDocs(ctx context.Context, userID int64) ([]TrainingDoc, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT d.id, d.title, substr(d.content, 1, 20000), COALESCE(d.correspondent_id, 0), COALESCE(d.document_type_id, 0), COALESCE(d.storage_path_id, 0)
		FROM documents d WHERE d.user_id = ? AND d.deleted_at IS NULL AND d.status = 'done'`, userID)
	if err != nil {
		return nil, err
	}
	var out []TrainingDoc
	by := map[int64]int{}
	for rows.Next() {
		var t TrainingDoc
		var title, content string
		if err := rows.Scan(&t.ID, &title, &content, &t.CorrespondentID, &t.DocumentTypeID, &t.StoragePathID); err != nil {
			rows.Close()
			return nil, err
		}
		t.Text = title + "\n" + content
		by[t.ID] = len(out)
		out = append(out, t)
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}
	trows, err := s.DB.QueryContext(ctx, `SELECT dt.document_id, dt.tag_id FROM document_tags dt JOIN documents d ON d.id = dt.document_id WHERE d.user_id = ? AND d.deleted_at IS NULL AND d.status = 'done'`, userID)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var did, tid int64
		if trows.Scan(&did, &tid) == nil {
			if i, ok := by[did]; ok {
				out[i].Tags = append(out[i].Tags, tid)
			}
		}
	}
	return out, nil
}

// ChangeStamp changes whenever a person's documents or queue do.
func (s *Store) ChangeStamp(ctx context.Context, userID int64) string {
	var a, b string
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(modified_at), ''), COUNT(1) FROM documents WHERE user_id = ?`, userID).Scan(&a, &n)
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(COALESCE(finished_at, started_at, created_at)), '') FROM jobs WHERE user_id = ?`, userID).Scan(&b)
	return a + "|" + strconv.Itoa(n) + "|" + b
}

// Stats summarise a person's archive.
type Stats struct {
	Documents       int          `json:"documents"`
	Inbox           int          `json:"inbox"`
	Untagged        int          `json:"untagged"`
	Pending         int          `json:"pending"`
	Failed          int          `json:"failed"`
	Favorites       int          `json:"favorites"`
	Trash           int          `json:"trash"`
	AddedToday      int          `json:"added_today"`
	AddedWeek       int          `json:"added_week"`
	AddedMonth      int          `json:"added_month"`
	Pages           int          `json:"pages"`
	Bytes           int64        `json:"bytes"`
	ArchiveBytes    int64        `json:"archive_bytes"`
	Characters      int64        `json:"characters"`
	Tags            int          `json:"tags"`
	Correspondents  int          `json:"correspondents"`
	Types           int          `json:"document_types"`
	ByMonth         []MonthCount `json:"by_month"`
	ByType          []NameCount  `json:"by_type"`
	ByCorrespondent []NameCount  `json:"by_correspondent"`
	ByTag           []NameCount  `json:"by_tag"`
	ByMime          []NameCount  `json:"by_mime"`
	Oldest          string       `json:"oldest"`
	Newest          string       `json:"newest"`
}

type MonthCount struct {
	Month string `json:"month"`
	Count int    `json:"count"`
}

type NameCount struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	Color string `json:"color,omitempty"`
}

// Stats computes the dashboard numbers for a person.
func (s *Store) Stats(ctx context.Context, userID int64) Stats {
	var st Stats
	live := `FROM documents d WHERE d.user_id = ? AND d.deleted_at IS NULL`
	now := time.Now()
	today := now.Format("2006-01-02")
	week := now.AddDate(0, 0, -7).UTC().Format(time.RFC3339)
	month := now.AddDate(0, 0, -30).UTC().Format(time.RFC3339)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(d.page_count), 0), COALESCE(SUM(d.size), 0), COALESCE(SUM(d.archive_size), 0), COALESCE(SUM(length(d.content)), 0),
		SUM(d.status = 'pending' OR d.status = 'processing'), SUM(d.status = 'failed'), SUM(d.favorite), SUM(d.added_at >= ?), SUM(d.added_at >= ?), SUM(d.added_at >= ?),
		COALESCE(MIN(d.created), ''), COALESCE(MAX(d.created), '') `+live, today, week, month, userID).
		Scan(&st.Documents, &st.Pages, &st.Bytes, &st.ArchiveBytes, &st.Characters, &st.Pending, &st.Failed, &st.Favorites, &st.AddedToday, &st.AddedWeek, &st.AddedMonth, &st.Oldest, &st.Newest)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) `+live+` AND EXISTS (SELECT 1 FROM document_tags x JOIN tags t ON t.id = x.tag_id WHERE x.document_id = d.id AND t.is_inbox = 1)`, userID).Scan(&st.Inbox)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) `+live+` AND NOT EXISTS (SELECT 1 FROM document_tags x WHERE x.document_id = d.id)`, userID).Scan(&st.Untagged)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM documents WHERE user_id = ? AND deleted_at IS NOT NULL`, userID).Scan(&st.Trash)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tags WHERE user_id = ?`, userID).Scan(&st.Tags)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM correspondents WHERE user_id = ?`, userID).Scan(&st.Correspondents)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM document_types WHERE user_id = ?`, userID).Scan(&st.Types)
	st.ByMonth = []MonthCount{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT substr(d.created, 1, 7), COUNT(1) `+live+` AND d.created >= ? GROUP BY 1 ORDER BY 1`, userID, now.AddDate(-1, 0, 0).Format("2006-01")); err == nil {
		for rows.Next() {
			var m MonthCount
			if rows.Scan(&m.Month, &m.Count) == nil {
				st.ByMonth = append(st.ByMonth, m)
			}
		}
		rows.Close()
	}
	top := func(q string) []NameCount {
		out := []NameCount{}
		rows, err := s.DB.QueryContext(ctx, q, userID)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var n NameCount
			if rows.Scan(&n.ID, &n.Name, &n.Count, &n.Color) == nil {
				out = append(out, n)
			}
		}
		return out
	}
	st.ByType = top(`SELECT t.id, t.name, COUNT(d.id), '' FROM document_types t LEFT JOIN documents d ON d.document_type_id = t.id AND d.deleted_at IS NULL WHERE t.user_id = ? GROUP BY t.id ORDER BY 3 DESC, t.name LIMIT 8`)
	st.ByCorrespondent = top(`SELECT c.id, c.name, COUNT(d.id), '' FROM correspondents c LEFT JOIN documents d ON d.correspondent_id = c.id AND d.deleted_at IS NULL WHERE c.user_id = ? GROUP BY c.id ORDER BY 3 DESC, c.name LIMIT 8`)
	st.ByTag = top(`SELECT t.id, t.name, COUNT(d.id), t.color FROM tags t LEFT JOIN document_tags x ON x.tag_id = t.id LEFT JOIN documents d ON d.id = x.document_id AND d.deleted_at IS NULL WHERE t.user_id = ? GROUP BY t.id ORDER BY 3 DESC, t.name LIMIT 10`)
	st.ByMime = top(`SELECT 0, d.mime, COUNT(1), '' FROM documents d WHERE d.user_id = ? AND d.deleted_at IS NULL GROUP BY d.mime ORDER BY 3 DESC LIMIT 8`)
	return st
}

// DocumentIDs lists the ids of a person's live documents (for exports).
func (s *Store) DocumentIDs(ctx context.Context, userID int64, trash bool) []int64 {
	q := `SELECT id FROM documents WHERE user_id = ? AND deleted_at IS NULL ORDER BY id`
	if trash {
		q = `SELECT id FROM documents WHERE user_id = ? AND deleted_at IS NOT NULL ORDER BY id`
	}
	rows, err := s.DB.QueryContext(ctx, q, userID)
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

// Neighbours returns the ids before and after a document in the created
// order, for the previous / next buttons.
func (s *Store) Neighbours(ctx context.Context, userID, id int64) (prev, next int64) {
	var created string
	if s.DB.QueryRowContext(ctx, `SELECT created FROM documents WHERE id = ? AND user_id = ?`, id, userID).Scan(&created) != nil {
		return 0, 0
	}
	_ = s.DB.QueryRowContext(ctx, `SELECT id FROM documents WHERE user_id = ? AND deleted_at IS NULL AND (created > ? OR (created = ? AND id > ?)) ORDER BY created, id LIMIT 1`, userID, created, created, id).Scan(&prev)
	_ = s.DB.QueryRowContext(ctx, `SELECT id FROM documents WHERE user_id = ? AND deleted_at IS NULL AND (created < ? OR (created = ? AND id < ?)) ORDER BY created DESC, id DESC LIMIT 1`, userID, created, created, id).Scan(&next)
	return prev, next
}
