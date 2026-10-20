package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// A Paperless-ngx compatible REST API, so Paperless clients (Paperless-Mobile,
// Swift Paperless, scripts) work against this app unchanged. Everything here
// is a facade over the same store and helpers the native /api/v1 uses, scoped
// to the signed-in person exactly like the rest of the app.

const plAPIVersion = "9"

// plAcked remembers which tasks a client dismissed (per process).
var plAcked sync.Map

// mountPaperlessAPI registers the Paperless-ngx routes on the root router.
// Paths are registered with and without the trailing slash Paperless uses.
func (s *Server) mountPaperlessAPI(r chi.Router) {
	reg := func(method, path string, h http.HandlerFunc) {
		r.Method(method, path, h)
		if strings.HasSuffix(path, "/") && path != "/api/" {
			r.Method(method, strings.TrimSuffix(path, "/"), h)
		}
	}
	open := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			plHeaders(w)
			h(w, r)
		}
	}
	auth := s.plAuth

	reg("POST", "/api/token/", open(s.plToken))
	reg("GET", "/api/", auth(s.plRoot))
	reg("GET", "/api/ui_settings/", auth(s.plUISettings))
	reg("GET", "/api/status/", auth(s.plStatus))
	reg("GET", "/api/remote_version/", auth(s.plRemoteVersion))
	reg("GET", "/api/statistics/", auth(s.plStatistics))
	reg("GET", "/api/users/", auth(s.plUsers))
	reg("GET", "/api/users/{id}/", auth(s.plUser))
	reg("GET", "/api/groups/", auth(s.plEmptyList))
	reg("GET", "/api/profile/", auth(s.plProfile))
	reg("PATCH", "/api/profile/", auth(s.plProfile))
	for _, p := range []string{"mail_accounts", "mail_rules", "workflows", "workflow_triggers", "workflow_actions", "share_links"} {
		reg("GET", "/api/"+p+"/", auth(s.plEmptyList))
	}
	reg("GET", "/api/logs/", auth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, []string{}) }))

	// taxonomy
	for _, k := range []string{"tags", "correspondents", "document_types", "storage_paths", "custom_fields"} {
		kind := k
		reg("GET", "/api/"+kind+"/", auth(s.plTaxonomyList(kind)))
		reg("POST", "/api/"+kind+"/", auth(s.plTaxonomyCreate(kind)))
		reg("GET", "/api/"+kind+"/{id}/", auth(s.plTaxonomyGet(kind)))
		reg("PATCH", "/api/"+kind+"/{id}/", auth(s.plTaxonomyUpdate(kind)))
		reg("PUT", "/api/"+kind+"/{id}/", auth(s.plTaxonomyUpdate(kind)))
		reg("DELETE", "/api/"+kind+"/{id}/", auth(s.plTaxonomyDelete(kind)))
	}
	reg("GET", "/api/saved_views/", auth(s.plViewsList))
	reg("POST", "/api/saved_views/", auth(s.plViewCreate))
	reg("GET", "/api/saved_views/{id}/", auth(s.plViewGet))
	reg("PATCH", "/api/saved_views/{id}/", auth(s.plViewUpdate))
	reg("PUT", "/api/saved_views/{id}/", auth(s.plViewUpdate))
	reg("DELETE", "/api/saved_views/{id}/", auth(s.plViewDelete))

	// documents
	reg("GET", "/api/documents/", auth(s.plDocumentsList))
	reg("POST", "/api/documents/post_document/", auth(s.plPostDocument))
	reg("POST", "/api/documents/bulk_edit/", auth(s.plBulkEdit))
	reg("GET", "/api/documents/next_asn/", auth(s.plNextASN))
	reg("POST", "/api/documents/selection_data/", auth(s.plSelectionData))
	reg("GET", "/api/documents/{id}/", auth(s.plDocumentGet))
	reg("PATCH", "/api/documents/{id}/", auth(s.plDocumentUpdate))
	reg("PUT", "/api/documents/{id}/", auth(s.plDocumentUpdate))
	reg("DELETE", "/api/documents/{id}/", auth(s.plDocumentDelete))
	reg("GET", "/api/documents/{id}/download/", auth(s.plDownload))
	reg("GET", "/api/documents/{id}/preview/", auth(s.plPreview))
	reg("GET", "/api/documents/{id}/thumb/", auth(s.plThumb))
	reg("GET", "/api/documents/{id}/metadata/", auth(s.plMetadata))
	reg("GET", "/api/documents/{id}/suggestions/", auth(s.plSuggestions))
	reg("GET", "/api/documents/{id}/notes/", auth(s.plNotesList))
	reg("POST", "/api/documents/{id}/notes/", auth(s.plNoteAdd))
	reg("DELETE", "/api/documents/{id}/notes/", auth(s.plNoteDelete))
	reg("GET", "/api/documents/{id}/history/", auth(s.plHistory))
	reg("GET", "/api/documents/{id}/share_links/", auth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, []any{}) }))

	// tasks & search
	reg("GET", "/api/tasks/", auth(s.plTasks))
	reg("GET", "/api/tasks/{id}/", auth(s.plTask))
	reg("POST", "/api/acknowledge_tasks/", auth(s.plAcknowledgeTasks))
	reg("GET", "/api/search/autocomplete/", auth(s.plAutocomplete))
	reg("GET", "/api/search/", auth(s.plSearch))
}

// ---------- auth ----------

func plHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Api-Version", plAPIVersion)
	w.Header().Set("X-Version", Version)
}

func plDetail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"detail": msg})
}

// plResolveUser resolves the caller from "Authorization: Token", Basic auth, or
// whatever the app itself accepts (bearer token, session cookie, proxy header).
func (s *Server) plResolveUser(r *http.Request) (*store.User, error) {
	ctx := r.Context()
	h := r.Header.Get("Authorization")
	switch {
	case strings.HasPrefix(strings.ToLower(h), "token "):
		return s.store.GetUserByAPIToken(ctx, strings.TrimSpace(h[6:]))
	case strings.HasPrefix(strings.ToLower(h), "basic "):
		email, pw, ok := r.BasicAuth()
		if !ok {
			return nil, http.ErrNoCookie
		}
		u, err := s.store.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
		if err != nil || u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(pw)) != nil {
			return nil, http.ErrNoCookie
		}
		return u, nil
	}
	return s.resolveUser(r)
}

func (s *Server) plAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		plHeaders(w)
		u, err := s.plResolveUser(r)
		if errors.Is(err, errLinkScope) {
			plDetail(w, http.StatusForbidden, "outside the link's scope")
			return
		}
		if err != nil || u == nil {
			w.Header().Set("WWW-Authenticate", `Token realm="api"`)
			plDetail(w, http.StatusUnauthorized, "Authentication credentials were not provided.")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		if app := linkAppOf(r); app != "" {
			ctx = context.WithValue(ctx, linkAppKey, app)
		}
		next(w, r.WithContext(ctx))
	}
}

// plToken is Paperless' POST /api/token/: email + password → an API token.
func (s *Server) plToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		if err := decode(r, &in); err != nil {
			plDetail(w, http.StatusBadRequest, "invalid json")
			return
		}
	} else {
		_ = r.ParseMultipartForm(1 << 20)
		in.Username, in.Password = r.FormValue("username"), r.FormValue("password")
	}
	u, err := s.store.GetUserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(in.Username)))
	if err != nil || u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		time.Sleep(300 * time.Millisecond)
		writeJSON(w, http.StatusBadRequest, map[string]any{"non_field_errors": []string{"Unable to log in with provided credentials."}})
		return
	}
	plain, _, err := s.store.CreateAPIToken(r.Context(), u.ID, "Paperless client")
	if err != nil {
		s.fail(w, err, "create token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": plain})
}

// ---------- paging, ordering, small helpers ----------

type plPage struct{ page, size, offset int }

func plPaging(r *http.Request) plPage {
	page := queryInt(r, "page")
	if page < 1 {
		page = 1
	}
	size := queryInt(r, "page_size")
	if size <= 0 {
		size = 25
	}
	size = clampInt(size, 1, 500)
	return plPage{page: page, size: size, offset: (page - 1) * size}
}

func plPageURL(r *http.Request, page int) string {
	q := r.URL.Query()
	q.Set("page", strconv.Itoa(page))
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.Path + "?" + q.Encode()
}

func plListResponse(r *http.Request, p plPage, count int, results any, all []int64) map[string]any {
	out := map[string]any{"count": count, "next": nil, "previous": nil, "results": results}
	if all != nil {
		out["all"] = all
	}
	if p.offset+p.size < count {
		out["next"] = plPageURL(r, p.page+1)
	}
	if p.page > 1 {
		out["previous"] = plPageURL(r, p.page-1)
	}
	return out
}

