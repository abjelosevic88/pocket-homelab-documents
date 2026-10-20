package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// plHarness serves the Paperless facade on its own router (so the tests do
// not depend on Router() mounting it) and falls through to the app's router
// for everything else (the harness uploads through /api/v1).
func plHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	app := h.s.Router()
	r := chi.NewRouter()
	h.s.mountPaperlessAPI(r)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		app.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, nil)))
	})
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	h.ts = ts
	return h
}

// pl sends a request with "Authorization: Token", the way Paperless clients do.
func (h *harness) pl(tok, method, path string, body any, out any, hdr ...string) int {
	h.t.Helper()
	return h.do("", method, path, body, out, append([]string{"Authorization", "Token " + tok}, hdr...)...)
}

type plList struct {
	Count    int              `json:"count"`
	Next     *string          `json:"next"`
	Previous *string          `json:"previous"`
	Results  []map[string]any `json:"results"`
	All      []int64          `json:"all"`
}

func TestPaperlessAuthAndMeta(t *testing.T) {
	h := plHarness(t)
	u, tok := h.user("alice@example.com")

	// no credentials → 401 with Paperless' detail shape and the version headers
	req, _ := http.NewRequest("GET", h.ts.URL+"/api/documents/", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 || res.Header.Get("X-Api-Version") != "9" || res.Header.Get("X-Version") == "" {
		t.Fatalf("unauthenticated: %d %v", res.StatusCode, res.Header)
	}

	var ui map[string]any
	if code := h.pl(tok, "GET", "/api/ui_settings/", nil, &ui); code != 200 {
		t.Fatalf("ui_settings: %d", code)
	}
	user := ui["user"].(map[string]any)
	if user["username"] != u.Email || user["is_superuser"] != true || ui["version"] == "" {
		t.Errorf("ui_settings: %v", ui)
	}
	if _, ok := ui["settings"].(map[string]any)["update_checking"]; !ok {
		t.Errorf("ui_settings has no settings: %v", ui)
	}

	// Bearer and Basic work too; Basic needs a password
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	u.PasswordHash = string(hash)
	if err := h.st.UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	var who map[string]any
	if code := h.do(tok, "GET", "/api/profile/", nil, &who); code != 200 || who["email"] != u.Email {
		t.Errorf("bearer: %d %v", code, who)
	}
	req, _ = http.NewRequest("GET", h.ts.URL+"/api/users/", nil)
	req.SetBasicAuth(u.Email, "secret123")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("basic auth: %d", res.StatusCode)
	}
	req.SetBasicAuth(u.Email, "wrong")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Errorf("basic auth wrong password: %d", res.StatusCode)
	}

	// POST /api/token/ mints a usable token
	var tk map[string]string
	if code := h.do("", "POST", "/api/token/", map[string]string{"username": u.Email, "password": "secret123"}, &tk); code != 200 || !strings.HasPrefix(tk["token"], "pd_") {
		t.Fatalf("token: %d %v", code, tk)
	}
	if code := h.pl(tk["token"], "GET", "/api/status/", nil, &who); code != 200 || who["pngx_version"] == "" {
		t.Errorf("new token: %d %v", code, who)
	}
	if code := h.do("", "POST", "/api/token/", map[string]string{"username": u.Email, "password": "nope"}, nil); code != 400 {
		t.Errorf("bad token login: %d", code)
	}

	var root map[string]string
	if code := h.pl(tok, "GET", "/api/", nil, &root); code != 200 || !strings.HasSuffix(root["documents"], "/api/documents/") {
		t.Errorf("root: %d %v", code, root)
	}
	var stats map[string]any
	if code := h.pl(tok, "GET", "/api/statistics/", nil, &stats); code != 200 || stats["documents_total"] != float64(0) || stats["inbox_tag"] == nil {
		t.Errorf("statistics: %d %v", code, stats)
	}
	var empty plList
	for _, p := range []string{"/api/groups/", "/api/mail_accounts/", "/api/mail_rules/", "/api/workflows/", "/api/share_links/", "/api/saved_views/"} {
		if code := h.pl(tok, "GET", p, nil, &empty); code != 200 || empty.Count != 0 {
			t.Errorf("%s: %d %+v", p, code, empty)
		}
	}
	var ver map[string]any
	if code := h.pl(tok, "GET", "/api/remote_version", nil, &ver); code != 200 || ver["update_available"] != false {
		t.Errorf("remote_version without slash: %d %v", code, ver)
	}
}

