package server

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// ---- saved views ----

func (s *Server) handleListViews(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.ListSavedViews(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "views")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleCreateView(w http.ResponseWriter, r *http.Request) {
	var v store.SavedView
	if err := decode(r, &v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	v.UserID = me(r)
	if err := s.store.CreateSavedView(r.Context(), &v); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) handleUpdateView(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	v, err := s.store.GetSavedView(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "view")
		return
	}
	var in store.SavedView
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["name"] {
		v.Name = in.Name
	}
	if keys["query"] {
		v.Query = in.Query
	}
	if keys["on_dashboard"] {
		v.OnDashboard = in.OnDashboard
	}
	if keys["in_sidebar"] {
		v.InSidebar = in.InSidebar
	}
	if keys["position"] {
		v.Position = in.Position
	}
	if err := s.store.UpdateSavedView(ctx, v); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleDeleteView(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSavedView(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete view")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- rules ----

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListRules(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "rules")
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var in store.Rule
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.UserID = me(r)
	if err := s.store.CreateRule(r.Context(), &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	cur, err := s.store.GetRule(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "rule")
		return
	}
	in := *cur
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.ID, in.UserID = cur.ID, uid
	if err := s.store.UpdateRule(ctx, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Trigger == "scheduled" && (in.Filters.ScheduleField != cur.Filters.ScheduleField || in.Filters.ScheduleOffsetDays != cur.Filters.ScheduleOffsetDays) {
		s.store.ResetRuleRuns(ctx, in.ID)
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRule(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete rule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunRule applies a rule to every existing document, in the background.
func (s *Server) handleRunRule(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	rule, err := s.store.GetRule(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "rule")
		return
	}
	j, err := s.store.EnqueueJob(ctx, uid, nil, "run_rule", map[string]any{"rule_id": rule.ID})
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	s.nudge()
	writeJSON(w, http.StatusAccepted, j)
}

// ---- jobs ----

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs(r.Context(), me(r), r.URL.Query().Get("status"), clampInt(queryInt(r, "limit"), 0, 500))
	if err != nil {
		s.fail(w, err, "jobs")
		return
	}
	q, run, failed := s.store.JobCounts(r.Context(), me(r))
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "queued": q, "running": run, "failed": failed})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.store.GetJob(r.Context(), idParam(r, "id"))
	if err != nil || j.UserID != me(r) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RetryJob(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "retry job")
		return
	}
	s.nudge()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if err := s.store.CancelJob(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "cancel job")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClearJobs(w http.ResponseWriter, r *http.Request) {
	s.store.ClearJobs(r.Context(), me(r))
	w.WriteHeader(http.StatusNoContent)
}

// ---- shares ----

func (s *Server) handleDocumentShares(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	list, err := s.store.ListShares(r.Context(), me(r), d.ID)
	if err != nil {
		s.fail(w, err, "shares")
		return
	}
	writeJSON(w, http.StatusOK, s.withShareURLs(list))
}

func (s *Server) withShareURLs(list []store.Share) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, sh := range list {
		b, _ := json.Marshal(sh)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["url"] = s.cfg.BaseURL + "/s/" + sh.Token
		out = append(out, m)
	}
	return out
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	var in struct {
		Days int    `json:"days"`
		File string `json:"file"`
	}
	_ = decode(r, &in)
	sh := &store.Share{UserID: me(r), DocumentID: d.ID, File: in.File}
	if in.Days > 0 {
		e := time.Now().AddDate(0, 0, in.Days).UTC().Format(time.RFC3339)
		sh.ExpiresAt = &e
	}
	if err := s.store.CreateShare(r.Context(), sh); err != nil {
		s.fail(w, err, "share")
		return
	}
	s.store.AddHistory(r.Context(), d.ID, me(r), actorName(r), "shared", map[string]any{"days": in.Days, "file": sh.File})
	out := s.withShareURLs([]store.Share{*sh})[0]
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListShares(r.Context(), me(r), 0)
	if err != nil {
		s.fail(w, err, "shares")
		return
	}
	writeJSON(w, http.StatusOK, s.withShareURLs(list))
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteShare(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete share")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shareDoc resolves /s/{token} to its document.
func (s *Server) shareDoc(w http.ResponseWriter, r *http.Request) (*store.Share, *store.Document) {
	sh, err := s.store.ShareByToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		http.Error(w, "This link does not exist or has expired.", http.StatusNotFound)
		return nil, nil
	}
	d, err := s.store.GetDocumentAny(r.Context(), sh.DocumentID, false)
	if err != nil || d.DeletedAt != nil {
		http.Error(w, "This link does not exist or has expired.", http.StatusNotFound)
		return nil, nil
	}
	return sh, d
}

func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	sh, d := s.shareDoc(w, r)
	if d == nil {
		return
	}
	// A PDF (or the original) opens in the browser; images and pages get a tiny page.
	base := "/s/" + sh.Token
	pages := d.PageCount
	if pages == 0 {
		pages = 1
	}
	var imgs strings.Builder
	if d.HasArchive || strings.EqualFold(d.OriginalExt, "pdf") {
		for i := 1; i <= pages && i <= 200; i++ {
			fmt.Fprintf(&imgs, `<img src="%s/pages/%d.jpg" loading="lazy" alt="Page %d">`, base, i, i)
		}
	} else if strings.HasPrefix(d.Mime, "image/") {
		fmt.Fprintf(&imgs, `<img src="%s/file" alt="">`, base)
	} else {
		imgs.WriteString(`<p class="muted">No preview for this file type.</p>`)
	}
	title := html.EscapeString(d.Title)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>%s</title>
<style>body{margin:0;font:14px/1.5 system-ui,sans-serif;background:#2b2e34;color:#e6e8ec}header{position:sticky;top:0;display:flex;align-items:center;gap:12px;padding:10px 16px;background:#1b1d22;border-bottom:1px solid #3a3f49}header h1{font-size:15px;margin:0;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}a.btn{color:#fff;background:#0f766e;padding:7px 12px;border-radius:8px;text-decoration:none;font-weight:600}main{max-width:1000px;margin:0 auto;padding:16px}main img{display:block;width:100%%;margin:0 0 14px;box-shadow:0 2px 12px rgba(0,0,0,.5);background:#fff}.muted{color:#9aa3b2}footer{text-align:center;color:#9aa3b2;padding:16px;font-size:12px}</style></head>
<body><header><h1>%s</h1><span class="muted">%s</span><a class="btn" href="%s/file?download=1">Download</a></header><main>%s</main><footer>Shared with Pocket Documents</footer></body></html>`,
		title, title, html.EscapeString(d.Created), base, imgs.String())
}

func (s *Server) handleShareFile(w http.ResponseWriter, r *http.Request) {
	sh, d := s.shareDoc(w, r)
	if d == nil {
		return
	}
	if sh.File == "original" || !d.HasArchive {
		name := safeFileName(d.Title)
		if d.OriginalExt != "" {
			name += "." + d.OriginalExt
		}
		serveFile(w, r, s.originalPath(d), name, firstNonEmpty(d.Mime, "application/octet-stream"), queryBool(r, "download"))
		return
	}
	path, name, ctype := s.bestFile(d)
	serveFile(w, r, path, name, ctype, queryBool(r, "download"))
}

func (s *Server) handleShareThumb(w http.ResponseWriter, r *http.Request) {
	_, d := s.shareDoc(w, r)
	if d == nil {
		return
	}
	if _, err := os.Stat(s.thumbPath(d.ID)); err != nil {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, s.thumbPath(d.ID), "thumb.jpg", "image/jpeg", false)
}

func (s *Server) handleSharePageImage(w http.ResponseWriter, r *http.Request) {
	_, d := s.shareDoc(w, r)
	if d == nil {
		return
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(chi.URLParam(r, "n"), ".jpg"))
	p, err := s.pageImage(r.Context(), d, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, p, "page.jpg", "image/jpeg", false)
}