// plListMaps answers a list endpoint from an in-memory slice: filters by
// name / id, orders, pages.
func plListMaps(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	q := r.URL.Query()
	if v := q.Get("name__icontains"); v != "" {
		items = plKeep(items, func(m map[string]any) bool {
			return strings.Contains(strings.ToLower(plStr(m["name"])), strings.ToLower(v))
		})
	}
	if v := q.Get("name__iexact"); v != "" {
		items = plKeep(items, func(m map[string]any) bool { return strings.EqualFold(plStr(m["name"]), v) })
	}
	if v := q.Get("name__istartswith"); v != "" {
		items = plKeep(items, func(m map[string]any) bool {
			return strings.HasPrefix(strings.ToLower(plStr(m["name"])), strings.ToLower(v))
		})
	}
	if ids := plInts(q["id__in"]); len(ids) > 0 {
		set := map[int64]bool{}
		for _, id := range ids {
			set[id] = true
		}
		items = plKeep(items, func(m map[string]any) bool { return set[plInt(m["id"])] })
	}
	if v := q.Get("id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		items = plKeep(items, func(m map[string]any) bool { return plInt(m["id"]) == id })
	}
	if ord := q.Get("ordering"); ord != "" {
		key, desc := strings.TrimPrefix(ord, "-"), strings.HasPrefix(ord, "-")
		sort.SliceStable(items, func(i, j int) bool {
			less := plLess(items[i][key], items[j][key])
			if desc {
				return plLess(items[j][key], items[i][key])
			}
			return less
		})
	}
	p := plPaging(r)
	count := len(items)
	lo, hi := clampInt(p.offset, 0, count), clampInt(p.offset+p.size, 0, count)
	writeJSON(w, http.StatusOK, plListResponse(r, p, count, items[lo:hi], nil))
}

func plKeep(items []map[string]any, f func(map[string]any) bool) []map[string]any {
	out := items[:0:0]
	for _, m := range items {
		if f(m) {
			out = append(out, m)
		}
	}
	return out
}

func plStr(v any) string {
	s, _ := v.(string)
	return s
}

func plInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func plLess(a, b any) bool {
	switch x := a.(type) {
	case string:
		return strings.ToLower(x) < strings.ToLower(plStr(b))
	case int, int64, float64:
		return plInt(a) < plInt(b)
	case bool:
		return !x && b == true
	}
	return false
}

// plInts reads ids from repeated and comma-separated values.
func plInts(vals []string) []int64 {
	var out []int64
	for _, raw := range vals {
		for _, v := range strings.Split(raw, ",") {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func plBoolParam(v string) (val, ok bool) {
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	}
	return false, false
}

// plStamp turns our RFC 3339 UTC stamps into Paperless' "+00:00" form.
func plStamp(v string) any {
	if v == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t.UTC().Format("2006-01-02T15:04:05.000000-07:00")
	}
	return v
}

func plStampPtr(v *string) any {
	if v == nil {
		return nil
	}
	return plStamp(*v)
}

func plDateTime(date string) string {
	if len(date) >= 10 {
		return date[:10] + "T00:00:00+00:00"
	}
	return date
}

// plDate keeps the date part of whatever a client sends.
func plDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 10 {
		if _, err := time.Parse("2006-01-02", v[:10]); err == nil {
			return v[:10]
		}
	}
	return normalizeDate(v)
}

func plShiftDate(date string, days int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

func plSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// plTextColor picks black or white for a tag background.
func plTextColor(color string) string {
	c := strings.TrimPrefix(strings.ToLower(color), "#")
	if len(c) != 6 {
		return "#000000"
	}
	n, err := strconv.ParseUint(c, 16, 32)
	if err != nil {
		return "#000000"
	}
	r, g, b := float64(n>>16&255), float64(n>>8&255), float64(n&255)
	if 0.299*r+0.587*g+0.114*b > 150 {
		return "#000000"
	}
	return "#ffffff"
}

func plIntOrNil(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// ---------- meta endpoints ----------

func (s *Server) plRoot(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimSuffix(plPageURL(r, 1), "?page=1")
	names := []string{"correspondents", "document_types", "documents", "logs", "tags", "saved_views", "storage_paths", "tasks", "users", "groups", "mail_accounts", "mail_rules", "share_links", "workflow_triggers", "workflow_actions", "workflows", "custom_fields", "config", "profile", "search", "ui_settings", "statistics", "status"}
	out := map[string]string{}
	for _, n := range names {
		out[n] = base + n + "/"
	}
	writeJSON(w, http.StatusOK, out)
}

var plPermModels = []string{"document", "tag", "correspondent", "documenttype", "storagepath", "savedview", "customfield", "note", "paperlesstask", "uisettings", "user", "group", "mailaccount", "mailrule", "sharelink", "workflow", "workflowtrigger", "workflowaction", "logentry", "history", "appconfig"}

func plPermissions() []string {
	out := []string{}
	for _, m := range plPermModels {
		for _, a := range []string{"view", "add", "change", "delete"} {
			out = append(out, a+"_"+m)
		}
	}
	return out
}

func plUserMap(u *store.User) map[string]any {
	return map[string]any{
		"id": u.ID, "username": u.Email, "email": u.Email, "first_name": u.Name, "last_name": "",
		"date_joined": plStamp(u.CreatedAt), "is_staff": true, "is_active": true, "is_superuser": true,
		"groups": []any{}, "user_permissions": []any{}, "inherited_permissions": plPermissions(),
	}
}

func (s *Server) plUISettings(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	st, _ := s.store.GetSettings(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{"id": u.ID, "username": u.Email, "first_name": u.Name, "last_name": "", "display_name": firstNonEmpty(u.Name, u.Email), "is_staff": true, "is_superuser": true, "groups": []any{}},
		"settings": map[string]any{
			"app_title":         firstNonEmpty(st.Title, "Pocket Documents"),
			"app_logo":          nil,
			"update_checking":   map[string]any{"enabled": false, "backend_setting": "default"},
			"saved_views":       map[string]any{"warn_on_unsaved_change": true},
			"bulk_edit":         map[string]any{"confirmation_dialogs": true, "apply_on_close": false},
			"documents":         map[string]any{"page_size": st.PageSize},
			"notifications":     map[string]any{"consumer_new_documents": true, "consumer_success": true, "consumer_failed": true, "consumer_suppress_on_dashboard": true},
			"auditlog_enabled":  true,
			"email_enabled":     false,
			"trash_delay":       st.TrashDays,
			"gmail_oauth_url":   "",
			"outlook_oauth_url": "",
		},
		"permissions": plPermissions(),
		"version":     Version,
	})
}

func (s *Server) plStatus(w http.ResponseWriter, r *http.Request) {
	used := dirSize(s.cfg.OriginalsDir) + dirSize(s.cfg.ArchiveDir)
	dbOK := "OK"
	dbErr := ""
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		dbOK, dbErr = "ERROR", err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pngx_version": Version,
		"server_os":    "linux",
		"install_type": "docker",
		"storage":      map[string]any{"total": used, "available": 0},
		"database":     map[string]any{"type": "sqlite", "url": s.cfg.DBPath, "status": dbOK, "error": dbErr, "migration_status": map[string]any{"latest_migration": "", "unapplied_migrations": []any{}}},
		"tasks":        map[string]any{"redis_url": "", "redis_status": "OK", "redis_error": "", "celery_status": "OK", "celery_url": "", "celery_error": "", "index_status": "OK", "index_last_modified": nil, "index_error": "", "classifier_status": "OK", "classifier_last_trained": nil, "classifier_error": "", "sanity_check_status": "OK", "sanity_check_last_run": nil, "sanity_check_error": ""},
	})
}

func (s *Server) plRemoteVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": Version, "update_available": false})
}

func (s *Server) plStatistics(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	st := s.store.Stats(ctx, uid)
	var inbox any
	inboxes := []int64{}
	if ids := s.store.InboxTagIDs(ctx, uid); len(ids) > 0 {
		inbox = ids[0]
		inboxes = ids
	}
	types := []map[string]any{}
	for _, m := range st.ByMime {
		types = append(types, map[string]any{"mime_type": m.Name, "mime_type_count": m.Count})
	}
	paths, _ := s.store.ListStoragePaths(ctx, uid)
	var asn any
	if n := s.store.NextASN(ctx, uid); n > 1 {
		asn = n - 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"documents_total":           st.Documents,
		"documents_inbox":           st.Inbox,
		"inbox_tag":                 inbox,
		"inbox_tags":                inboxes,
		"document_file_type_counts": types,
		"character_count":           st.Characters,
		"tag_count":                 st.Tags,
		"correspondent_count":       st.Correspondents,
		"document_type_count":       st.Types,
		"storage_path_count":        len(paths),
		"current_asn":               asn,
	})
}

func (s *Server) plUsers(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	plListMaps(w, r, []map[string]any{plUserMap(u)})
}

func (s *Server) plUser(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if idParam(r, "id") != u.ID {
		plDetail(w, http.StatusNotFound, "Not found.")
		return
	}
	writeJSON(w, http.StatusOK, plUserMap(u))
}

func (s *Server) plEmptyList(w http.ResponseWriter, r *http.Request) {
	plListMaps(w, r, []map[string]any{})
}

func (s *Server) plProfile(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"email": u.Email, "first_name": u.Name, "last_name": "", "auth_token": "", "has_usable_password": true, "social_accounts": []any{}, "is_mfa_enabled": false})
}

// ---------- taxonomy ----------

func plMatchMap(m store.Matchable) map[string]any {
	return map[string]any{"match": m.Match, "matching_algorithm": m.MatchingAlgorithm, "is_insensitive": m.IsInsensitive}
}

func plTagMap(t store.Tag, owner int64) map[string]any {
	m := plMatchMap(t.Matchable)
	m["id"], m["slug"], m["name"], m["color"], m["text_color"] = t.ID, plSlug(t.Name), t.Name, t.Color, plTextColor(t.Color)
	m["is_inbox_tag"], m["document_count"], m["owner"], m["user_can_change"] = t.IsInbox, t.DocumentCount, owner, true
	m["parent"], m["children"] = nil, []any{}
	return m
}