func TestPaperlessTagsCRUD(t *testing.T) {
	h := plHarness(t)
	_, tok := h.user("alice@example.com")

	var tag map[string]any
	if code := h.pl(tok, "POST", "/api/tags/", map[string]any{"name": "Tax", "color": "#1f78b4", "matching_algorithm": 1, "match": "porez", "is_inbox_tag": false}, &tag); code != 201 {
		t.Fatalf("create tag: %d %v", code, tag)
	}
	id := int64(tag["id"].(float64))
	if tag["slug"] != "tax" || tag["text_color"] != "#ffffff" || tag["user_can_change"] != true || tag["is_inbox_tag"] != false || tag["document_count"] != float64(0) {
		t.Errorf("tag shape: %v", tag)
	}
	var list plList
	if code := h.pl(tok, "GET", "/api/tags/?page_size=100", nil, &list); code != 200 || list.Count != 2 { // Inbox + Tax
		t.Fatalf("list tags: %d %+v", code, list)
	}
	if code := h.pl(tok, "GET", "/api/tags/?name__icontains=ta", nil, &list); code != 200 || list.Count != 1 || list.Results[0]["name"] != "Tax" {
		t.Errorf("name__icontains: %+v", list)
	}
	if code := h.pl(tok, "GET", "/api/tags/?name__iexact=inbox", nil, &list); code != 200 || list.Count != 1 || list.Results[0]["is_inbox_tag"] != true {
		t.Errorf("name__iexact: %+v", list)
	}
	if code := h.pl(tok, "GET", "/api/tags/?id__in="+itoa(id), nil, &list); code != 200 || list.Count != 1 {
		t.Errorf("id__in: %+v", list)
	}
	if code := h.pl(tok, "GET", "/api/tags/?ordering=-name", nil, &list); code != 200 || list.Results[0]["name"] != "Tax" {
		t.Errorf("ordering: %+v", list)
	}
	if code := h.pl(tok, "PATCH", "/api/tags/"+itoa(id)+"/", map[string]any{"name": "Taxes", "color": "#ffffff"}, &tag); code != 200 || tag["name"] != "Taxes" || tag["text_color"] != "#000000" || tag["match"] != "porez" {
		t.Errorf("patch tag: %d %v", code, tag)
	}
	if code := h.pl(tok, "GET", "/api/tags/"+itoa(id)+"/", nil, &tag); code != 200 || tag["name"] != "Taxes" {
		t.Errorf("get tag: %d %v", code, tag)
	}
	if code := h.pl(tok, "PUT", "/api/tags/"+itoa(id)+"/", map[string]any{"name": "Taxes", "color": "#ffffff", "match": "", "matching_algorithm": 6, "is_insensitive": true, "is_inbox_tag": false}, &tag); code != 200 || tag["matching_algorithm"] != float64(6) {
		t.Errorf("put tag: %d %v", code, tag)
	}
	if code := h.pl(tok, "POST", "/api/tags/", map[string]any{"name": "Taxes"}, &tag); code != 400 {
		t.Errorf("duplicate tag: %d %v", code, tag)
	}
	if code := h.pl(tok, "DELETE", "/api/tags/"+itoa(id)+"/", nil, nil); code != 204 {
		t.Errorf("delete tag: %d", code)
	}
	if code := h.pl(tok, "GET", "/api/tags/"+itoa(id)+"/", nil, nil); code != 404 {
		t.Errorf("deleted tag still there: %d", code)
	}

	// the other kinds share the shape
	var corr map[string]any
	if code := h.pl(tok, "POST", "/api/correspondents/", map[string]any{"name": "Elektroprivreda", "matching_algorithm": 1, "match": "elektro"}, &corr); code != 201 || corr["slug"] != "elektroprivreda" {
		t.Errorf("correspondent: %d %v", code, corr)
	}
	if _, ok := corr["last_correspondence"]; !ok {
		t.Errorf("correspondent lacks last_correspondence: %v", corr)
	}
	var sp map[string]any
	if code := h.pl(tok, "POST", "/api/storage_paths/", map[string]any{"name": "Bills", "path": "{created_year}/{correspondent}/{title}"}, &sp); code != 201 || sp["path"] != "{created_year}/{correspondent}/{title}" {
		t.Errorf("storage path: %d %v", code, sp)
	}
	var cf map[string]any
	if code := h.pl(tok, "POST", "/api/custom_fields/", map[string]any{"name": "Status", "data_type": "select", "extra_data": map[string]any{"select_options": []map[string]any{{"id": "paid", "label": "paid"}, {"id": "open", "label": "open"}}}}, &cf); code != 201 {
		t.Fatalf("custom field: %d %v", code, cf)
	}
	opts := cf["extra_data"].(map[string]any)["select_options"].([]any)
	if len(opts) != 2 || opts[0].(map[string]any)["label"] != "paid" {
		t.Errorf("select options: %v", cf)
	}
	var cf2 map[string]any
	if code := h.pl(tok, "POST", "/api/custom_fields/", map[string]any{"name": "Amount", "data_type": "monetary", "extra_data": map[string]any{"default_currency": "EUR"}}, &cf2); code != 201 || cf2["extra_data"].(map[string]any)["default_currency"] != "EUR" {
		t.Errorf("monetary field: %d %v", code, cf2)
	}
}

