package server

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

func (s *Server) isAdmin(r *http.Request) bool {
	u := userFrom(r.Context())
	return u != nil && u.Role == "admin"
}

// publicSettings adds what the UI needs to know about the environment.
func (s *Server) publicSettings(r *http.Request, st store.Settings) map[string]any {
	tools := ocr.Detect(r.Context(), s.cfg.GotenbergURL)
	_, aiStatus := s.inferenceFor(r.Context(), me(r), st.AIProvider, st.AIKey != "")
	return map[string]any{
		"title": st.Title, "setup_complete": st.SetupComplete,
		"ocr_language": st.OCRLanguage, "ocr_mode": st.OCRMode, "ocr_deskew": st.OCRDeskew, "ocr_rotate": st.OCRRotate, "ocr_clean": st.OCRClean,
		"ocr_max_pages": st.OCRMaxPages, "ocr_output_type": st.OCROutputType, "trash_days": st.TrashDays,
		"consume_dir": s.cfg.ConsumeDir, "consume_subdirs_as_tags": st.ConsumeSubdirsAsTags, "folder_roots": s.cfg.FolderRoots,
		"folder_kind": st.FolderKind,
		"date_format": st.DateFormat, "default_view": st.DefaultView, "page_size": st.PageSize, "default_sort": st.DefaultSort,
		"title_from_name": st.TitleFromName, "date_from_text": st.DateFromText, "date_from_name": st.DateFromName, "suggest_on_open": st.SuggestOnOpen,
		"barcodes_enabled": st.BarcodesEnabled, "barcode_asn_prefix": st.BarcodeASNPrefix, "barcode_separator": st.BarcodeSeparator, "barcode_keep_separator": st.BarcodeKeepSep, "barcode_max_pages": st.BarcodeMaxPages,
		"money_mode": st.MoneyMode, "money_url": st.MoneyURL, "money_token_set": st.MoneyToken != "", "money_account": st.MoneyAccount,
		"notify_url":       st.NotifyURL,
		"ai_auto":          st.AIAuto, "ai_provider": aiStatus.Provider, "ai_pocket_model": st.AIPocketModel, "ai_status": aiStatus, "ai_configured": aiStatus.Ready,
		"ai_url": st.AIURL, "ai_key_set": st.AIKey != "", "ai_model": st.AIModel, "ai_model_effective": firstNonEmpty(st.AIModel, ai.DefaultModel), "ai_context": st.AIContext,
		"core":             s.core.Configured(),
		"core_ready":       s.coreReady(),
		"tools":            tools,
		"max_upload_bytes": s.cfg.MaxUploadBytes,
		"ocr_workers":      s.cfg.OCRWorkers,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r, st))
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	cur, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in struct {
		store.Settings
		MoneyToken *string `json:"money_token"`
		AIKey      *string `json:"ai_key"`
	}
	in.Settings = cur
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st := in.Settings
	// box-wide part
	if s.isAdmin(r) {
		if strings.TrimSpace(st.Title) == "" {
			st.Title = store.DefaultTitle
		}
		st.OCRLanguage = strings.Trim(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(st.OCRLanguage)), " ", ""), "+")
		if st.OCRLanguage == "" {
			st.OCRLanguage = "eng"
		}
		if st.OCRMode != "redo" && st.OCRMode != "force" {
			st.OCRMode = "skip"
		}
		if st.OCROutputType != "pdf" {
			st.OCROutputType = "pdfa"
		}
		st.OCRMaxPages = clampInt(st.OCRMaxPages, 0, 5000)
		st.TrashDays = clampInt(st.TrashDays, 0, 3650)
		st.BarcodeASNPrefix = strings.TrimSpace(st.BarcodeASNPrefix)
		st.BarcodeSeparator = firstNonEmpty(strings.TrimSpace(st.BarcodeSeparator), "PATCHT")
		st.BarcodeMaxPages = clampInt(st.BarcodeMaxPages, 1, 500)
		st.SetupComplete = cur.SetupComplete
		if err := s.store.SaveBoxSettings(ctx, st); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	// personal part
	if st.DateFormat != "dmy-dot" && st.DateFormat != "long" {
		st.DateFormat = "iso"
	}
	if st.DefaultView != "cards" && st.DefaultView != "list" {
		st.DefaultView = "table"
	}
	st.PageSize = clampInt(st.PageSize, 10, 500)
	if st.MoneyMode != "direct" && st.MoneyMode != "off" {
		st.MoneyMode = "core"
	}
	st.MoneyToken = ""
	// the documents folder has its own endpoint; what it saved stays
	st.FolderKind, st.FolderPath, st.FolderURL, st.FolderUser, st.FolderName, st.FolderViaCore, st.FolderFile, st.FolderInbox, st.FolderInboxName = cur.FolderKind, cur.FolderPath, cur.FolderURL, cur.FolderUser, cur.FolderName, cur.FolderViaCore, cur.FolderFile, cur.FolderInbox, cur.FolderInboxName
	if in.MoneyToken != nil {
		st.MoneyToken = strings.TrimSpace(*in.MoneyToken)
		if st.MoneyToken == "" {
			_ = s.store.SetUserSetting(ctx, uid, "money_token", "")
		}
	}
	if !core.ValidAIProvider(st.AIProvider) {
		writeErr(w, http.StatusBadRequest, "choose automatic, Pocket AI or OpenAI")
		return
	}
	if !core.ValidInferenceModel(st.AIPocketModel) {
		writeErr(w, http.StatusBadRequest, "invalid Pocket AI model")
		return
	}
	st.AIKey = ""
	if in.AIKey != nil {
		st.AIKey = strings.TrimSpace(*in.AIKey)
		if st.AIKey == "" {
			_ = s.store.SetUserSetting(ctx, uid, "ai_key", "")
		}
	}
	if err := s.store.SaveSettings(ctx, uid, st); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	st, _ = s.store.GetSettings(ctx, uid)
	writeJSON(w, http.StatusOK, s.publicSettings(r, st))
}