func plNamedMap(n store.Named, kind string, owner int64) map[string]any {
	m := plMatchMap(n.Matchable)
	m["id"], m["slug"], m["name"], m["document_count"], m["owner"], m["user_can_change"] = n.ID, plSlug(n.Name), n.Name, n.DocumentCount, owner, true
	switch kind {
	case "correspondents":
		m["last_correspondence"] = nil
		if n.LastCreated != "" {
			m["last_correspondence"] = plDateTime(n.LastCreated)
		}
	case "storage_paths":
		m["path"] = n.Path
	}
	return m
}

func plFieldMap(f store.CustomField) map[string]any {
	var ex struct {
		Options  []string `json:"options"`
		Currency string   `json:"currency"`
	}
	_ = json.Unmarshal(f.Extra, &ex)
	opts := []map[string]any{}
	for _, o := range ex.Options {
		opts = append(opts, map[string]any{"id": o, "label": o})
	}
	var cur any
	if ex.Currency != "" {
		cur = ex.Currency
	}
	return map[string]any{"id": f.ID, "name": f.Name, "data_type": f.DataType, "extra_data": map[string]any{"select_options": opts, "default_currency": cur}, "document_count": f.DocumentCount}
}

func plFieldType(t string) string {
	switch t {
	case "longtext", "":
		return "string"
	}
	return t
}

func (s *Server) plTaxonomyItems(ctx context.Context, uid int64, kind string) ([]map[string]any, error) {
	out := []map[string]any{}
	switch kind {
	case "tags":
		tags, err := s.store.ListTags(ctx, uid)
		if err != nil {
			return nil, err
		}
		for _, t := range tags {
			out = append(out, plTagMap(t, uid))
		}
	case "correspondents", "document_types", "storage_paths":
		var items []store.Named
		var err error
		switch kind {
		case "correspondents":
			items, err = s.store.ListCorrespondents(ctx, uid)
		case "document_types":
			items, err = s.store.ListDocumentTypes(ctx, uid)
		default:
			items, err = s.store.ListStoragePaths(ctx, uid)
		}
		if err != nil {
			return nil, err
		}
		for _, n := range items {
			out = append(out, plNamedMap(n, kind, uid))
		}
	case "custom_fields":
		fields, err := s.store.ListCustomFields(ctx, uid)
		if err != nil {
			return nil, err
		}
		for _, f := range fields {
			out = append(out, plFieldMap(f))
		}
	}
	return out, nil
}

func (s *Server) plTaxonomyList(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.plTaxonomyItems(r.Context(), me(r), kind)
		if err != nil {
			s.fail(w, err, "list "+kind)
			return
		}
		plListMaps(w, r, items)
	}
}

// plTaxonomyGetOne returns one item of a kind as a Paperless map.
func (s *Server) plTaxonomyGetOne(ctx context.Context, uid, id int64, kind string) (map[string]any, error) {
	switch kind {
	case "tags":
		t, err := s.store.GetTag(ctx, uid, id)
		if err != nil {
			return nil, err
		}
		return plTagMap(*t, uid), nil
	case "correspondents":
		n, err := s.store.GetCorrespondent(ctx, uid, id)
		if err != nil {
			return nil, err
		}
		return plNamedMap(*n, kind, uid), nil
	case "document_types":
		n, err := s.store.GetDocumentType(ctx, uid, id)
		if err != nil {
			return nil, err
		}
		return plNamedMap(*n, kind, uid), nil
	case "storage_paths":
		n, err := s.store.GetStoragePath(ctx, uid, id)
		if err != nil {
			return nil, err
		}
		return plNamedMap(*n, kind, uid), nil
	case "custom_fields":
		f, err := s.store.GetCustomField(ctx, uid, id)
		if err != nil {
			return nil, err
		}
		return plFieldMap(*f), nil
	}
	return nil, store.ErrNotFound
}

func (s *Server) plTaxonomyGet(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := s.plTaxonomyGetOne(r.Context(), me(r), idParam(r, "id"), kind)
		if err != nil {
			s.plFail(w, err, kind)
			return
		}
		writeJSON(w, http.StatusOK, m)
	}
}

// plFail answers errors in Paperless' {"detail": ...} shape.
func (s *Server) plFail(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		plDetail(w, http.StatusNotFound, "Not found.")
	case errors.Is(err, store.ErrConflict):
		plDetail(w, http.StatusBadRequest, what+": already exists")
	default:
		s.log.Error(what, "err", err)
		plDetail(w, http.StatusInternalServerError, what+": "+err.Error())
	}
}

// plTaxonomyIn is what Paperless sends for a tag, correspondent, type, path or field.
type plTaxonomyIn struct {
	Name              string          `json:"name"`
	Color             string          `json:"color"`
	Match             string          `json:"match"`
	MatchingAlgorithm int             `json:"matching_algorithm"`
	IsInsensitive     *bool           `json:"is_insensitive"`
	IsInboxTag        bool            `json:"is_inbox_tag"`
	Path              string          `json:"path"`
	DataType          string          `json:"data_type"`
	ExtraData         json.RawMessage `json:"extra_data"`
}

func (in plTaxonomyIn) matchable() store.Matchable {
	return taxonomyIn{Match: in.Match, MatchingAlgorithm: in.MatchingAlgorithm, IsInsensitive: in.IsInsensitive}.matchable()
}

func plTaxonomyError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "needs a name") || strings.Contains(msg, "is required") || strings.Contains(msg, "unknown field type") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"name": []string{msg}})
		return true
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"name": []string{"An item with this name already exists."}})
		return true
	}
	return false
}

func (s *Server) plTaxonomyCreate(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, uid := r.Context(), me(r)
		var in plTaxonomyIn
		if err := decode(r, &in); err != nil {
			plDetail(w, http.StatusBadRequest, "invalid json")
			return
		}
		var id int64
		var err error
		switch kind {
		case "tags":
			t := &store.Tag{UserID: uid, Name: in.Name, Color: validColor(in.Color), Matchable: in.matchable(), IsInbox: in.IsInboxTag}
			err, id = s.store.CreateTag(ctx, t), t.ID
		case "correspondents":
			n := &store.Named{UserID: uid, Name: in.Name, Matchable: in.matchable()}
			err, id = s.store.CreateCorrespondent(ctx, n), n.ID
		case "document_types":
			n := &store.Named{UserID: uid, Name: in.Name, Matchable: in.matchable()}
			err, id = s.store.CreateDocumentType(ctx, n), n.ID
		case "storage_paths":
			n := &store.Named{UserID: uid, Name: in.Name, Path: strings.TrimSpace(in.Path), Matchable: in.matchable()}
			err, id = s.store.CreateStoragePath(ctx, n), n.ID
		case "custom_fields":
			f := &store.CustomField{UserID: uid, Name: in.Name, DataType: plFieldType(in.DataType), Extra: paperlessFieldExtra(in.ExtraData)}
			err, id = s.store.CreateCustomField(ctx, f), f.ID
		}
		if plTaxonomyError(w, err) {
			return
		}
		if err != nil {
			s.plFail(w, err, "create "+kind)
			return
		}
		s.classifiers.Invalidate(uid)
		m, err := s.plTaxonomyGetOne(ctx, uid, id, kind)
		if err != nil {
			s.plFail(w, err, kind)
			return
		}
		writeJSON(w, http.StatusCreated, m)
	}
}

func (s *Server) plTaxonomyUpdate(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, uid, id := r.Context(), me(r), idParam(r, "id")
		var in plTaxonomyIn
		keys, err := decodeKeys(r, &in)
		if err != nil {
			plDetail(w, http.StatusBadRequest, "invalid json")
			return
		}
		matching := func(cur store.Matchable) store.Matchable {
			if !keys["match"] && !keys["matching_algorithm"] && !keys["is_insensitive"] {
				return cur
			}
			m := in.matchable()
			if !keys["match"] {
				m.Match = cur.Match
			}
			if !keys["matching_algorithm"] {
				m.MatchingAlgorithm = cur.MatchingAlgorithm
			}
			if !keys["is_insensitive"] {
				m.IsInsensitive = cur.IsInsensitive
			}
			return m
		}
		switch kind {
		case "tags":
			t, gerr := s.store.GetTag(ctx, uid, id)
			if gerr != nil {
				s.plFail(w, gerr, "tag")
				return
			}
			if keys["name"] {
				t.Name = in.Name
			}
			if keys["color"] {
				t.Color = validColor(in.Color)
			}
			if keys["is_inbox_tag"] {
				t.IsInbox = in.IsInboxTag
			}
			t.Matchable = matching(t.Matchable)
			err = s.store.UpdateTag(ctx, t)
		case "correspondents", "document_types", "storage_paths":
			var n *store.Named
			var gerr error
			switch kind {
			case "correspondents":
				n, gerr = s.store.GetCorrespondent(ctx, uid, id)
			case "document_types":
				n, gerr = s.store.GetDocumentType(ctx, uid, id)
			default:
				n, gerr = s.store.GetStoragePath(ctx, uid, id)
			}
			if gerr != nil {
				s.plFail(w, gerr, kind)
				return
			}
			if keys["name"] {
				n.Name = in.Name
			}
			if keys["path"] {
				n.Path = strings.TrimSpace(in.Path)
			}
			n.Matchable = matching(n.Matchable)
			switch kind {
			case "correspondents":
				err = s.store.UpdateCorrespondent(ctx, n)
			case "document_types":
				err = s.store.UpdateDocumentType(ctx, n)
			default:
				err = s.store.UpdateStoragePath(ctx, n)
				if err == nil && keys["path"] {
					go s.remirrorPath(uid, id)
				}
			}
		case "custom_fields":
			f, gerr := s.store.GetCustomField(ctx, uid, id)
			if gerr != nil {
				s.plFail(w, gerr, "field")
				return
			}
			if keys["name"] {
				f.Name = in.Name
			}
			if keys["data_type"] && in.DataType != "" && f.DocumentCount == 0 {
				f.DataType = plFieldType(in.DataType)
			}
			if keys["extra_data"] {
				f.Extra = paperlessFieldExtra(in.ExtraData)
			}
			err = s.store.UpdateCustomField(ctx, f)
		}
		if plTaxonomyError(w, err) {
			return
		}
		if err != nil {
			s.plFail(w, err, "update "+kind)
			return
		}
		if keys["name"] {
			col := map[string]string{"tags": "tag", "correspondents": "correspondent", "document_types": "document_type", "storage_paths": "storage_path", "custom_fields": "field"}[kind]
			go s.store.ReindexRelated(contextBG(), col, id)
		}
		s.classifiers.Invalidate(uid)
		m, err := s.plTaxonomyGetOne(ctx, uid, id, kind)
		if err != nil {
			s.plFail(w, err, kind)
			return
		}
		writeJSON(w, http.StatusOK, m)
	}
}

