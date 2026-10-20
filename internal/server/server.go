// Package server wires the HTTP API, auth, the background workers and the
// embedded SPA.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/config"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/match"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Version is injected at build time.
var Version = "dev"

// Server holds dependencies.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	log      *slog.Logger
	webFS    fs.FS
	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64
	http     *http.Client

	oidcMu sync.Mutex
	oidc   *oidcProvider

	core     *core.Client
	coreMu   sync.Mutex
	coreOK   bool
	coreErr  string
	resolved map[string]resolvedURL

	classifiers *match.Cache
	foldersMu   sync.Mutex
	folders     map[int64]*docFolder
	wake        chan struct{} // nudges the workers
	workersOn   bool
	processing  sync.Map // document id → job id while a worker has it
}

type resolvedURL struct {
	url string
	at  time.Time
}

// New creates a server.
func New(cfg *config.Config, st *store.Store, log *slog.Logger, webFS fs.FS) *Server {
	s := &Server{cfg: cfg, store: st, log: log, webFS: webFS, started: time.Now(), http: &http.Client{Timeout: 20 * time.Second},
		oidc:        &oidcProvider{issuerURL: cfg.OIDCIssuer},
		core:        core.New(cfg.CoreURL, core.Options{TokenFile: cfg.DataDir + "/core.token"}),
		resolved:    map[string]resolvedURL{},
		classifiers: match.NewCache(),
		folders:     map[int64]*docFolder{},
		wake:        make(chan struct{}, 1),
	}
	store.EnvDefaults.OCRLanguage = cfg.OCRLanguage
	store.EnvDefaults.OCRMode = cfg.OCRMode
	store.EnvDefaults.OCRDeskew = cfg.OCRDeskew
	store.EnvDefaults.OCRRotate = cfg.OCRRotate
	store.EnvDefaults.OCRClean = cfg.OCRClean
	store.EnvDefaults.OCRMaxPages = cfg.OCRMaxPages
	store.EnvDefaults.OCROutputType = cfg.OCROutputType
	return s
}

// Bootstrap seeds the first admin (if configured) and demo data, and starts
// the workers, the consume folder watcher and the janitor.
func (s *Server) Bootstrap(ctx context.Context) error {
	for _, d := range []string{s.cfg.OriginalsDir, s.cfg.ArchiveDir, s.cfg.ThumbsDir, s.cfg.PagesDir, s.cfg.ConsumeDir, s.cfg.TmpDir, s.cfg.DataDir + "/backups"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("data dir %s: %w", d, err)
		}
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n == 0 && s.cfg.AuthMode == "local" && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		if len(s.cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
		u := &store.User{Email: s.cfg.AdminEmail, Name: s.cfg.AdminName, PasswordHash: string(h), Role: "admin"}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return err
		}
		st, _ := s.store.GetSettings(ctx, 0)
		st.SetupComplete = true
		if err := s.store.SaveBoxSettings(ctx, st); err != nil {
			return err
		}
		s.seedDefaults(ctx, u.ID)
		s.log.Info("admin user created from environment", "email", u.Email)
	}
	if s.cfg.DemoData {
		if err := s.seedDemo(ctx); err != nil {
			return err
		}
	}
	_ = s.store.PurgeSessions(ctx)
	if n := s.store.RequeueRunning(ctx); n > 0 {
		s.log.Info("requeued jobs left running by the last run", "jobs", n)
	}
	if dir := ocr.PrepareTessdata(s.tessdataDir()); dir != "" {
		s.log.Info("tesseract languages", "dir", dir)
	}
	tools := ocr.Detect(ctx, s.cfg.GotenbergURL)
	s.log.Info("ocr tools", "ocrmypdf", tools.OCRmyPDF, "tesseract", tools.Tesseract, "poppler", tools.PDFToText, "gotenberg", tools.Gotenberg, "languages", strings.Join(tools.Languages, ","))
	if !tools.CanOCR() {
		s.log.Warn("ocrmypdf/tesseract not found: scans will be stored without text; install them or use the Docker image")
	}
	s.startWorkers(ctx)
	go s.consumeLoop(ctx)
	go s.inboxLoop(ctx)
	go s.mailLoop(ctx)
	go s.janitor(ctx)
	if s.core.Configured() {
		go s.coreLoop(ctx)
	}
	return nil
}

