package store

import (
	"context"
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