func (s *Server) plTaxonomyDelete(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, uid, id := r.Context(), me(r), idParam(r, "id")
		var err error
		var ids []int64
		switch kind {
		case "tags":
			ids = s.relatedIDs(ctx, "tag", id)
			err = s.store.DeleteTag(ctx, uid, id)
		case "correspondents":
			ids = s.relatedIDs(ctx, "correspondent", id)
			err = s.store.DeleteCorrespondent(ctx, uid, id)
		case "document_types":
			ids = s.relatedIDs(ctx, "document_type", id)
			err = s.store.DeleteDocumentType(ctx, uid, id)
		case "storage_paths":
			ids = s.relatedIDs(ctx, "storage_path", id)
			err = s.store.DeleteStoragePath(ctx, uid, id)
		case "custom_fields":
			ids = s.relatedIDs(ctx, "field", id)
			err = s.store.DeleteCustomField(ctx, uid, id)
		}
		if err != nil {
			s.plFail(w, err, "delete "+kind)
			return
		}
		for _, did := range ids {
			_ = s.store.IndexDocument(ctx, did)
		}
		s.classifiers.Invalidate(uid)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------- saved views ----------

// Paperless filter rule types this facade understands.
const (
	plRuleTitle          = 0
	plRuleContent        = 1
	plRuleASN            = 2
	plRuleCorrespondent  = 3
	plRuleDocumentType   = 4
	plRuleInbox          = 5
	plRuleHasTag         = 6
	plRuleIsTagged       = 7
	plRuleCreatedBefore  = 8
	plRuleCreatedAfter   = 9
	plRuleAddedBefore    = 13
	plRuleAddedAfter     = 14
	plRuleNotTag         = 17
	plRuleNoASN          = 18
	plRuleTitleContent   = 19
	plRuleFulltext       = 20
	plRuleTagsIn         = 22
	plRuleStoragePath    = 25
	plRuleCorrespondents = 26
	plRuleDocumentTypes  = 28
	plRuleStoragePaths   = 30
)

var plSortOut = map[string]string{"asn": "archive_serial_number", "correspondent": "correspondent__name", "type": "document_type__name", "notes": "num_notes", "rank": "score"}
var plSortIn = map[string]string{"archive_serial_number": "asn", "correspondent__name": "correspondent", "document_type__name": "type", "num_notes": "notes", "score": "rank", "created": "created", "added": "added", "modified": "modified", "title": "title", "id": "id", "page_count": "pages"}

// plOrdering maps a Paperless ordering parameter to our sort key.
func plOrdering(ord string) string {
	if ord == "" {
		return ""
	}
	desc := strings.HasPrefix(ord, "-")
	key, ok := plSortIn[strings.TrimPrefix(ord, "-")]
	if !ok {
		return ""
	}
	if desc {
		return "-" + key
	}
	return key
}

// plViewMap turns one of our saved views (a query map) into Paperless' shape.
func plViewMap(v store.SavedView, owner int64) map[string]any {
	q := map[string]any{}
	_ = json.Unmarshal(v.Query, &q)
	rules := []map[string]any{}
	rule := func(t int, val any) {
		rules = append(rules, map[string]any{"rule_type": t, "value": val})
	}
	str := func(v any) string {
		switch x := v.(type) {
		case string:
			return x
		case float64:
			return trimFloat(x)
		case bool:
			if x {
				return "1"
			}
		}
		return ""
	}
	list := func(v any) []string {
		switch x := v.(type) {
		case string:
			return strings.Split(x, ",")
		case float64:
			return []string{trimFloat(x)}
		case []any:
			var out []string
			for _, e := range x {
				out = append(out, str(e))
			}
			return out
		}
		return nil
	}
	for k, val := range q {
		switch k {
		case "q":
			if s := str(val); s != "" {
				rule(plRuleFulltext, s)
			}
		case "title":
			if s := str(val); s != "" {
				rule(plRuleTitle, s)
			}
		case "tags", "tags__all":
			for _, t := range list(val) {
				if t != "" {
					rule(plRuleHasTag, t)
				}
			}
		case "tags__any":
			for _, t := range list(val) {
				if t != "" {
					rule(plRuleTagsIn, t)
				}
			}
		case "tags__none":
			for _, t := range list(val) {
				if t != "" {
					rule(plRuleNotTag, t)
				}
			}
		case "correspondent":
			if s := str(val); s == "none" {
				rule(plRuleCorrespondent, nil)
			} else {
				for _, c := range list(val) {
					if c != "" {
						rule(plRuleCorrespondents, c)
					}
				}
			}
		case "document_type":
			if s := str(val); s == "none" {
				rule(plRuleDocumentType, nil)
			} else {
				for _, c := range list(val) {
					if c != "" {
						rule(plRuleDocumentTypes, c)
					}
				}
			}
		case "storage_path":
			if s := str(val); s == "none" {
				rule(plRuleStoragePath, nil)
			} else {
				for _, c := range list(val) {
					if c != "" {
						rule(plRuleStoragePaths, c)
					}
				}
			}
		case "inbox":
			if str(val) == "1" {
				rule(plRuleInbox, "true")
			}
		case "untagged":
			if str(val) == "1" {
				rule(plRuleIsTagged, "false")
			}
		case "asn":
			if s := str(val); s != "" {
				rule(plRuleASN, s)
			}
		case "created_from":
			if s := str(val); s != "" {
				rule(plRuleCreatedAfter, plShiftDate(s, -1))
			}
		case "created_to":
			if s := str(val); s != "" {
				rule(plRuleCreatedBefore, plShiftDate(s, 1))
			}
		case "added_from":
			if s := str(val); s != "" {
				rule(plRuleAddedAfter, plShiftDate(s, -1))
			}
		case "added_to":
			if s := str(val); s != "" {
				rule(plRuleAddedBefore, plShiftDate(s, 1))
			}
		}
	}
	sortField, reverse := "created", true
	if srt := str(q["sort"]); srt != "" {
		reverse = strings.HasPrefix(srt, "-")
		key := strings.TrimPrefix(srt, "-")
		if mapped, ok := plSortOut[key]; ok {
			key = mapped
		}
		sortField = key
	}
	return map[string]any{
		"id": v.ID, "name": v.Name, "show_on_dashboard": v.OnDashboard, "show_in_sidebar": v.InSidebar,
		"sort_field": sortField, "sort_reverse": reverse, "filter_rules": rules,
		"page_size": nil, "display_mode": nil, "display_fields": nil, "owner": owner, "user_can_change": true,
	}
}

type plViewIn struct {
	Name          string `json:"name"`
	ShowDashboard bool   `json:"show_on_dashboard"`
	ShowSidebar   bool   `json:"show_in_sidebar"`
	SortField     string `json:"sort_field"`
	SortReverse   bool   `json:"sort_reverse"`
	FilterRules   []struct {
		RuleType int     `json:"rule_type"`
		Value    *string `json:"value"`
	} `json:"filter_rules"`
}

// plRulesToQuery turns Paperless filter rules into our query map (best effort).
func plRulesToQuery(in plViewIn, keys map[string]bool) map[string]any {
	q := map[string]any{}
	addList := func(k, v string) {
		cur, _ := q[k].([]any)
		q[k] = append(cur, v)
	}
	val := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	for _, r := range in.FilterRules {
		v := val(r.Value)
		switch r.RuleType {
		case plRuleTitle:
			q["title"] = v
		case plRuleContent, plRuleTitleContent, plRuleFulltext:
			q["q"] = strings.TrimSpace(strings.TrimSpace(plStr(q["q"])) + " " + v)
		case plRuleASN:
			q["asn"] = v
		case plRuleNoASN:
			if v == "true" || v == "1" {
				q["q"] = strings.TrimSpace(plStr(q["q"]) + " asn:none")
			}
		case plRuleCorrespondent:
			if r.Value == nil {
				q["correspondent"] = "none"
			} else {
				addList("correspondent", v)
			}
		case plRuleCorrespondents:
			addList("correspondent", v)
		case plRuleDocumentType:
			if r.Value == nil {
				q["document_type"] = "none"
			} else {
				addList("document_type", v)
			}
		case plRuleDocumentTypes:
			addList("document_type", v)
		case plRuleStoragePath:
			if r.Value == nil {
				q["storage_path"] = "none"
			} else {
				addList("storage_path", v)
			}
		case plRuleStoragePaths:
			addList("storage_path", v)
		case plRuleInbox:
			if v == "true" || v == "1" {
				q["inbox"] = true
			}
		case plRuleHasTag:
			addList("tags", v)
		case plRuleTagsIn:
			addList("tags__any", v)
		case plRuleNotTag:
			addList("tags__none", v)
		case plRuleIsTagged:
			if v == "false" || v == "0" {
				q["untagged"] = true
			}
		case plRuleCreatedAfter:
			q["created_from"] = plShiftDate(plDate(v), 1)
		case plRuleCreatedBefore:
			q["created_to"] = plShiftDate(plDate(v), -1)
		case plRuleAddedAfter:
			q["added_from"] = plShiftDate(plDate(v), 1)
		case plRuleAddedBefore:
			q["added_to"] = plShiftDate(plDate(v), -1)
		}
	}
	if keys["sort_field"] || keys["sort_reverse"] {
		key := in.SortField
		if mapped, ok := plSortIn[key]; ok {
			key = mapped
		}
		if key != "" {
			if in.SortReverse {
				key = "-" + key
			}
			q["sort"] = key
		}
	}
	return q
}

func (s *Server) plViewsList(w http.ResponseWriter, r *http.Request) {
	uid := me(r)
	views, err := s.store.ListSavedViews(r.Context(), uid)
	if err != nil {
		s.plFail(w, err, "views")
		return
	}
	items := []map[string]any{}
	for _, v := range views {
		items = append(items, plViewMap(v, uid))
	}
	plListMaps(w, r, items)
}

func (s *Server) plViewGet(w http.ResponseWriter, r *http.Request) {
	uid := me(r)
	v, err := s.store.GetSavedView(r.Context(), uid, idParam(r, "id"))
	if err != nil {
		s.plFail(w, err, "view")
		return
	}
	writeJSON(w, http.StatusOK, plViewMap(*v, uid))
}

func (s *Server) plViewCreate(w http.ResponseWriter, r *http.Request) {
	uid := me(r)
	var in plViewIn
	keys, err := decodeKeys(r, &in)
	if err != nil {
		plDetail(w, http.StatusBadRequest, "invalid json")
		return
	}
	qb, _ := json.Marshal(plRulesToQuery(in, keys))
	v := store.SavedView{UserID: uid, Name: in.Name, Query: qb, OnDashboard: in.ShowDashboard, InSidebar: in.ShowSidebar}
	if err := s.store.CreateSavedView(r.Context(), &v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"name": []string{err.Error()}})
		return
	}
	writeJSON(w, http.StatusCreated, plViewMap(v, uid))
}