func TestPaperlessDocuments(t *testing.T) {
	h := plHarness(t)
	u, tok := h.user("alice@example.com")
	var corr, typ, tag map[string]any
	h.pl(tok, "POST", "/api/correspondents/", map[string]any{"name": "Elektroprivreda", "matching_algorithm": 1, "match": "elektroprivreda"}, &corr)
	h.pl(tok, "POST", "/api/document_types/", map[string]any{"name": "Invoice", "matching_algorithm": 1, "match": "invoice"}, &typ)
	h.pl(tok, "POST", "/api/tags/", map[string]any{"name": "Paid", "matching_algorithm": 0}, &tag)
	corrID, typID, tagID := int64(corr["id"].(float64)), int64(typ["id"].(float64)), int64(tag["id"].(float64))

	d, code := h.upload(tok, "struja_mart.txt", "ELEKTROPRIVREDA\nInvoice no. 2026-03-1187\nDate: 14.03.2026\nAmount due 58,40 EUR", nil)
	if code != 201 {
		t.Fatalf("upload: %d", code)
	}
	h.upload(tok, "lease.txt", "Lease agreement for the flat, signed 01.02.2025", map[string]string{"title": "Lease"})
	h.drain()

	// list shape
	var list plList
	if code := h.pl(tok, "GET", "/api/documents/?page=1&page_size=25&ordering=-created&full_perms=true", nil, &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if list.Count != 2 || len(list.Results) != 2 || len(list.All) != 2 || list.Next != nil || list.Previous != nil {
		t.Fatalf("list: %+v", list)
	}
	first := list.Results[0]
	for _, k := range []string{"id", "correspondent", "document_type", "storage_path", "title", "content", "tags", "created", "created_date", "modified", "added", "deleted_at", "archive_serial_number", "original_file_name", "archived_file_name", "owner", "user_can_change", "is_shared_by_requester", "notes", "custom_fields", "page_count", "mime_type"} {
		if _, ok := first[k]; !ok {
			t.Errorf("document lacks %q: %v", k, first)
		}
	}
	if first["owner"] != float64(u.ID) || first["created"] != "2026-03-14T00:00:00+00:00" || first["created_date"] != "2026-03-14" || first["correspondent"] != float64(corrID) || first["document_type"] != float64(typID) {
		t.Errorf("first document: %v", first)
	}
	if !strings.Contains(first["modified"].(string), "+00:00") {
		t.Errorf("modified stamp: %v", first["modified"])
	}

	// paging
	if code := h.pl(tok, "GET", "/api/documents/?page=1&page_size=1", nil, &list); code != 200 || list.Count != 2 || len(list.Results) != 1 || list.Next == nil || !strings.Contains(*list.Next, "page=2") || len(list.All) != 2 {
		t.Errorf("paging: %+v", list)
	}
	if code := h.pl(tok, "GET", "/api/documents/?page=2&page_size=1", nil, &list); code != 200 || len(list.Results) != 1 || list.Previous == nil || list.Next != nil {
		t.Errorf("page 2: %+v", list)
	}

	// filters
	type want struct {
		q string
		n int
	}
	for _, w := range []want{
		{"query=invoice", 1}, {"query=nothinghere", 0}, {"title__icontains=lease", 1},
		{"correspondent__id=" + itoa(corrID), 1}, {"correspondent__id__in=" + itoa(corrID), 1}, {"correspondent__isnull=true", 1}, {"correspondent__isnull=false", 1},
		{"document_type__id__in=" + itoa(typID), 1}, {"document_type__isnull=1", 1},
		{"tags__id__all=" + itoa(tagID), 0}, {"tags__id__none=" + itoa(tagID), 2}, {"is_in_inbox=true", 2}, {"is_in_inbox=false", 0}, {"is_tagged=true", 2}, {"is_tagged=false", 0},
		{"created__date__gte=2026-01-01", 1}, {"created__date__lt=2026-01-01", 1}, {"created__date__gt=2026-03-14", 0}, {"created__year=2025", 1},
		{"archive_serial_number__isnull=true", 2}, {"id__in=" + itoa(d.ID), 1}, {"storage_path__isnull=true", 2},
	} {
		if code := h.pl(tok, "GET", "/api/documents/?"+w.q, nil, &list); code != 200 || list.Count != w.n {
			t.Errorf("filter %s: %d results (code %d), want %d", w.q, list.Count, code, w.n)
		}
	}
	var hit, slim plList // fresh: decoding into a reused list keeps old map keys
	if code := h.pl(tok, "GET", "/api/documents/?query=invoice&truncate_content=true", nil, &hit); code != 200 || hit.Count != 1 || hit.Results[0]["__search_hit__"] == nil {
		t.Errorf("search hit: %+v", hit)
	}
	if code := h.pl(tok, "GET", "/api/documents/?fields=id,title", nil, &slim); code != 200 || slim.Results[0]["content"] != nil {
		t.Errorf("fields without content: %+v", slim.Results[0])
	}

	// one document
	var doc map[string]any
	if code := h.pl(tok, "GET", "/api/documents/"+itoa(d.ID)+"/", nil, &doc); code != 200 || !strings.Contains(doc["content"].(string), "Invoice") {
		t.Fatalf("get: %d %v", code, doc)
	}

	// PATCH the way Paperless-Mobile does
	if code := h.pl(tok, "PATCH", "/api/documents/"+itoa(d.ID)+"/", map[string]any{"title": "Electricity March", "archive_serial_number": 7, "tags": []int64{tagID}, "created_date": "2026-03-15", "correspondent": nil}, &doc); code != 200 {
		t.Fatalf("patch: %d %v", code, doc)
	}
	if doc["title"] != "Electricity March" || doc["archive_serial_number"] != float64(7) || doc["created_date"] != "2026-03-15" || doc["correspondent"] != nil {
		t.Errorf("patched: %v", doc)
	}
	if tags := doc["tags"].([]any); len(tags) != 1 || tags[0] != float64(tagID) {
		t.Errorf("patched tags: %v", doc["tags"])
	}
	var asn int64
	if code := h.pl(tok, "GET", "/api/documents/next_asn/", nil, &asn); code != 200 || asn != 8 {
		t.Errorf("next_asn: %d %d", code, asn)
	}

	// notes, metadata, suggestions, history, files
	var notes []map[string]any
	if code := h.pl(tok, "POST", "/api/documents/"+itoa(d.ID)+"/notes/", map[string]any{"note": "paid by card"}, &notes); code != 200 || len(notes) != 1 || notes[0]["note"] != "paid by card" {
		t.Errorf("add note: %d %v", code, notes)
	}
	if code := h.pl(tok, "GET", "/api/documents/"+itoa(d.ID)+"/", nil, &doc); code != 200 || len(doc["notes"].([]any)) != 1 {
		t.Errorf("notes on document: %v", doc["notes"])
	}
	noteID := int64(notes[0]["id"].(float64))
	if code := h.pl(tok, "DELETE", "/api/documents/"+itoa(d.ID)+"/notes/?id="+itoa(noteID), nil, &notes); code != 200 || len(notes) != 0 {
		t.Errorf("delete note: %d %v", code, notes)
	}
	var meta map[string]any
	if code := h.pl(tok, "GET", "/api/documents/"+itoa(d.ID)+"/metadata/", nil, &meta); code != 200 || meta["original_filename"] != "struja_mart.txt" || meta["original_checksum"] == "" {
		t.Errorf("metadata: %d %v", code, meta)
	}
	var sg map[string]any
	if code := h.pl(tok, "GET", "/api/documents/"+itoa(d.ID)+"/suggestions/", nil, &sg); code != 200 || sg["dates"] == nil || sg["correspondents"] == nil {
		t.Errorf("suggestions: %d %v", code, sg)
	}
	var hist []map[string]any
	if code := h.pl(tok, "GET", "/api/documents/"+itoa(d.ID)+"/history/", nil, &hist); code != 200 || len(hist) == 0 || hist[0]["action"] == nil {
		t.Errorf("history: %d %v", code, hist)
	}
	req, _ := http.NewRequest("GET", h.ts.URL+"/api/documents/"+itoa(d.ID)+"/download/?original=true", nil)
	req.Header.Set("Authorization", "Token "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		t.Errorf("download: %d %v", res.StatusCode, res.Header)
	}
	req, _ = http.NewRequest("GET", h.ts.URL+"/api/documents/"+itoa(d.ID)+"/preview/", nil)
	req.Header.Set("Authorization", "Token "+tok)
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("preview: %d", res.StatusCode)
	}

	// bulk_edit
	var bulk map[string]any
	if code := h.pl(tok, "POST", "/api/documents/bulk_edit/", map[string]any{"documents": list.All, "method": "add_tag", "parameters": map[string]any{"tag": tagID}}, &bulk); code != 200 || bulk["result"] != "OK" {
		t.Fatalf("bulk add_tag: %d %v", code, bulk)
	}
	if code := h.pl(tok, "GET", "/api/documents/?tags__id__all="+itoa(tagID), nil, &list); code != 200 || list.Count != 2 {
		t.Errorf("after bulk add_tag: %+v", list)
	}
	if code := h.pl(tok, "POST", "/api/documents/bulk_edit/", map[string]any{"documents": []int64{d.ID}, "method": "modify_tags", "parameters": map[string]any{"add_tags": []int64{}, "remove_tags": []int64{tagID}}}, &bulk); code != 200 {
		t.Errorf("bulk modify_tags: %d %v", code, bulk)
	}
	if code := h.pl(tok, "POST", "/api/documents/bulk_edit/", map[string]any{"documents": []int64{d.ID}, "method": "set_correspondent", "parameters": map[string]any{"correspondent": corrID}}, &bulk); code != 200 {
		t.Errorf("bulk set_correspondent: %d %v", code, bulk)
	}
	if code := h.pl(tok, "GET", "/api/documents/"+itoa(d.ID)+"/", nil, &doc); code != 200 || doc["correspondent"] != float64(corrID) || len(doc["tags"].([]any)) != 0 {
		t.Errorf("after bulk: %v", doc)
	}
	if code := h.pl(tok, "POST", "/api/documents/bulk_edit/", map[string]any{"documents": []int64{d.ID}, "method": "rotate", "parameters": map[string]any{"degrees": 90}}, &bulk); code != 400 {
		t.Errorf("rotate should be refused: %d %v", code, bulk)
	}
	var sel map[string]any
	if code := h.pl(tok, "POST", "/api/documents/selection_data/", map[string]any{"documents": []int64{d.ID}}, &sel); code != 200 || len(sel["selected_correspondents"].([]any)) != 1 {
		t.Errorf("selection_data: %d %v", code, sel)
	}

	// search endpoints
	var words []string
	if code := h.pl(tok, "GET", "/api/search/autocomplete/?term=inv", nil, &words); code != 200 || len(words) == 0 || words[0] != "Invoice" {
		t.Errorf("autocomplete: %d %v", code, words)
	}
	var global map[string]any
	if code := h.pl(tok, "GET", "/api/search/?query=invoice", nil, &global); code != 200 || len(global["documents"].([]any)) != 1 || len(global["document_types"].([]any)) != 1 {
		t.Errorf("global search: %d %v", code, global)
	}

	// delete = trash
	if code := h.pl(tok, "DELETE", "/api/documents/"+itoa(d.ID)+"/", nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code := h.pl(tok, "GET", "/api/documents/", nil, &list); code != 200 || list.Count != 1 {
		t.Errorf("after delete: %+v", list)
	}

	// bulk delete of the rest
	if code := h.pl(tok, "POST", "/api/documents/bulk_edit/", map[string]any{"documents": list.All, "method": "delete"}, &bulk); code != 200 {
		t.Errorf("bulk delete: %d %v", code, bulk)
	}
	if code := h.pl(tok, "GET", "/api/documents/", nil, &list); code != 200 || list.Count != 0 {
		t.Errorf("after bulk delete: %+v", list)
	}
}

func TestPaperlessPostDocumentAndTasks(t *testing.T) {
	h := plHarness(t)
	_, tok := h.user("alice@example.com")
	var tag map[string]any
	h.pl(tok, "POST", "/api/tags/", map[string]any{"name": "Scanned"}, &tag)
	tagID := int64(tag["id"].(float64))

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("title", "Phone bill")
	_ = mw.WriteField("created", "2026-04-01T00:00:00+02:00")
	_ = mw.WriteField("tags", itoa(tagID))
	_ = mw.WriteField("archive_serial_number", "42")
	fw, _ := mw.CreateFormFile("document", "bill.txt")
	_, _ = fw.Write([]byte("Telecom invoice for April\nTotal 20 EUR"))
	_ = mw.Close()
	var raw, taskID string
	if code := h.pl(tok, "POST", "/api/documents/post_document/", &buf, &raw, "Content-Type", mw.FormDataContentType()); code != 200 {
		t.Fatalf("post_document: %d %q", code, raw)
	}
	if err := json.Unmarshal([]byte(raw), &taskID); err != nil || taskID == "" {
		t.Fatalf("post_document answer is not a JSON string: %q", raw)
	}

	var tasks []map[string]any
	if code := h.pl(tok, "GET", "/api/tasks/?task_id="+taskID, nil, &tasks); code != 200 || len(tasks) != 1 || tasks[0]["status"] != "PENDING" || tasks[0]["task_file_name"] != "Phone bill" {
		t.Fatalf("pending task: %d %v", code, tasks)
	}
	h.drain()
	if code := h.pl(tok, "GET", "/api/tasks/?task_id="+taskID, nil, &tasks); code != 200 || len(tasks) != 1 || tasks[0]["status"] != "SUCCESS" || tasks[0]["related_document"] == nil || tasks[0]["acknowledged"] != false {
		t.Fatalf("done task: %d %v", code, tasks)
	}
	if !strings.Contains(tasks[0]["result"].(string), "New document id") {
		t.Errorf("task result: %v", tasks[0]["result"])
	}
	var ack map[string]int
	if code := h.pl(tok, "POST", "/api/acknowledge_tasks/", map[string]any{"tasks": []any{tasks[0]["id"]}}, &ack); code != 200 || ack["result"] != 1 {
		t.Errorf("acknowledge: %d %v", code, ack)
	}
	if code := h.pl(tok, "GET", "/api/tasks/?task_id="+taskID, nil, &tasks); code != 200 || tasks[0]["acknowledged"] != true {
		t.Errorf("acknowledged flag: %v", tasks)
	}

	var list plList
	if code := h.pl(tok, "GET", "/api/documents/?archive_serial_number=42", nil, &list); code != 200 || list.Count != 1 {
		t.Fatalf("uploaded document: %+v", list)
	}
	doc := list.Results[0]
	if doc["title"] != "Phone bill" || doc["created_date"] != "2026-04-01" {
		t.Errorf("uploaded document: %v", doc)
	}
	tags := doc["tags"].([]any)
	hasTag := false
	for _, x := range tags {
		if x == float64(tagID) {
			hasTag = true
		}
	}
	if !hasTag {
		t.Errorf("tag from upload missing: %v", tags)
	}

	// the same file again is a duplicate
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	fw, _ = mw.CreateFormFile("document", "bill.txt")
	_, _ = fw.Write([]byte("Telecom invoice for April\nTotal 20 EUR"))
	_ = mw.Close()
	var detail map[string]any
	if code := h.pl(tok, "POST", "/api/documents/post_document/", &buf, &detail, "Content-Type", mw.FormDataContentType()); code != 409 || detail["detail"] == nil {
		t.Errorf("duplicate upload: %d %v", code, detail)
	}
}

func TestPaperlessSavedViews(t *testing.T) {
	h := plHarness(t)
	_, tok := h.user("alice@example.com")
	var tag map[string]any
	h.pl(tok, "POST", "/api/tags/", map[string]any{"name": "Paid"}, &tag)
	tagID := itoa(int64(tag["id"].(float64)))

	var view map[string]any
	body := map[string]any{"name": "Paid bills", "show_on_dashboard": true, "show_in_sidebar": false, "sort_field": "archive_serial_number", "sort_reverse": true,
		"filter_rules": []map[string]any{{"rule_type": 6, "value": tagID}, {"rule_type": 0, "value": "bill"}, {"rule_type": 99, "value": "ignored"}}}
	if code := h.pl(tok, "POST", "/api/saved_views/", body, &view); code != 201 {
		t.Fatalf("create view: %d %v", code, view)
	}
	if view["sort_field"] != "archive_serial_number" || view["sort_reverse"] != true || view["show_on_dashboard"] != true {
		t.Errorf("view: %v", view)
	}
	rules := view["filter_rules"].([]any)
	if len(rules) != 2 {
		t.Errorf("rules round trip: %v", rules)
	}
	// stored as our query map
	var stored []store.SavedView
	h.do(tok, "GET", "/api/v1/saved_views", nil, &stored)
	if len(stored) != 1 || !strings.Contains(string(stored[0].Query), `"title":"bill"`) || !strings.Contains(string(stored[0].Query), `"sort":"-asn"`) {
		t.Errorf("stored query: %s", stored[0].Query)
	}
	id := itoa(int64(view["id"].(float64)))
	if code := h.pl(tok, "PATCH", "/api/saved_views/"+id+"/", map[string]any{"show_in_sidebar": true}, &view); code != 200 || view["show_in_sidebar"] != true || len(view["filter_rules"].([]any)) != 2 {
		t.Errorf("patch view: %d %v", code, view)
	}
	if code := h.pl(tok, "DELETE", "/api/saved_views/"+id+"/", nil, nil); code != 204 {
		t.Errorf("delete view: %d", code)
	}
}

func TestPaperlessPerPerson(t *testing.T) {
	h := plHarness(t)
	_, alice := h.user("alice@example.com")
	_, bob := h.user("bob@example.com")
	d, code := h.upload(alice, "secret.txt", "Alice's private note about salary", nil)
	if code != 201 {
		t.Fatalf("upload: %d", code)
	}
	h.drain()
	var list plList
	if code := h.pl(bob, "GET", "/api/documents/?query=salary", nil, &list); code != 200 || list.Count != 0 {
		t.Errorf("bob sees alice's documents: %+v", list)
	}
	for _, p := range []string{"/", "/download/", "/preview/", "/thumb/", "/metadata/", "/notes/", "/history/", "/suggestions/"} {
		if code := h.pl(bob, "GET", "/api/documents/"+itoa(d.ID)+p, nil, nil); code != 404 {
			t.Errorf("bob GET %s: %d, want 404", p, code)
		}
	}
	if code := h.pl(bob, "PATCH", "/api/documents/"+itoa(d.ID)+"/", map[string]any{"title": "hijacked"}, nil); code != 404 {
		t.Errorf("bob PATCH: %d", code)
	}
	if code := h.pl(bob, "DELETE", "/api/documents/"+itoa(d.ID)+"/", nil, nil); code != 404 {
		t.Errorf("bob DELETE: %d", code)
	}
	var bulk map[string]any
	if code := h.pl(bob, "POST", "/api/documents/bulk_edit/", map[string]any{"documents": []int64{d.ID}, "method": "delete"}, &bulk); code != 404 {
		t.Errorf("bob bulk delete: %d %v", code, bulk)
	}
	var doc map[string]any
	if code := h.pl(alice, "GET", "/api/documents/"+itoa(d.ID)+"/", nil, &doc); code != 200 || doc["title"] != "secret" {
		t.Errorf("alice still has it: %d %v", code, doc)
	}
	// users list only shows the caller
	var users plList
	if code := h.pl(bob, "GET", "/api/users/", nil, &users); code != 200 || users.Count != 1 || users.Results[0]["username"] != "bob@example.com" {
		t.Errorf("users: %+v", users)
	}
	raw, _ := json.Marshal(users.Results[0])
	if strings.Contains(string(raw), "alice") {
		t.Errorf("users leak: %s", raw)
	}
}