// seedDefaults gives a new account an inbox tag so the first upload has somewhere to land.
func (s *Server) seedDefaults(ctx context.Context, userID int64) {
	if tags, _ := s.store.ListTags(ctx, userID); len(tags) == 0 {
		_ = s.store.CreateTag(ctx, &store.Tag{UserID: userID, Name: "Inbox", Color: "#f59e0b", IsInbox: true, Matchable: store.Matchable{MatchingAlgorithm: store.MatchNone}})
	}
}

// ---- Pocket Core ----

// coreLoop registers with Core on start (retrying until it answers) and
// re-registers every 30 minutes so version and last-seen stay fresh.
func (s *Server) coreLoop(ctx context.Context) {
	wait := 5 * time.Second
	for {
		err := s.registerWithCore(ctx)
		s.coreMu.Lock()
		s.coreOK, s.coreErr = err == nil, ""
		if err != nil {
			s.coreErr = err.Error()
		}
		s.coreMu.Unlock()
		if err != nil {
			s.log.Warn("pocket core: registration failed, retrying", "err", err, "in", wait.String())
		} else {
			wait = 30 * time.Minute
			s.reportModes(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err != nil && wait < 5*time.Minute {
			wait *= 2
		}
	}
}

func (s *Server) registerWithCore(ctx context.Context) error {
	m := core.Manifest{
		Slug: "documents", Name: "Pocket Documents",
		Description: "Scans and files made searchable: OCR into PDF/A, full-text search with filters, tags, correspondents, types, custom fields, rules and a classifier that learns how you file. A Paperless-ngx replacement.",
		Icon:        "/favicon.svg", Version: Version,
		URL: s.cfg.CoreAppURL, PublicURL: s.cfg.BaseURL, HealthPath: "/healthz", EnvPrefix: "DOCUMENTS",
		Publishes: []string{"documents.added", "documents.processed", "documents.updated", "documents.deleted", "documents.failed", "documents.shared"},
		Offers:    linkOffers,
		Uses:      linkUses,
		Services:  serviceUses,
	}
	if s.cfg.BaseURL != "" {
		m.OIDC = &core.OIDCRequest{RedirectURIs: []string{s.oidcCallbackURL()}}
	}
	reg, err := s.core.Register(ctx, s.cfg.CoreRegistrationKey, m)
	if err != nil {
		return err
	}
	if reg.Token != "" {
		s.log.Info("pocket core: registered, app token stored", "core", s.cfg.CoreURL)
	} else {
		s.log.Debug("pocket core: re-registered", "core", s.cfg.CoreURL)
	}
	return nil
}

// publish sends an event to Core without blocking. The payload names whose
// document it was (user_id, user_email).
func (s *Server) publish(userID int64, topic, subject string, payload map[string]any) {
	if !s.core.Configured() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		payload["user_id"] = userID
		if u, err := s.store.GetUser(ctx, userID); err == nil {
			payload["user_email"] = u.Email
		}
		if err := s.core.Publish(ctx, topic, subject, payload); err != nil {
			s.log.Warn("pocket core: publish failed", "topic", topic, "err", err)
		}
	}()
}

// resolveSibling asks Core where a sibling app lives (cached a minute).
func (s *Server) resolveSibling(ctx context.Context, slug string) string {
	if !s.core.Configured() {
		return ""
	}
	s.coreMu.Lock()
	if r, ok := s.resolved[slug]; ok && time.Since(r.at) < time.Minute {
		s.coreMu.Unlock()
		return r.url
	}
	s.coreMu.Unlock()
	url := ""
	if res, err := s.core.Resolve(ctx, slug); err == nil {
		url = res.URL
	}
	s.coreMu.Lock()
	s.resolved[slug] = resolvedURL{url: url, at: time.Now()}
	s.coreMu.Unlock()
	return url
}

// coreStatus summarises the Pocket Core link for the About page.
func (s *Server) coreStatus() map[string]any {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return map[string]any{"configured": s.core.Configured(), "url": s.cfg.CoreURL, "registered": s.coreOK, "error": s.coreErr}
}

// janitor empties the trash past its retention, prunes the job log and
// drops stale sessions, once an hour.
func (s *Server) janitor(ctx context.Context) {
	run := func() {
		st, err := s.store.GetSettings(ctx, 0)
		if err != nil {
			return
		}
		if docs, err := s.store.TrashOlderThan(ctx, st.TrashDays); err == nil {
			for _, d := range docs {
				if err := s.purgeDocument(ctx, &d); err == nil {
					s.log.Info("trash emptied", "document", d.ID, "title", d.Title)
				}
			}
		}
		s.store.PruneJobs(ctx, 500)
		_ = s.store.PurgeSessions(ctx)
		s.runScheduled(ctx)
		// page image caches of documents that no longer exist
		if entries, err := os.ReadDir(s.cfg.PagesDir); err == nil {
			for _, e := range entries {
				id, err := strconv.ParseInt(e.Name(), 10, 64)
				if err != nil {
					continue
				}
				if _, err := s.store.GetDocumentAny(ctx, id, false); errors.Is(err, store.ErrNotFound) {
					_ = os.RemoveAll(filepath.Join(s.cfg.PagesDir, e.Name()))
				}
			}
		}
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// Router builds the chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	if s.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(s.logging)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5, "application/json", "text/html", "text/plain", "text/css", "application/javascript", "image/svg+xml"))

	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/api/health", s.handleHealth)
	if s.cfg.MetricsEnabled {
		r.Get("/metrics", s.handleMetrics)
	}
	r.Post("/api/v1/client-error", s.handleClientError)

	// Public share links: a page, the file, the thumbnail.
	r.Get("/s/{token}", s.handleSharePage)
	r.Get("/s/{token}/file", s.handleShareFile)
	r.Get("/s/{token}/thumb", s.handleShareThumb)
	r.Get("/s/{token}/pages/{n}", s.handleSharePageImage)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/setup", s.handleSetup)
		r.Get("/auth/status", s.handleAuthStatus)
		r.Get("/auth/oidc/start", s.handleOIDCStart)
		r.Get("/auth/oidc/callback", s.handleOIDCCallback)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Put("/auth/me", s.handleUpdateMe)

			r.Get("/dashboard", s.handleDashboard)
			r.Get("/statistics", s.handleStatistics)
			r.Get("/changes", s.handleChanges)

			r.Get("/documents", s.handleListDocuments)
			r.Post("/documents", s.handleUpload)
			r.Post("/documents/post_document", s.handleUpload) // Paperless clients
			r.Post("/documents/bulk", s.handleBulk)
			r.Get("/documents/next_asn", s.handleNextASN)
			r.Get("/documents/{id}", s.handleGetDocument)
			r.Put("/documents/{id}", s.handleUpdateDocument)
			r.Patch("/documents/{id}", s.handleUpdateDocument)
			r.Delete("/documents/{id}", s.handleDeleteDocument)
			r.Post("/documents/{id}/restore", s.handleRestoreDocument)
			r.Delete("/documents/{id}/purge", s.handlePurgeDocument)
			r.Get("/documents/{id}/download", s.handleDownload)
			r.Get("/documents/{id}/preview", s.handlePreview)
			r.Get("/documents/{id}/thumb", s.handleThumb)
			r.Get("/documents/{id}/pages/{n}", s.handlePageImage)
			r.Get("/documents/{id}/pages/{n}/text", s.handlePageText)
			r.Get("/documents/{id}/text", s.handleText)
			r.Post("/documents/{id}/reprocess", s.handleReprocess)
			r.Get("/documents/{id}/suggestions", s.handleSuggestions)
			r.Post("/documents/{id}/suggestions/apply", s.handleApplySuggestions)
			r.Get("/documents/{id}/similar", s.handleSimilar)
			r.Get("/documents/{id}/notes", s.handleListNotes)
			r.Post("/documents/{id}/notes", s.handleAddNote)
			r.Delete("/documents/{id}/notes/{nid}", s.handleDeleteNote)
			r.Get("/documents/{id}/history", s.handleHistory)
			r.Get("/documents/{id}/shares", s.handleDocumentShares)
			r.Post("/documents/{id}/shares", s.handleCreateShare)
			r.Get("/documents/{id}/jobs", s.handleDocumentJobs)
			r.Post("/documents/{id}/money", s.handleBookMoney)
			r.Post("/documents/{id}/pages", s.handlePages)
			r.Post("/documents/{id}/replace", s.handleReplaceFile)
			r.Get("/documents/{id}/versions", s.handleListVersions)
			r.Get("/documents/{id}/versions/{n}/download", s.handleDownloadVersion)
			r.Post("/documents/{id}/versions/{n}/restore", s.handleRestoreVersion)
			r.Get("/documents/{id}/shared_with", s.handleListDocShares)
			r.Post("/documents/{id}/shared_with", s.handleShareWith)
			r.Delete("/documents/{id}/shared_with/{uid}", s.handleUnshareWith)
			r.Post("/documents/merge", s.handleMerge)
			r.Get("/shared", s.handleSharedWithMe)
			r.Get("/ocr/languages", s.handleListLanguages)
			r.Post("/ocr/languages", s.handleInstallLanguage)
			r.Delete("/ocr/languages/{code}", s.handleRemoveLanguage)

			for _, t := range []struct {
				path string
				kind string
			}{{"/tags", "tag"}, {"/correspondents", "correspondent"}, {"/document_types", "document_type"}, {"/storage_paths", "storage_path"}, {"/custom_fields", "custom_field"}} {
				kind := t.kind
				r.Get(t.path, s.taxonomyList(kind))
				r.Post(t.path, s.taxonomyCreate(kind))
				r.Put(t.path+"/{id}", s.taxonomyUpdate(kind))
				r.Delete(t.path+"/{id}", s.taxonomyDelete(kind))
			}
			r.Get("/taxonomy", s.handleTaxonomy)

			r.Get("/saved_views", s.handleListViews)
			r.Post("/saved_views", s.handleCreateView)
			r.Put("/saved_views/{id}", s.handleUpdateView)
			r.Delete("/saved_views/{id}", s.handleDeleteView)

			r.Get("/rules", s.handleListRules)
			r.Post("/rules", s.handleCreateRule)
			r.Put("/rules/{id}", s.handleUpdateRule)
			r.Delete("/rules/{id}", s.handleDeleteRule)
			r.Post("/rules/{id}/run", s.handleRunRule)

			r.Get("/shares", s.handleListShares)
			r.Delete("/shares/{id}", s.handleDeleteShare)

			r.Get("/jobs", s.handleListJobs)
			r.Get("/jobs/{id}", s.handleGetJob)
			r.Post("/jobs/{id}/retry", s.handleRetryJob)
			r.Post("/jobs/{id}/cancel", s.handleCancelJob)
			r.Delete("/jobs", s.handleClearJobs)

			r.Get("/trash", s.handleTrash)
			r.Delete("/trash", s.handleEmptyTrash)

			r.Get("/export", s.handleExport)
			r.Post("/import", s.handleImport)
			r.Post("/import/paperless", s.handleImportPaperless)
			r.Post("/import/paperless/test", s.handleTestPaperless)
			r.Post("/reindex", s.handleReindex)
			r.Post("/retrain", s.handleRetrain)

			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Put("/users/{id}", s.handleUpdateUser)
			r.Delete("/users/{id}", s.handleDeleteUser)

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)
			r.Get("/settings/system", s.handleSystemInfo)
			r.Get("/folder", s.handleGetFolder)
			r.Put("/folder", s.handlePutFolder)
			r.Post("/folder/test", s.handleTestFolder)
			r.Post("/folder/sync", s.handleFolderSync)
			r.Post("/folder/inbox", s.handleFolderInbox)
			r.Post("/settings/notify/test", s.handleTestNotify)
			r.Get("/money/status", s.handleMoneyStatus)

			r.Get("/tokens", s.handleListTokens)
			r.Post("/tokens", s.handleCreateToken)
			r.Delete("/tokens/{id}", s.handleDeleteToken)

			r.Get("/backup", s.handleBackup)
			s.mountMail(r)
		})
	})
	// Paperless-ngx clients: the same documents through Paperless' own API shapes.
	s.mountPaperlessAPI(r)

	r.NotFound(s.handleSPA)
	return r
}