func (s *Server) plViewUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	v, err := s.store.GetSavedView(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.plFail(w, err, "view")
		return
	}
	var in plViewIn
	keys, err := decodeKeys(r, &in)
	if err != nil {
		plDetail(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["name"] {
		v.Name = in.Name
	}
	if keys["show_on_dashboard"] {
		v.OnDashboard = in.ShowDashboard
	}
	if keys["show_in_sidebar"] {
		v.InSidebar = in.ShowSidebar
	}
	if keys["filter_rules"] || keys["sort_field"] || keys["sort_reverse"] {
		q := map[string]any{}
		_ = json.Unmarshal(v.Query, &q)
		fresh := plRulesToQuery(in, keys)
		if keys["filter_rules"] {
			srt := q["sort"]
			q = fresh
			if _, ok := fresh["sort"]; !ok && srt != nil {
				q["sort"] = srt
			}
		} else if srt, ok := fresh["sort"]; ok {
			q["sort"] = srt
		}
		v.Query, _ = json.Marshal(q)
	}
	if err := s.store.UpdateSavedView(ctx, v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"name": []string{err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, plViewMap(*v, uid))
}

func (s *Server) plViewDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSavedView(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.plFail(w, err, "delete view")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- documents ----------

// plFieldValue types a stored string the way Paperless would.
func plFieldValue(f store.CustomField, v string) any {
	switch f.DataType {
	case "integer":
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	case "float":
		if x, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return x
		}
	case "boolean":
		b, _ := plBoolParam(v)
		return b
	case "documentlink":
		var ids []int64
		if json.Unmarshal([]byte(v), &ids) == nil {
			return ids
		}
		return plInts([]string{v})
	}
	if v == "" {
		return nil
	}
	return v
}

// plFieldString flattens whatever a client sends for a field value.
func plFieldString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return trimFloat(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case []any:
		b, _ := json.Marshal(x)
		return string(b)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// plDocMap renders a document in Paperless' shape.
func (s *Server) plDocMap(ctx context.Context, u *store.User, d *store.Document, names store.NameMaps, withContent bool, truncate bool) map[string]any {
	notes := []map[string]any{}
	if d.NotesCount > 0 {
		if list, err := s.store.ListNotes(ctx, d.ID); err == nil {
			for _, n := range list {
				notes = append(notes, plNoteMap(n, u))
			}
		}
	}
	fields := []map[string]any{}
	for _, fv := range d.Fields {
		var val any = fv.Value
		if f, ok := names.Fields[fv.FieldID]; ok {
			val = plFieldValue(f, fv.Value)
		}
		fields = append(fields, map[string]any{"field": fv.FieldID, "value": val})
	}
	tags := d.Tags
	if tags == nil {
		tags = []int64{}
	}
	var archived any
	if d.HasArchive {
		archived = safeFileName(d.Title) + ".pdf"
	}
	var deleted any
	if d.DeletedAt != nil {
		deleted = plStamp(*d.DeletedAt)
	}
	out := map[string]any{
		"id":                     d.ID,
		"correspondent":          plIntOrNil(d.CorrespondentID),
		"document_type":          plIntOrNil(d.DocumentTypeID),
		"storage_path":           plIntOrNil(d.StoragePathID),
		"title":                  d.Title,
		"tags":                   tags,
		"created":                plDateTime(d.Created),
		"created_date":           d.Created,
		"modified":               plStamp(d.ModifiedAt),
		"added":                  plStamp(d.AddedAt),
		"deleted_at":             deleted,
		"archive_serial_number":  plIntOrNil(d.ASN),
		"original_file_name":     d.OriginalName,
		"archived_file_name":     archived,
		"owner":                  u.ID,
		"user_can_change":        true,
		"is_shared_by_requester": false,
		"notes":                  notes,
		"custom_fields":          fields,
		"page_count":             d.PageCount,
		"mime_type":              firstNonEmpty(d.Mime, "application/octet-stream"),
	}
	if withContent {
		c := d.Content
		if truncate && len(c) > 500 {
			c = string([]rune(c)[:min(500, len([]rune(c)))])
		}
		out["content"] = c
	}
	if d.Snippet != "" {
		out["__search_hit__"] = map[string]any{"score": d.Rank, "highlights": d.Snippet, "note_highlights": "", "rank": 0}
	}
	return out
}

func plNoteMap(n store.Note, u *store.User) map[string]any {
	return map[string]any{"id": n.ID, "note": n.Text, "created": plStamp(n.CreatedAt), "user": map[string]any{"id": u.ID, "username": u.Email, "first_name": u.Name, "last_name": ""}}
}

// plDocFilter builds the store filter from Paperless' query parameters.
func (s *Server) plDocFilter(r *http.Request) store.DocFilter {
	ctx, uid := r.Context(), me(r)
	q := r.URL.Query()
	f := store.DocFilter{UserID: uid, NoFacets: true, Fields: map[int64]string{}}
	text := strings.TrimSpace(strings.Join([]string{q.Get("query"), q.Get("title_content"), q.Get("content__icontains")}, " "))
	if text != "" {
		f.Query, f.FTS = text, store.FTSQuery(text)
	}
	f.TitleContains = firstNonEmpty(q.Get("title__icontains"), q.Get("title__istartswith"), q.Get("title__iexact"))
	f.Tags = plInts(q["tags__id__all"])
	f.AnyTags = plInts(q["tags__id__in"])
	if v := q.Get("tags__id"); v != "" {
		f.Tags = append(f.Tags, plInts([]string{v})...)
	}
	f.NotTags = plInts(q["tags__id__none"])
	names := s.store.Names(ctx, uid)
	allIDs := func(m map[int64]string) []int64 {
		out := make([]int64, 0, len(m))
		for id := range m {
			out = append(out, id)
		}
		if len(out) == 0 {
			out = []int64{-1}
		}
		return out
	}
	if b, ok := plBoolParam(q.Get("is_in_inbox")); ok {
		if b {
			f.Inbox = true
		} else {
			f.NotTags = append(f.NotTags, s.store.InboxTagIDs(ctx, uid)...)
		}
	}
	if b, ok := plBoolParam(q.Get("is_tagged")); ok {
		if b {
			f.AnyTags = append(f.AnyTags, allIDs(names.Tags)...)
		} else {
			f.Untagged = true
		}
	}
	f.Correspondents = append(plInts(q["correspondent__id"]), plInts(q["correspondent__id__in"])...)
	if b, ok := plBoolParam(q.Get("correspondent__isnull")); ok {
		if b {
			f.NoCorrespondent = true
		} else {
			f.Correspondents = append(f.Correspondents, allIDs(names.Correspondents)...)
		}
	}
	f.Types = append(plInts(q["document_type__id"]), plInts(q["document_type__id__in"])...)
	if b, ok := plBoolParam(q.Get("document_type__isnull")); ok {
		if b {
			f.NoType = true
		} else {
			f.Types = append(f.Types, allIDs(names.Types)...)
		}
	}
	f.Paths = append(plInts(q["storage_path__id"]), plInts(q["storage_path__id__in"])...)
	if b, ok := plBoolParam(q.Get("storage_path__isnull")); ok {
		if b {
			f.NoPath = true
		} else {
			f.Paths = append(f.Paths, allIDs(names.Paths)...)
		}
	}
	if v := q.Get("archive_serial_number"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.ASN = &n
		}
	}
	if v := q.Get("archive_serial_number__gt"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			n++
			f.ASNFrom = &n
		}
	}
	if v := q.Get("archive_serial_number__gte"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.ASNFrom = &n
		}
	}
	if v := q.Get("archive_serial_number__lt"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			n--
			f.ASNTo = &n
		}
	}
	if v := q.Get("archive_serial_number__lte"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.ASNTo = &n
		}
	}
	if b, ok := plBoolParam(q.Get("archive_serial_number__isnull")); ok {
		has := !b
		f.HasASN = &has
	}
	date := func(prefix string) (from, to string) {
		if v := q.Get(prefix + "__date__gt"); v != "" {
			from = plShiftDate(plDate(v), 1)
		}
		if v := q.Get(prefix + "__date__gte"); v != "" {
			from = plDate(v)
		}
		if v := q.Get(prefix + "__gt"); v != "" && from == "" {
			from = plShiftDate(plDate(v), 1)
		}
		if v := q.Get(prefix + "__gte"); v != "" && from == "" {
			from = plDate(v)
		}
		if v := q.Get(prefix + "__date__lt"); v != "" {
			to = plShiftDate(plDate(v), -1)
		}
		if v := q.Get(prefix + "__date__lte"); v != "" {
			to = plDate(v)
		}
		if v := q.Get(prefix + "__lt"); v != "" && to == "" {
			to = plShiftDate(plDate(v), -1)
		}
		if v := q.Get(prefix + "__lte"); v != "" && to == "" {
			to = plDate(v)
		}
		return
	}
	f.CreatedFrom, f.CreatedTo = date("created")
	f.AddedFrom, f.AddedTo = date("added")
	if v := q.Get("created__year"); v != "" {
		f.CreatedFrom, f.CreatedTo = v+"-01-01", v+"-12-31"
		if m := q.Get("created__month"); m != "" {
			if n, err := strconv.Atoi(m); err == nil {
				first := time.Date(atoiDefault(v, 2000), time.Month(n), 1, 0, 0, 0, 0, time.UTC)
				f.CreatedFrom, f.CreatedTo = first.Format("2006-01-02"), first.AddDate(0, 1, -1).Format("2006-01-02")
			}
		}
	}
	f.IDs = plInts(q["id__in"])
	if v := q.Get("id"); v != "" {
		f.IDs = append(f.IDs, plInts([]string{v})...)
	}
	if v := q.Get("mime_type"); v != "" {
		f.Mime = v
	}
	if v := q.Get("custom_fields__id__all"); v != "" {
		for _, id := range plInts([]string{v}) {
			f.Fields[id] = ""
		}
	}
	if v := q.Get("custom_fields__id__in"); v != "" {
		for _, id := range plInts([]string{v}) {
			f.Fields[id] = ""
		}
	}
	f.NoField = plInts(q["custom_fields__id__none"])
	if b, ok := plBoolParam(q.Get("has_custom_fields")); ok && !b {
		for id := range names.Fields {
			f.NoField = append(f.NoField, id)
		}
	}
	f.Sort = plOrdering(q.Get("ordering"))
	if f.Sort == "" && f.FTS == "" {
		f.Sort = "-created"
	}
	p := plPaging(r)
	f.Limit, f.Offset = p.size, p.offset
	f.WithContent = true
	if fields := q.Get("fields"); fields != "" && !strings.Contains(","+fields+",", ",content,") {
		f.WithContent = false
	}
	return f
}