func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	_ = decode(r, &in)
	st, _ := s.store.GetSettings(r.Context(), me(r))
	u := firstNonEmpty(strings.TrimSpace(in.URL), st.NotifyURL)
	if u == "" {
		writeErr(w, http.StatusBadRequest, "no notification URL")
		return
	}
	if err := sendNotify(u, "Pocket Documents", "Notifications work. You hear from Pocket Documents here when a document cannot be processed."); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	counts := s.store.UserCounts(r.Context(), me(r))
	out := map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": counts, "tools": ocr.Detect(r.Context(), s.cfg.GotenbergURL), "admin": s.isAdmin(r),
	}
	if s.isAdmin(r) {
		if n, err := s.store.CountUsers(r.Context()); err == nil {
			counts["users"] = n
		}
		out["data_dir"], out["db_path"], out["db_bytes"] = s.cfg.DataDir, s.cfg.DBPath, size
		out["originals_bytes"], out["archive_bytes"], out["thumbs_bytes"], out["pages_bytes"] = dirSize(s.cfg.OriginalsDir), dirSize(s.cfg.ArchiveDir), dirSize(s.cfg.ThumbsDir), dirSize(s.cfg.PagesDir)
		out["consume_dir"], out["folder_roots"] = s.cfg.ConsumeDir, s.cfg.FolderRoots
		q, run, failed := s.store.JobCounts(r.Context(), 0)
		out["jobs"] = map[string]int{"queued": q, "running": run, "failed": failed}
		out["box_counts"] = s.store.Counts(r.Context())
	}
	if cl, err := s.classifierFor(r.Context(), me(r)); err == nil && cl != nil {
		out["classifier"] = cl.Suggest("", 1).TrainedOn
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDashboard is the Homepage widget and the app's own dashboard numbers.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	st := s.store.Stats(ctx, uid)
	q, run, failed := s.store.JobCounts(ctx, uid)
	recent, _, _, _ := s.store.ListDocuments(ctx, store.DocFilter{UserID: uid, Sort: "-added", Limit: 8, NoFacets: true})
	views, _ := s.store.ListSavedViews(ctx, uid)
	dash := []map[string]any{}
	for _, v := range views {
		if !v.OnDashboard {
			continue
		}
		docs, total := s.viewDocs(r, v, 6)
		dash = append(dash, map[string]any{"view": v, "documents": docs, "count": total})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"documents": st.Documents, "inbox": st.Inbox, "untagged": st.Untagged, "pending": st.Pending + q + run, "failed": st.Failed, "added_today": st.AddedToday, "added_week": st.AddedWeek, "added_month": st.AddedMonth,
		"pages": st.Pages, "bytes": st.Bytes, "archive_bytes": st.ArchiveBytes, "storage_mb": (st.Bytes + st.ArchiveBytes) / (1 << 20), "trash": st.Trash, "favorites": st.Favorites,
		"queued": q, "running": run, "jobs_failed": failed, "recent": recent, "views": dash, "stats": st,
	})
}