// ---------- helpers ----------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.requests.Add(1)
		if ww.Status() >= 500 {
			s.errors.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String(), "ip", r.RemoteAddr)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func (s *Server) fail(w http.ResponseWriter, err error, context string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "already exists")
		return
	}
	s.log.Error(context, "err", err)
	writeErr(w, http.StatusInternalServerError, context+": "+err.Error())
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	return dec.Decode(v)
}

// decodeKeys decodes JSON into v and also reports which top-level keys were
// present, so PUT handlers can tell "not sent" from "sent empty".
func decodeKeys(r *http.Request, v any) (map[string]bool, error) {
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(raw))
	for k := range raw {
		keys[k] = true
	}
	return keys, json.Unmarshal(b, v)
}

func idParam(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

func me(r *http.Request) int64 { return actorID(r.Context()) }

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// actorName is who did something, for the history.
func actorName(r *http.Request) string {
	u := userFrom(r.Context())
	if u == nil {
		return "system"
	}
	if app := linkAppFrom(r.Context()); app != "" {
		return app + " for " + firstNonEmpty(u.Name, u.Email)
	}
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ") || r.Header.Get("X-API-Key") != "" {
		return firstNonEmpty(u.Name, u.Email) + " (api)"
	}
	return firstNonEmpty(u.Name, u.Email)
}