func atoiDefault(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

// plAllIDs lists every id the filter matches (Paperless' "all" for select-all).
func (s *Server) plAllIDs(ctx context.Context, f store.DocFilter, page []store.Document, total int) []int64 {
	if total <= len(page) && f.Offset == 0 {
		ids := make([]int64, 0, len(page))
		for _, d := range page {
			ids = append(ids, d.ID)
		}
		return ids
	}
	f.WithContent, f.Limit, f.Offset = false, 500, 0
	ids := []int64{}
	for f.Offset < 10000 {
		docs, _, _, err := s.store.ListDocuments(ctx, f)
		if err != nil || len(docs) == 0 {
			break
		}
		for _, d := range docs {
			ids = append(ids, d.ID)
		}
		if len(docs) < f.Limit {
			break
		}
		f.Offset += f.Limit
	}
	return ids
}

func (s *Server) plDocumentsList(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userFrom(r.Context())
	f := s.plDocFilter(r)
	docs, total, _, err := s.store.ListDocuments(ctx, f)
	if err != nil {
		s.plFail(w, err, "list documents")
		return
	}
	names := s.store.Names(ctx, u.ID)
	truncate := queryBool(r, "truncate_content")
	results := make([]map[string]any, 0, len(docs))
	for i := range docs {
		results = append(results, s.plDocMap(ctx, u, &docs[i], names, f.WithContent, truncate))
	}
	writeJSON(w, http.StatusOK, plListResponse(r, plPaging(r), total, results, s.plAllIDs(ctx, f, docs, total)))
}

func (s *Server) plDocumentGet(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userFrom(r.Context())
	d, err := s.store.GetDocument(ctx, u.ID, idParam(r, "id"), true)
	if err != nil {
		s.plFail(w, err, "document")
		return
	}
	writeJSON(w, http.StatusOK, s.plDocMap(ctx, u, d, s.store.Names(ctx, u.ID), true, false))
}

// plDocumentIn is Paperless' document body; the custom field values are typed.
type plDocumentIn struct {
	Title           *string  `json:"title"`
	Content         *string  `json:"content"`
	Correspondent   *int64   `json:"correspondent"`
	DocumentType    *int64   `json:"document_type"`
	StoragePath     *int64   `json:"storage_path"`
	ASN             *int64   `json:"archive_serial_number"`
	Created         *string  `json:"created"`
	CreatedDate     *string  `json:"created_date"`
	Tags            *[]int64 `json:"tags"`
	RemoveInboxTags bool     `json:"remove_inbox_tags"`
	CustomFields    *[]struct {
		Field int64 `json:"field"`
		Value any   `json:"value"`
	} `json:"custom_fields"`
}

func (s *Server) plDocumentUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, u, id := r.Context(), userFrom(r.Context()), idParam(r, "id")
	if _, err := s.store.GetDocument(ctx, u.ID, id, false); err != nil {
		s.plFail(w, err, "document")
		return
	}
	var in plDocumentIn
	keys, err := decodeKeys(r, &in)
	if err != nil {
		plDetail(w, http.StatusBadRequest, "invalid json")
		return
	}
	din := documentIn{Title: in.Title, Content: in.Content, Correspondent: in.Correspondent, DocumentType: in.DocumentType, StoragePath: in.StoragePath, ASN: in.ASN, Tags: in.Tags}
	if keys["archive_serial_number"] {
		keys["asn"] = true
	}
	if in.CreatedDate != nil && *in.CreatedDate != "" {
		din.Created = in.CreatedDate
	} else if in.Created != nil && *in.Created != "" {
		din.Created = in.Created
	}
	if in.CustomFields != nil {
		fields := []store.FieldValue{}
		for _, fv := range *in.CustomFields {
			fields = append(fields, store.FieldValue{FieldID: fv.Field, Value: plFieldString(fv.Value)})
		}
		din.CustomFields = &fields
	}
	c := s.changesFrom(keys, din)
	if in.RemoveInboxTags {
		c.RemoveTags = s.store.InboxTagIDs(ctx, u.ID)
	}
	if err := s.applyChanges(ctx, u.ID, id, c, actorName(r), "updated"); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"archive_serial_number": []string{"Archive serial number must be unique."}})
			return
		}
		s.plFail(w, err, "update document")
		return
	}
	d, err := s.store.GetDocument(ctx, u.ID, id, true)
	if err != nil {
		s.plFail(w, err, "document")
		return
	}
	writeJSON(w, http.StatusOK, s.plDocMap(ctx, u, d, s.store.Names(ctx, u.ID), true, false))
}

// plSoftDelete moves a document to the trash the way the app does.
func (s *Server) plSoftDelete(ctx context.Context, uid, id int64, actor string) error {
	d, err := s.store.GetDocument(ctx, uid, id, false)
	if err != nil {
		return err
	}
	if err := s.store.SoftDeleteDocument(ctx, uid, id); err != nil {
		return err
	}
	s.unplace(ctx, d)
	s.store.AddHistory(ctx, id, uid, actor, "deleted", nil)
	s.classifiers.Invalidate(uid)
	s.publish(uid, "documents.deleted", "document:"+strconv.FormatInt(id, 10), map[string]any{"id": id, "title": d.Title})
	return nil
}

func (s *Server) plDocumentDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.plSoftDelete(r.Context(), me(r), idParam(r, "id"), actorName(r)); err != nil {
		s.plFail(w, err, "delete document")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) plDocForFile(w http.ResponseWriter, r *http.Request) *store.Document {
	d, err := s.store.GetDocument(r.Context(), me(r), idParam(r, "id"), false)
	if err != nil {
		s.plFail(w, err, "document")
		return nil
	}
	return d
}

