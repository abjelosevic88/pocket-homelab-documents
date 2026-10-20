package server

import (
	"net/http"
	"strconv"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Sharing with another account on the box: the owner grants view or edit
// on one document. The other person sees it under "Shared with me", opens,
// downloads and (with edit) changes its details and writes notes; nothing
// else of the owner's is visible, and only the owner can delete or share.

// docAccess returns a document the caller may see: their own ("owner") or
// one shared with them (level view | edit). ErrNotFound otherwise.
func (s *Server) docAccess(r *http.Request, id int64, withContent bool) (*store.Document, string, error) {
	ctx, uid := r.Context(), me(r)
	if d, err := s.store.GetDocument(ctx, uid, id, withContent); err == nil {
		d.Access = "owner"
		return d, "owner", nil
	}
	if linkAppFrom(ctx) != "" {
		return nil, "", store.ErrNotFound // linked apps act for the person on the person's documents only
	}
	return s.store.GetSharedDocument(ctx, uid, id, withContent)
}

// docForFile is a document the caller may at least view.
func (s *Server) docForFile(w http.ResponseWriter, r *http.Request) *store.Document {
	d, _, err := s.docAccess(r, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return nil
	}
	return d
}

// docForEdit is a document the caller may change.
func (s *Server) docForEdit(w http.ResponseWriter, r *http.Request, withContent bool) *store.Document {
	d, level, err := s.docAccess(r, idParam(r, "id"), withContent)
	if err != nil {
		s.fail(w, err, "document")
		return nil
	}
	if level == "view" {
		writeErr(w, http.StatusForbidden, "this document was shared with you to view, not to change")
		return nil
	}
	return d
}

func (s *Server) handleListDocShares(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	list, err := s.store.ListDocShares(ctx, d.ID)
	if err != nil {
		s.fail(w, err, "shares")
		return
	}
	out := []map[string]any{}
	for _, sh := range list {
		m := map[string]any{"user_id": sh.UserID, "level": sh.Level, "created_at": sh.CreatedAt}
		if u, err := s.store.GetUser(ctx, sh.UserID); err == nil {
			m["name"], m["email"] = u.Name, u.Email
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleShareWith(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	var in struct {
		UserID int64  `json:"user_id"`
		Level  string `json:"level"`
	}
	if err := decode(r, &in); err != nil || in.UserID == 0 {
		writeErr(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if in.UserID == uid {
		writeErr(w, http.StatusBadRequest, "that is you")
		return
	}
	if _, err := s.store.GetUser(ctx, in.UserID); err != nil {
		writeErr(w, http.StatusNotFound, "no such user")
		return
	}
	if err := s.store.ShareDocument(ctx, d.ID, in.UserID, uid, in.Level); err != nil {
		s.fail(w, err, "share")
		return
	}
	s.store.AddHistory(ctx, d.ID, uid, actorName(r), "shared_with", map[string]any{"user_id": in.UserID, "level": in.Level})
	s.publish(uid, "documents.shared", "document:"+strconv.FormatInt(d.ID, 10), map[string]any{"id": d.ID, "title": d.Title, "with_user_id": in.UserID, "level": in.Level})
	s.handleListDocShares(w, r)
}

func (s *Server) handleUnshareWith(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	other, _ := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if err := s.store.UnshareDocument(ctx, d.ID, other); err != nil {
		s.fail(w, err, "unshare")
		return
	}
	s.store.AddHistory(ctx, d.ID, uid, actorName(r), "unshared_with", map[string]any{"user_id": other})
	w.WriteHeader(http.StatusNoContent)
}

// handleSharedWithMe lists what other accounts shared with the caller.
func (s *Server) handleSharedWithMe(w http.ResponseWriter, r *http.Request) {
	docs, err := s.store.ListSharedWith(r.Context(), me(r), 500)
	if err != nil {
		s.fail(w, err, "shared")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": docs, "count": len(docs)})
}