// handleClientError writes a crash of the web app into the server's log.
func (s *Server) handleClientError(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message, Stack, Component, URL, Where, Agent string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	who := ""
	if u, err := s.resolveUser(r); err == nil && u != nil {
		who = u.Email
	}
	s.log.Warn("web app crashed", "user", who, "where", in.Where, "url", in.URL, "message", in.Message, "stack", in.Stack, "component", in.Component, "agent", in.Agent)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- health / metrics ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": Version, "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := s.store.Counts(ctx)
	q, run, failed := s.store.JobCounts(ctx, 0)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP pocket_documents_info Build info.\n# TYPE pocket_documents_info gauge\npocket_documents_info{version=%q} 1\n", Version)
	fmt.Fprintf(w, "# HELP pocket_documents_uptime_seconds Process uptime.\n# TYPE pocket_documents_uptime_seconds gauge\npocket_documents_uptime_seconds %d\n", int(time.Since(s.started).Seconds()))
	fmt.Fprintf(w, "# HELP pocket_documents_http_requests_total HTTP requests served.\n# TYPE pocket_documents_http_requests_total counter\npocket_documents_http_requests_total %d\n", s.requests.Load())
	fmt.Fprintf(w, "# HELP pocket_documents_http_errors_total HTTP 5xx responses.\n# TYPE pocket_documents_http_errors_total counter\npocket_documents_http_errors_total %d\n", s.errors.Load())
	fmt.Fprintf(w, "# HELP pocket_documents_jobs Jobs by state.\n# TYPE pocket_documents_jobs gauge\npocket_documents_jobs{state=\"queued\"} %d\npocket_documents_jobs{state=\"running\"} %d\npocket_documents_jobs{state=\"failed\"} %d\n", q, run, failed)
	fmt.Fprintf(w, "# HELP pocket_documents_rows Rows per table.\n# TYPE pocket_documents_rows gauge\n")
	for k, v := range counts {
		fmt.Fprintf(w, "pocket_documents_rows{table=%q} %d\n", k, v)
	}
}

// ---------- static / SPA ----------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.Error(w, "web UI not built (run `make web`)", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.webFS.Open(path); err == nil {
		f.Close()
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(s.webFS).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	r.URL.Path = "/"
	http.FileServerFS(s.webFS).ServeHTTP(w, r)
}