func (s *Server) plDownload(w http.ResponseWriter, r *http.Request) {
	d := s.plDocForFile(w, r)
	if d == nil {
		return
	}
	if queryBool(r, "original") || !d.HasArchive {
		name := safeFileName(d.Title)
		if d.OriginalExt != "" {
			name += "." + d.OriginalExt
		}
		serveFile(w, r, s.originalPath(d), name, firstNonEmpty(d.Mime, "application/octet-stream"), true)
		return
	}
	path, name, ctype := s.bestFile(d)
	serveFile(w, r, path, name, ctype, true)
}

func (s *Server) plPreview(w http.ResponseWriter, r *http.Request) {
	d := s.plDocForFile(w, r)
	if d == nil {
		return
	}
	if queryBool(r, "original") {
		name := safeFileName(d.Title)
		if d.OriginalExt != "" {
			name += "." + d.OriginalExt
		}
		serveFile(w, r, s.originalPath(d), name, firstNonEmpty(d.Mime, "application/octet-stream"), false)
		return
	}
	path, name, ctype := s.bestFile(d)
	serveFile(w, r, path, name, ctype, false)
}

func (s *Server) plThumb(w http.ResponseWriter, r *http.Request) {
	d := s.plDocForFile(w, r)
	if d == nil {
		return
	}
	s.serveThumb(w, r, d)
}

func (s *Server) plMetadata(w http.ResponseWriter, r *http.Request) {
	d := s.plDocForFile(w, r)
	if d == nil {
		return
	}
	var archiveSum, archiveName any
	var archiveSize any
	if d.HasArchive {
		archiveSum, archiveName, archiveSize = d.ArchiveChecksum, strconv.FormatInt(d.ID, 10)+".pdf", d.ArchiveSize
	}
	media := filepath.Base(s.originalPath(d))
	writeJSON(w, http.StatusOK, map[string]any{
		"original_checksum":      d.Checksum,
		"original_size":          d.Size,
		"original_mime_type":     firstNonEmpty(d.Mime, "application/octet-stream"),
		"media_filename":         media,
		"has_archive_version":    d.HasArchive,
		"original_metadata":      []any{},
		"archive_checksum":       archiveSum,
		"archive_media_filename": archiveName,
		"original_filename":      d.OriginalName,
		"lang":                   firstNonEmpty(d.Language, "en"),
		"archive_size":           archiveSize,
		"archive_metadata":       []any{},
	})
}

func (s *Server) plSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), true)
	if err != nil {
		s.plFail(w, err, "document")
		return
	}
	sg := s.suggestFor(ctx, uid, d)
	writeJSON(w, http.StatusOK, map[string]any{"correspondents": sg.Correspondents, "tags": sg.Tags, "document_types": sg.DocumentTypes, "storage_paths": sg.StoragePaths, "dates": sg.Dates})
}

func (s *Server) plNotesOf(ctx context.Context, u *store.User, id int64) []map[string]any {
	out := []map[string]any{}
	if list, err := s.store.ListNotes(ctx, id); err == nil {
		for _, n := range list {
			out = append(out, plNoteMap(n, u))
		}
	}
	return out
}

func (s *Server) plNotesList(w http.ResponseWriter, r *http.Request) {
	d := s.plDocForFile(w, r)
	if d == nil {
		return
	}
	writeJSON(w, http.StatusOK, s.plNotesOf(r.Context(), userFrom(r.Context()), d.ID))
}

