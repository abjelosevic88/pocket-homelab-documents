package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// One set of handlers for tags, correspondents, document types, storage
// paths and custom fields: list, create, update, delete, all per person.

type taxonomyIn struct {
	Name              string          `json:"name"`
	Color             string          `json:"color"`
	Path              string          `json:"path"`
	Match             string          `json:"match"`
	MatchingAlgorithm int             `json:"matching_algorithm"`
	IsInsensitive     *bool           `json:"is_insensitive"`
	IsInbox           bool            `json:"is_inbox"`
	DataType          string          `json:"data_type"`
	Extra             json.RawMessage `json:"extra"`
	Position          int             `json:"position"`
}

func (in taxonomyIn) matchable() store.Matchable {
	m := store.Matchable{Match: strings.TrimSpace(in.Match), MatchingAlgorithm: in.MatchingAlgorithm, IsInsensitive: true}
	if in.IsInsensitive != nil {
		m.IsInsensitive = *in.IsInsensitive
	}
	if m.MatchingAlgorithm < 0 || m.MatchingAlgorithm > 6 {
		m.MatchingAlgorithm = store.MatchAuto
	}
	return m
}

func (s *Server) taxonomyList(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, uid := r.Context(), me(r)
		var out any
		var err error
		switch kind {
		case "tag":
			out, err = s.store.ListTags(ctx, uid)
		case "correspondent":
			out, err = s.store.ListCorrespondents(ctx, uid)
		case "document_type":
			out, err = s.store.ListDocumentTypes(ctx, uid)
		case "storage_path":
			out, err = s.store.ListStoragePaths(ctx, uid)
		case "custom_field":
			out, err = s.store.ListCustomFields(ctx, uid)
		}
		if err != nil {
			s.fail(w, err, "list "+kind)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) taxonomyCreate(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, uid := r.Context(), me(r)
		var in taxonomyIn
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		var out any
		var err error
		switch kind {
		case "tag":
			t := &store.Tag{UserID: uid, Name: in.Name, Color: validColor(in.Color), Matchable: in.matchable(), IsInbox: in.IsInbox}
			err, out = s.store.CreateTag(ctx, t), t
		case "correspondent":
			n := &store.Named{UserID: uid, Name: in.Name, Matchable: in.matchable()}
			err, out = s.store.CreateCorrespondent(ctx, n), n
		case "document_type":
			n := &store.Named{UserID: uid, Name: in.Name, Matchable: in.matchable()}
			err, out = s.store.CreateDocumentType(ctx, n), n
		case "storage_path":
			n := &store.Named{UserID: uid, Name: in.Name, Path: strings.TrimSpace(in.Path), Matchable: in.matchable()}
			err, out = s.store.CreateStoragePath(ctx, n), n
		case "custom_field":
			f := &store.CustomField{UserID: uid, Name: in.Name, DataType: firstNonEmpty(in.DataType, "string"), Extra: in.Extra, Position: in.Position}
			err, out = s.store.CreateCustomField(ctx, f), f
		}
		if err != nil {
			if strings.Contains(err.Error(), "needs a name") || strings.Contains(err.Error(), "is required") || strings.Contains(err.Error(), "unknown field type") {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			s.fail(w, err, "create "+kind)
			return
		}
		s.classifiers.Invalidate(uid)
		writeJSON(w, http.StatusCreated, out)
	}
}

func (s *Server) taxonomyUpdate(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, uid, id := r.Context(), me(r), idParam(r, "id")
		var in taxonomyIn
		keys, err := decodeKeys(r, &in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		var out any
		switch kind {
		case "tag":
			t, gerr := s.store.GetTag(ctx, uid, id)
			if gerr != nil {
				s.fail(w, gerr, "tag")
				return
			}
			if keys["name"] {
				t.Name = in.Name
			}
			if keys["color"] {
				t.Color = validColor(in.Color)
			}
			if keys["match"] || keys["matching_algorithm"] || keys["is_insensitive"] {
				m := in.matchable()
				if !keys["match"] {
					m.Match = t.Match
				}
				if !keys["matching_algorithm"] {
					m.MatchingAlgorithm = t.MatchingAlgorithm
				}
				if !keys["is_insensitive"] {
					m.IsInsensitive = t.IsInsensitive
				}
				t.Matchable = m
			}
			if keys["is_inbox"] {
				t.IsInbox = in.IsInbox
			}
			err, out = s.store.UpdateTag(ctx, t), t
		case "correspondent", "document_type", "storage_path":
			var n *store.Named
			var gerr error
			switch kind {
			case "correspondent":
				n, gerr = s.store.GetCorrespondent(ctx, uid, id)
			case "document_type":
				n, gerr = s.store.GetDocumentType(ctx, uid, id)
			default:
				n, gerr = s.store.GetStoragePath(ctx, uid, id)
			}
			if gerr != nil {
				s.fail(w, gerr, kind)
				return
			}
			if keys["name"] {
				n.Name = in.Name
			}
			if keys["path"] {
				n.Path = strings.TrimSpace(in.Path)
			}
			if keys["match"] || keys["matching_algorithm"] || keys["is_insensitive"] {
				m := in.matchable()
				if !keys["match"] {
					m.Match = n.Match
				}
				if !keys["matching_algorithm"] {
					m.MatchingAlgorithm = n.MatchingAlgorithm
				}
				if !keys["is_insensitive"] {
					m.IsInsensitive = n.IsInsensitive
				}
				n.Matchable = m
			}
			switch kind {
			case "correspondent":
				err = s.store.UpdateCorrespondent(ctx, n)
			case "document_type":
				err = s.store.UpdateDocumentType(ctx, n)
			default:
				err = s.store.UpdateStoragePath(ctx, n)
				if err == nil && keys["path"] {
					go s.remirrorPath(uid, id)
				}
			}
			out = n
		case "custom_field":
			f, gerr := s.store.GetCustomField(ctx, uid, id)
			if gerr != nil {
				s.fail(w, gerr, "field")
				return
			}
			if keys["name"] {
				f.Name = in.Name
			}
			if keys["data_type"] && in.DataType != "" && f.DocumentCount == 0 {
				f.DataType = in.DataType
			}
			if keys["extra"] {
				f.Extra = in.Extra
			}
			if keys["position"] {
				f.Position = in.Position
			}
			err, out = s.store.UpdateCustomField(ctx, f), f
		}
		if err != nil {
			if strings.Contains(err.Error(), "needs a name") || strings.Contains(err.Error(), "is required") || strings.Contains(err.Error(), "unknown field type") {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			s.fail(w, err, "update "+kind)
			return
		}
		if keys["name"] {
			col := map[string]string{"tag": "tag", "correspondent": "correspondent", "document_type": "document_type", "storage_path": "storage_path", "custom_field": "field"}[kind]
			go s.store.ReindexRelated(contextBG(), col, id)
		}
		s.classifiers.Invalidate(uid)
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) taxonomyDelete(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, uid, id := r.Context(), me(r), idParam(r, "id")
		var err error
		col := ""
		var ids []int64
		switch kind {
		case "tag":
			ids = s.relatedIDs(ctx, "tag", id)
			err, col = s.store.DeleteTag(ctx, uid, id), "tag"
		case "correspondent":
			ids = s.relatedIDs(ctx, "correspondent", id)
			err = s.store.DeleteCorrespondent(ctx, uid, id)
		case "document_type":
			ids = s.relatedIDs(ctx, "document_type", id)
			err = s.store.DeleteDocumentType(ctx, uid, id)
		case "storage_path":
			ids = s.relatedIDs(ctx, "storage_path", id)
			err = s.store.DeleteStoragePath(ctx, uid, id)
		case "custom_field":
			ids = s.relatedIDs(ctx, "field", id)
			err = s.store.DeleteCustomField(ctx, uid, id)
		}
		_ = col
		if err != nil {
			s.fail(w, err, "delete "+kind)
			return
		}
		for _, did := range ids {
			_ = s.store.IndexDocument(ctx, did)
		}
		s.classifiers.Invalidate(uid)
		w.WriteHeader(http.StatusNoContent)
	}
}

// relatedIDs lists the documents carrying a taxonomy item (before it goes).
func (s *Server) relatedIDs(ctx contextT, col string, id int64) []int64 {
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
	}
	rows, err := s.store.DB.QueryContext(ctx, q, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var d int64
		if rows.Scan(&d) == nil {
			out = append(out, d)
		}
	}
	return out
}

// remirrorPath moves every document of a storage path in the documents folder
// after its template changed.
func (s *Server) remirrorPath(uid, pathID int64) {
	ctx := contextBG()
	for _, id := range s.relatedIDs(ctx, "storage_path", pathID) {
		if d, err := s.store.GetDocument(ctx, uid, id, false); err == nil {
			_ = s.placeInFolder(ctx, d)
		}
	}
}

// handleTaxonomy returns everything a person's forms need in one round trip.
func (s *Server) handleTaxonomy(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	tags, _ := s.store.ListTags(ctx, uid)
	corr, _ := s.store.ListCorrespondents(ctx, uid)
	types, _ := s.store.ListDocumentTypes(ctx, uid)
	paths, _ := s.store.ListStoragePaths(ctx, uid)
	fields, _ := s.store.ListCustomFields(ctx, uid)
	views, _ := s.store.ListSavedViews(ctx, uid)
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags, "correspondents": corr, "document_types": types, "storage_paths": paths, "custom_fields": fields, "saved_views": views})
}

var tagColors = []string{"#a6cee3", "#1f78b4", "#b2df8a", "#33a02c", "#fb9a99", "#e31a1c", "#fdbf6f", "#ff7f00", "#cab2d6", "#6a3d9a", "#b15928", "#000000", "#cccccc"}

func validColor(c string) string {
	c = strings.TrimSpace(strings.ToLower(c))
	if len(c) == 7 && c[0] == '#' {
		ok := true
		for _, ch := range c[1:] {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
				ok = false
			}
		}
		if ok {
			return c
		}
	}
	if c == "" {
		return tagColors[0]
	}
	return tagColors[0]
}