// viewDocs runs a saved view's query through the normal list filter.
func (s *Server) viewDocs(r *http.Request, v store.SavedView, limit int) ([]store.Document, int) {
	q := map[string]any{}
	_ = jsonUnmarshal(v.Query, &q)
	r2 := r.Clone(r.Context())
	vals := r2.URL.Query()
	for k := range vals {
		vals.Del(k)
	}
	for k, val := range q {
		switch x := val.(type) {
		case string:
			vals.Set(k, x)
		case float64:
			vals.Set(k, trimFloat(x))
		case bool:
			if x {
				vals.Set(k, "1")
			}
		case []any:
			for _, e := range x {
				switch y := e.(type) {
				case string:
					vals.Add(k, y)
				case float64:
					vals.Add(k, trimFloat(y))
				}
			}
		}
	}
	vals.Set("limit", trimFloat(float64(limit)))
	vals.Set("no_facets", "1")
	r2.URL.RawQuery = vals.Encode()
	f, _ := s.listFilter(r2)
	docs, total, _, err := s.store.ListDocuments(r.Context(), f)
	if err != nil {
		return []store.Document{}, 0
	}
	return docs, total
}

func (s *Server) handleStatistics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Stats(r.Context(), me(r)))
}

// handleChanges answers a stamp that changes whenever the person's documents
// or queue do; open pages ask it and reload when it moves.
func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	q, run, _ := s.store.JobCounts(r.Context(), me(r))
	writeJSON(w, http.StatusOK, map[string]any{"stamp": s.store.ChangeStamp(r.Context(), me(r)), "queued": q, "running": run})
}

// ---- trash ----

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	docs, total, _, err := s.store.ListDocuments(r.Context(), store.DocFilter{UserID: me(r), Trash: true, Sort: "-modified", Limit: 500, NoFacets: true})
	if err != nil {
		s.fail(w, err, "trash")
		return
	}
	st, _ := s.store.GetSettings(r.Context(), 0)
	writeJSON(w, http.StatusOK, map[string]any{"results": docs, "count": total, "trash_days": st.TrashDays})
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	n := 0
	for _, id := range s.store.DocumentIDs(ctx, uid, true) {
		if d, err := s.store.GetDocument(ctx, uid, id, false); err == nil {
			if s.purgeDocument(ctx, d) == nil {
				n++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"purged": n})
}

// ---- maintenance ----

func (s *Server) handleReindex(w http.ResponseWriter, r *http.Request) {
	j, err := s.store.EnqueueJob(r.Context(), me(r), nil, "reindex", nil)
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	s.nudge()
	writeJSON(w, http.StatusAccepted, j)
}

func (s *Server) handleRetrain(w http.ResponseWriter, r *http.Request) {
	s.classifiers.Invalidate(me(r))
	cl, err := s.classifierFor(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "train")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trained_on": cl.Suggest("", 1).TrainedOn})
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeErr(w, http.StatusForbidden, "only an admin can download the database")
		return
	}
	dst := filepath.Join(s.cfg.DataDir, "backups", "pocket-documents-"+time.Now().Format("20060102-150405")+".db")
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(dst)+`"`)
	http.ServeFile(w, r, dst)
}