func (s *Server) plNoteAdd(w http.ResponseWriter, r *http.Request) {
	d := s.plDocForFile(w, r)
	if d == nil {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		plDetail(w, http.StatusBadRequest, "invalid json")
		return
	}
	u := userFrom(r.Context())
	n := &store.Note{DocumentID: d.ID, UserID: u.ID, Text: in.Note}
	if err := s.store.AddNote(r.Context(), n); err != nil {
		plDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.store.AddHistory(r.Context(), d.ID, u.ID, actorName(r), "note", map[string]any{"text": n.Text})
	writeJSON(w, http.StatusOK, s.plNotesOf(r.Context(), u, d.ID))
}

func (s *Server) plNoteDelete(w http.ResponseWriter, r *http.Request) {
	d := s.plDocForFile(w, r)
	if d == nil {
		return
	}
	nid, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err := s.store.DeleteNote(r.Context(), d.ID, nid); err != nil {
		s.plFail(w, err, "delete note")
		return
	}
	writeJSON(w, http.StatusOK, s.plNotesOf(r.Context(), userFrom(r.Context()), d.ID))
}

func (s *Server) plHistory(w http.ResponseWriter, r *http.Request) {
	d := s.plDocForFile(w, r)
	if d == nil {
		return
	}
	u := userFrom(r.Context())
	entries, err := s.store.ListHistory(r.Context(), d.ID, 300)
	if err != nil {
		s.plFail(w, err, "history")
		return
	}
	out := []map[string]any{}
	for _, e := range entries {
		action := "update"
		switch e.Action {
		case "added":
			action = "create"
		case "deleted":
			action = "delete"
		}
		changes := map[string]any{}
		var raw map[string]json.RawMessage
		if json.Unmarshal(e.Changes, &raw) == nil {
			for k, v := range raw {
				var pair []any
				if json.Unmarshal(v, &pair) == nil && len(pair) == 2 {
					changes[k] = map[string]any{"type": "string", "old": pair[0], "new": pair[1]}
				} else {
					var x any
					_ = json.Unmarshal(v, &x)
					changes[k] = map[string]any{"type": "string", "old": nil, "new": x}
				}
			}
		}
		out = append(out, map[string]any{"id": e.ID, "timestamp": plStamp(e.CreatedAt), "action": action, "changes": changes, "actor": map[string]any{"id": u.ID, "username": e.Actor}})
	}
	writeJSON(w, http.StatusOK, out)
}

// plPostDocument is Paperless' upload: multipart with "document" plus
// metadata fields; the answer is the task id as a JSON string.
func (s *Server) plPostDocument(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		plDetail(w, http.StatusBadRequest, "send multipart/form-data with a \"document\" field")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	file, hdr, err := r.FormFile("document")
	if err != nil {
		plDetail(w, http.StatusBadRequest, "no file in the \"document\" field")
		return
	}
	defer file.Close()
	name := filepath.Base(hdr.Filename)
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	tmp, _, _, err := s.saveUpload(file, firstNonEmpty(ext, "bin"))
	if err != nil {
		plDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	fv := func(k string) string { return strings.TrimSpace(r.FormValue(k)) }
	in := incoming{Path: tmp, Name: name, Title: fv("title"), Source: "api", Actor: actorName(r), Tags: plInts(r.MultipartForm.Value["tags"])}
	if v := fv("created"); v != "" {
		in.Created = plDate(v)
	}
	parse := func(k string) *int64 {
		if v := fv(k); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return &n
			}
		}
		return nil
	}
	in.ASN, in.Correspondent, in.DocumentType, in.StoragePath = parse("archive_serial_number"), parse("correspondent"), parse("document_type"), parse("storage_path")
	for _, id := range plInts(r.MultipartForm.Value["custom_fields"]) {
		in.Fields = append(in.Fields, store.FieldValue{FieldID: id})
	}
	d, err := s.ingest(ctx, uid, in)
	if err != nil {
		var dup *ErrDuplicate
		if errors.As(err, &dup) {
			plDetail(w, http.StatusConflict, err.Error())
			return
		}
		plDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	var jobID int64
	_ = s.store.DB.QueryRowContext(ctx, `SELECT id FROM jobs WHERE document_id = ? AND kind = 'process' ORDER BY id DESC LIMIT 1`, d.ID).Scan(&jobID)
	if jobID == 0 {
		jobID = d.ID
	}
	writeJSON(w, http.StatusOK, strconv.FormatInt(jobID, 10))
}

// plBulkEdit applies Paperless' bulk operations document by document.
func (s *Server) plBulkEdit(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	var in struct {
		Documents  []int64        `json:"documents"`
		Method     string         `json:"method"`
		Parameters map[string]any `json:"parameters"`
	}
	if err := decode(r, &in); err != nil {
		plDetail(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(in.Documents) == 0 {
		plDetail(w, http.StatusBadRequest, "no documents chosen")
		return
	}
	if len(in.Documents) > 2000 {
		plDetail(w, http.StatusBadRequest, "too many at once")
		return
	}
	p := in.Parameters
	if p == nil {
		p = map[string]any{}
	}
	pInt := func(k string) *int64 {
		v, ok := p[k]
		if !ok || v == nil {
			return nil
		}
		n := plInt(v)
		return &n
	}
	pInts := func(k string) []int64 {
		var out []int64
		switch x := p[k].(type) {
		case []any:
			for _, e := range x {
				out = append(out, plInt(e))
			}
		case float64:
			out = append(out, int64(x))
		case map[string]any:
			for id := range x {
				if n, err := strconv.ParseInt(id, 10, 64); err == nil {
					out = append(out, n)
				}
			}
		}
		return out
	}
	actor := actorName(r)
	apply := func(id int64) error {
		c := store.DocChanges{}
		switch in.Method {
		case "set_correspondent":
			v := pInt("correspondent")
			c.CorrespondentID = &v
		case "set_document_type":
			v := pInt("document_type")
			c.DocumentTypeID = &v
		case "set_storage_path":
			v := pInt("storage_path")
			c.StoragePathID = &v
		case "add_tag":
			if t := pInt("tag"); t != nil {
				c.AddTags = []int64{*t}
			}
		case "remove_tag":
			if t := pInt("tag"); t != nil {
				c.RemoveTags = []int64{*t}
			}
		case "modify_tags":
			c.AddTags, c.RemoveTags = pInts("add_tags"), pInts("remove_tags")
		case "modify_custom_fields":
			switch x := p["add_custom_fields"].(type) {
			case map[string]any:
				for k, v := range x {
					if n, err := strconv.ParseInt(k, 10, 64); err == nil {
						c.SetFields = append(c.SetFields, store.FieldValue{FieldID: n, Value: plFieldString(v)})
					}
				}
			default:
				for _, fid := range pInts("add_custom_fields") {
					c.SetFields = append(c.SetFields, store.FieldValue{FieldID: fid})
				}
			}
			c.RemoveFields = pInts("remove_custom_fields")
		case "delete":
			return s.plSoftDelete(ctx, uid, id, actor)
		case "reprocess", "redo_ocr":
			d, err := s.store.GetDocument(ctx, uid, id, false)
			if err != nil {
				return err
			}
			mode := ""
			if in.Method == "redo_ocr" {
				mode = "redo"
			}
			_, err = s.enqueueProcess(ctx, d, "reprocess", map[string]any{"mode": mode, "actor": actor})
			return err
		case "set_permissions":
			_, err := s.store.GetDocument(ctx, uid, id, false)
			return err
		}
		return s.applyChanges(ctx, uid, id, c, actor, "bulk")
	}
	switch in.Method {
	case "set_correspondent", "set_document_type", "set_storage_path", "add_tag", "remove_tag", "modify_tags", "modify_custom_fields", "delete", "reprocess", "redo_ocr", "set_permissions":
	case "rotate", "merge", "split", "delete_pages", "edit_pdf":
		plDetail(w, http.StatusBadRequest, in.Method+" is not supported here")
		return
	default:
		plDetail(w, http.StatusBadRequest, "unknown method "+in.Method)
		return
	}
	for _, id := range in.Documents {
		if err := apply(id); err != nil {
			s.log.Warn("paperless bulk_edit", "method", in.Method, "id", id, "err", err)
			if errors.Is(err, store.ErrNotFound) {
				plDetail(w, http.StatusNotFound, "Not found.")
				return
			}
		}
	}
	s.classifiers.Invalidate(uid)
	writeJSON(w, http.StatusOK, map[string]string{"result": "OK"})
}

func (s *Server) plNextASN(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.NextASN(r.Context(), me(r)))
}

func (s *Server) plSelectionData(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	var in struct {
		Documents []int64 `json:"documents"`
	}
	if err := decode(r, &in); err != nil {
		plDetail(w, http.StatusBadRequest, "invalid json")
		return
	}
	corr, types, paths, tags, fields := map[int64]int{}, map[int64]int{}, map[int64]int{}, map[int64]int{}, map[int64]int{}
	for _, id := range in.Documents {
		d, err := s.store.GetDocument(ctx, uid, id, false)
		if err != nil {
			continue
		}
		if d.CorrespondentID != nil {
			corr[*d.CorrespondentID]++
		}
		if d.DocumentTypeID != nil {
			types[*d.DocumentTypeID]++
		}
		if d.StoragePathID != nil {
			paths[*d.StoragePathID]++
		}
		for _, t := range d.Tags {
			tags[t]++
		}
		for _, f := range d.Fields {
			fields[f.FieldID]++
		}
	}
	counts := func(m map[int64]int) []map[string]any {
		out := []map[string]any{}
		ids := make([]int64, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			out = append(out, map[string]any{"id": id, "document_count": m[id]})
		}
		return out
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"selected_correspondents": counts(corr), "selected_tags": counts(tags), "selected_document_types": counts(types),
		"selected_storage_paths": counts(paths), "selected_custom_fields": counts(fields),
	})
}

// ---------- tasks ----------

func plTaskMap(j store.Job) map[string]any {
	status := map[string]string{"queued": "PENDING", "running": "STARTED", "done": "SUCCESS", "failed": "FAILURE", "cancelled": "REVOKED"}[j.Status]
	if status == "" {
		status = "PENDING"
	}
	var result any
	var related any
	if j.DocumentID != nil {
		related = strconv.FormatInt(*j.DocumentID, 10)
	}
	switch j.Status {
	case "done":
		if j.DocumentID != nil {
			result = "Success. New document id " + strconv.FormatInt(*j.DocumentID, 10) + " created"
		} else {
			result = "Success"
		}
	case "failed":
		result = j.Error
	}
	_, acked := plAcked.Load(j.ID)
	name := j.DocumentTitle
	if name == "" {
		name = j.Kind
	}
	return map[string]any{
		"id": j.ID, "task_id": strconv.FormatInt(j.ID, 10), "task_file_name": name, "task_name": "consume_file",
		"date_created": plStamp(j.CreatedAt), "date_done": plStampPtr(j.FinishedAt), "type": "file", "status": status,
		"result": result, "acknowledged": acked, "related_document": related, "owner": j.UserID,
	}
}

func (s *Server) plTasks(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs(r.Context(), me(r), "", 200)
	if err != nil {
		s.plFail(w, err, "tasks")
		return
	}
	want := r.URL.Query().Get("task_id")
	out := []map[string]any{}
	for _, j := range jobs {
		if j.Kind != "process" && j.Kind != "reprocess" && j.Kind != "import_paperless" {
			continue
		}
		if want != "" && strconv.FormatInt(j.ID, 10) != want {
			continue
		}
		if b, ok := plBoolParam(r.URL.Query().Get("acknowledged")); ok {
			if _, acked := plAcked.Load(j.ID); acked != b {
				continue
			}
		}
		out = append(out, plTaskMap(j))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) plTask(w http.ResponseWriter, r *http.Request) {
	j, err := s.store.GetJob(r.Context(), idParam(r, "id"))
	if err != nil || j.UserID != me(r) {
		plDetail(w, http.StatusNotFound, "Not found.")
		return
	}
	writeJSON(w, http.StatusOK, plTaskMap(*j))
}

func (s *Server) plAcknowledgeTasks(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tasks []int64 `json:"tasks"`
	}
	if err := decode(r, &in); err != nil {
		plDetail(w, http.StatusBadRequest, "invalid json")
		return
	}
	n := 0
	for _, id := range in.Tasks {
		if j, err := s.store.GetJob(r.Context(), id); err == nil && j.UserID == me(r) {
			plAcked.Store(id, true)
			n++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"result": n})
}

// ---------- search ----------

func (s *Server) plAutocomplete(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	term := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("term")))
	limit := queryInt(r, "limit")
	if limit <= 0 {
		limit = 10
	}
	out := []string{}
	if term == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	seen := map[string]bool{}
	add := func(v string) {
		k := strings.ToLower(v)
		if v == "" || seen[k] || len(out) >= limit {
			return
		}
		seen[k] = true
		out = append(out, v)
	}
	names := s.store.Names(ctx, uid)
	for _, m := range []map[int64]string{names.Tags, names.Correspondents, names.Types, names.Paths} {
		var sorted []string
		for _, n := range m {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, n := range sorted {
			if strings.HasPrefix(strings.ToLower(n), term) {
				add(n)
			}
		}
	}
	if len(out) < limit {
		f := store.DocFilter{UserID: uid, Query: term, FTS: store.FTSQuery(term), Limit: 10, NoFacets: true, WithContent: true}
		if docs, _, _, err := s.store.ListDocuments(ctx, f); err == nil {
			freq := map[string]int{}
			for _, d := range docs {
				for _, t := range store.Tokens(d.Title + "\n" + d.Content) {
					if strings.HasPrefix(t, term) {
						freq[t]++
					}
				}
			}
			var words []string
			for t := range freq {
				words = append(words, t)
			}
			sort.Slice(words, func(i, j int) bool {
				if freq[words[i]] != freq[words[j]] {
					return freq[words[i]] > freq[words[j]]
				}
				return words[i] < words[j]
			})
			for _, t := range words {
				add(t)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) plSearch(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userFrom(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	docs := []map[string]any{}
	total := 0
	tags, corr, types, paths, fields, views := []map[string]any{}, []map[string]any{}, []map[string]any{}, []map[string]any{}, []map[string]any{}, []map[string]any{}
	if query != "" {
		f := store.DocFilter{UserID: u.ID, Query: query, FTS: store.FTSQuery(query), Limit: 3, NoFacets: true, WithContent: true}
		if list, n, _, err := s.store.ListDocuments(ctx, f); err == nil {
			names := s.store.Names(ctx, u.ID)
			for i := range list {
				docs = append(docs, s.plDocMap(ctx, u, &list[i], names, true, true))
			}
			total = n
		}
		match := func(items []map[string]any) []map[string]any {
			out := plKeep(items, func(m map[string]any) bool {
				return strings.Contains(strings.ToLower(plStr(m["name"])), strings.ToLower(query))
			})
			if len(out) > 3 {
				out = out[:3]
			}
			total += len(out)
			return out
		}
		for kind, into := range map[string]*[]map[string]any{"tags": &tags, "correspondents": &corr, "document_types": &types, "storage_paths": &paths, "custom_fields": &fields} {
			if items, err := s.plTaxonomyItems(ctx, u.ID, kind); err == nil {
				*into = match(items)
			}
		}
		if list, err := s.store.ListSavedViews(ctx, u.ID); err == nil {
			items := []map[string]any{}
			for _, v := range list {
				items = append(items, plViewMap(v, u.ID))
			}
			views = match(items)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "documents": docs, "saved_views": views, "correspondents": corr, "document_types": types, "storage_paths": paths,
		"tags": tags, "users": []any{}, "groups": []any{}, "mail_accounts": []any{}, "mail_rules": []any{}, "custom_fields": fields, "workflows": []any{},
	})
}
