package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

type listOut struct {
	Results []store.Document `json:"results"`
	Count   int              `json:"count"`
	Facets  *store.Facets    `json:"facets"`
}

// Upload → processed (text read natively) → found by search, facets, filters, suggestions, rules.
func TestUploadSearchFlow(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("alice@example.com")
	var tagTax store.Tag
	h.do(tok, "POST", "/api/v1/tags", map[string]any{"name": "Tax", "match": "porez tax", "matching_algorithm": 1}, &tagTax)
	var corr store.Named
	h.do(tok, "POST", "/api/v1/correspondents", map[string]any{"name": "Elektroprivreda", "match": "elektroprivreda", "matching_algorithm": 1}, &corr)
	var typ store.Named
	h.do(tok, "POST", "/api/v1/document_types", map[string]any{"name": "Invoice", "match": "invoice", "matching_algorithm": 1}, &typ)

	d, code := h.upload(tok, "struja_mart.txt", "ELEKTROPRIVREDA\nInvoice no. 2026-03-1187\nDate: 14.03.2026\nAmount due 58,40 EUR\nporez 5%", nil)
	if code != 201 {
		t.Fatalf("upload: %d", code)
	}
	if d.Status != "pending" || d.Title != "struja mart" {
		t.Fatalf("new doc: %+v", d)
	}
	// inbox tag applied
	if len(d.Tags) != 1 {
		t.Fatalf("inbox tag missing: %v", d.Tags)
	}
	h.drain()
	var got struct {
		Document store.Document `json:"document"`
	}
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID), nil, &got)
	dd := got.Document
	if dd.Status != "done" {
		t.Fatalf("status %s: %s", dd.Status, dd.StatusMessage)
	}
	if !strings.Contains(dd.Content, "Invoice no.") {
		t.Fatalf("content not read: %q", dd.Content)
	}
	if dd.Created != "2026-03-14" {
		t.Errorf("created not read from text: %s", dd.Created)
	}
	if dd.CorrespondentID == nil || *dd.CorrespondentID != corr.ID {
		t.Errorf("correspondent not matched: %v", dd.CorrespondentID)
	}
	if dd.DocumentTypeID == nil || *dd.DocumentTypeID != typ.ID {
		t.Errorf("type not matched: %v", dd.DocumentTypeID)
	}
	hasTax := false
	for _, x := range dd.Tags {
		if x == tagTax.ID {
			hasTax = true
		}
	}
	if !hasTax {
		t.Errorf("tag not matched: %v", dd.Tags)
	}
	// search: words, prefixes, phrases, filters, substrings
	for _, q := range []string{"invoice", "inv", `"amount due"`, "tag:tax", "from:elektroprivreda type:invoice", "created:2026-03", "asn:none nothing"} {
		var l listOut
		h.do(tok, "GET", "/api/v1/documents?q="+urlq(q), nil, &l)
		want := 1
		if strings.HasPrefix(q, "asn:none") {
			want = 0
		}
		if l.Count != want {
			t.Errorf("search %q: %d results, want %d", q, l.Count, want)
		}
	}
	var l listOut
	h.do(tok, "GET", "/api/v1/documents?q=nvoic", nil, &l)
	if l.Count != 1 {
		t.Errorf("substring fallback: %d", l.Count)
	}
	if l.Results[0].Snippet == "" || !strings.Contains(l.Results[0].Snippet, "[[[") {
		t.Errorf("no snippet: %q", l.Results[0].Snippet)
	}
	h.do(tok, "GET", "/api/v1/documents", nil, &l)
	if l.Facets == nil || l.Facets.Correspondents[corr.ID] != 1 || l.Facets.Tags[tagTax.ID] != 1 {
		t.Errorf("facets: %+v", l.Facets)
	}
	// duplicate upload is refused with the existing id
	var dup map[string]any
	var buf = multipartText(t, "again.txt", "ELEKTROPRIVREDA\nInvoice no. 2026-03-1187\nDate: 14.03.2026\nAmount due 58,40 EUR\nporez 5%")
	if code := h.do(tok, "POST", "/api/v1/documents", buf.body, &dup, "Content-Type", buf.ctype); code != 409 {
		t.Fatalf("duplicate: %d %v", code, dup)
	}
	// update + history + notes
	var upd store.Document
	if code := h.do(tok, "PUT", "/api/v1/documents/"+itoa(d.ID), map[string]any{"title": "Electricity March", "asn": 7, "remove_inbox_tags": true}, &upd); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if upd.Title != "Electricity March" || upd.ASN == nil || *upd.ASN != 7 || len(upd.Tags) != 1 {
		t.Fatalf("update result: %+v", upd)
	}
	var next map[string]int64
	h.do(tok, "GET", "/api/v1/documents/next_asn", nil, &next)
	if next["next_asn"] != 8 {
		t.Errorf("next asn %d", next["next_asn"])
	}
	var note store.Note
	if code := h.do(tok, "POST", "/api/v1/documents/"+itoa(d.ID)+"/notes", map[string]any{"text": "paid by card"}, &note); code != 201 {
		t.Fatalf("note: %d", code)
	}
	h.do(tok, "GET", "/api/v1/documents?q=card", nil, &l)
	if l.Count != 1 {
		t.Errorf("notes are searchable: %d", l.Count)
	}
	var hist []store.HistoryEntry
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID)+"/history", nil, &hist)
	if len(hist) < 4 {
		t.Errorf("history too short: %d", len(hist))
	}
	// text download, original download
	var txt string
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID)+"/text", nil, &txt)
	if !strings.Contains(txt, "ELEKTROPRIVREDA") {
		t.Errorf("text endpoint: %q", txt)
	}
	if code := h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID)+"/download?original=1", nil, nil); code != 200 {
		t.Errorf("download: %d", code)
	}
	// trash and restore
	if code := h.do(tok, "DELETE", "/api/v1/documents/"+itoa(d.ID), nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	h.do(tok, "GET", "/api/v1/documents?q=invoice", nil, &l)
	if l.Count != 0 {
		t.Errorf("deleted still found")
	}
	var trash listOut
	h.do(tok, "GET", "/api/v1/trash", nil, &trash)
	if trash.Count != 1 {
		t.Errorf("trash: %d", trash.Count)
	}
	if code := h.do(tok, "POST", "/api/v1/documents/"+itoa(d.ID)+"/restore", nil, nil); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	h.do(tok, "GET", "/api/v1/documents?q=invoice", nil, &l)
	if l.Count != 1 {
		t.Errorf("restored not found")
	}
	// purge removes files
	orig := h.s.originalPath(d)
	h.do(tok, "DELETE", "/api/v1/documents/"+itoa(d.ID), nil, nil)
	if code := h.do(tok, "DELETE", "/api/v1/documents/"+itoa(d.ID)+"/purge", nil, nil); code != 204 {
		t.Fatalf("purge: %d", code)
	}
	if _, err := os.Stat(orig); err == nil {
		t.Errorf("original still on disk after purge")
	}
}

func TestRulesBulkAndViews(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("alice@example.com")
	var tag store.Tag
	h.do(tok, "POST", "/api/v1/tags", map[string]any{"name": "Receipts"}, &tag)
	var corr store.Named
	h.do(tok, "POST", "/api/v1/correspondents", map[string]any{"name": "IKEA"}, &corr)
	var rule store.Rule
	code := h.do(tok, "POST", "/api/v1/rules", map[string]any{"name": "IKEA receipts", "enabled": true, "trigger": "added",
		"filters": map[string]any{"filename": "ikea*"}, "actions": map[string]any{"add_tags": []int64{tag.ID}, "correspondent": corr.ID, "title": "IKEA {created}"}}, &rule)
	if code != 201 {
		t.Fatalf("rule: %d", code)
	}
	d, _ := h.upload(tok, "ikea-receipt.txt", "IKEA Banja Luka\nReceipt 12.05.2026\nTotal 89,90", nil)
	d2, _ := h.upload(tok, "other.txt", "Something else entirely", nil)
	h.drain()
	var got struct {
		Document store.Document `json:"document"`
	}
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID), nil, &got)
	if got.Document.CorrespondentID == nil || *got.Document.CorrespondentID != corr.ID || got.Document.Title != "IKEA 2026-05-12" {
		t.Fatalf("rule not applied: %+v", got.Document)
	}
	found := false
	for _, x := range got.Document.Tags {
		if x == tag.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("rule tag missing")
	}
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d2.ID), nil, &got)
	if got.Document.CorrespondentID != nil {
		t.Errorf("rule applied to the wrong document")
	}
	// bulk: set type on both, then delete both
	var typ store.Named
	h.do(tok, "POST", "/api/v1/document_types", map[string]any{"name": "Receipt"}, &typ)
	var res map[string]int
	h.do(tok, "POST", "/api/v1/documents/bulk", map[string]any{"ids": []int64{d.ID, d2.ID}, "op": "set_document_type", "document_type": typ.ID}, &res)
	if res["done"] != 2 {
		t.Fatalf("bulk: %v", res)
	}
	var l listOut
	h.do(tok, "GET", "/api/v1/documents?document_type="+itoa(typ.ID), nil, &l)
	if l.Count != 2 {
		t.Errorf("bulk type filter: %d", l.Count)
	}
	// saved view runs through the same filter and shows on the dashboard
	var v store.SavedView
	h.do(tok, "POST", "/api/v1/saved_views", map[string]any{"name": "Receipts", "query": map[string]any{"document_type": typ.ID, "sort": "-created"}, "on_dashboard": true}, &v)
	var dash struct {
		Documents int `json:"documents"`
		Views     []struct {
			Count int `json:"count"`
		} `json:"views"`
	}
	h.do(tok, "GET", "/api/v1/dashboard", nil, &dash)
	if dash.Documents != 2 || len(dash.Views) != 1 || dash.Views[0].Count != 2 {
		t.Errorf("dashboard: %+v", dash)
	}
	// the documents folder follows the storage path, renames with the title and takes files from its Inbox
	var sp store.Named
	h.do(tok, "POST", "/api/v1/storage_paths", map[string]any{"name": "By correspondent", "path": "{correspondent}/{created_year}/{title}"}, &sp)
	root := filepath.Join(h.dir, "mirror")
	if code := h.do(tok, "PUT", "/api/v1/folder", map[string]any{"kind": "folder", "path": root, "file": "original", "inbox": true, "inbox_name": "Inbox"}, nil); code != 200 {
		t.Fatalf("set folder: %d", code)
	}
	h.drain() // folder_sync
	h.do(tok, "PUT", "/api/v1/documents/"+itoa(d.ID), map[string]any{"storage_path": sp.ID}, nil)
	want := filepath.Join(root, "IKEA", "2026", "IKEA 2026-05-12.txt")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("folder file missing: %s", want)
	}
	h.do(tok, "PUT", "/api/v1/documents/"+itoa(d.ID), map[string]any{"title": "IKEA sofa"}, nil)
	if _, err := os.Stat(want); err == nil {
		t.Errorf("old folder file not removed after rename")
	}
	if _, err := os.Stat(filepath.Join(root, "IKEA", "2026", "IKEA sofa.txt")); err != nil {
		t.Errorf("renamed folder file missing")
	}
	if _, err := os.Stat(filepath.Join(root, "Inbox")); err != nil {
		t.Errorf("inbox folder not made")
	}
	_ = os.MkdirAll(filepath.Join(root, "Inbox", "Bank"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "Inbox", "Bank", "statement.txt"), []byte("Bank statement May 2026"), 0o644)
	var took map[string]int
	if code := h.do(tok, "POST", "/api/v1/folder/inbox", nil, &took); code != 200 || took["taken"] != 1 {
		t.Fatalf("inbox: %d %v", code, took)
	}
	h.drain()
	var l2 listOut
	h.do(tok, "GET", "/api/v1/documents?q=statement", nil, &l2)
	if l2.Count != 1 || l2.Results[0].Source != "folder" || len(l2.Results[0].Tags) != 2 {
		t.Fatalf("inbox document: %+v", l2.Results)
	}
	if _, err := os.Stat(filepath.Join(root, "Inbox", "Bank", "statement.txt")); err == nil {
		t.Errorf("taken file still in the inbox")
	}
	if _, err := os.Stat(filepath.Join(root, "2026", "statement.txt")); err != nil {
		t.Errorf("taken document not written back into the folder")
	}
	// trash removes the copy
	h.do(tok, "DELETE", "/api/v1/documents/"+itoa(d.ID), nil, nil)
	if _, err := os.Stat(filepath.Join(root, "IKEA", "2026", "IKEA sofa.txt")); err == nil {
		t.Errorf("folder copy still there after delete")
	}
}

func TestExportImportAndShare(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("alice@example.com")
	var tag store.Tag
	h.do(tok, "POST", "/api/v1/tags", map[string]any{"name": "Keep"}, &tag)
	d, _ := h.upload(tok, "letter.txt", "Dear Sir,\nthis is a letter.", map[string]string{"title": "A letter", "tags": itoa(tag.ID), "created": "2024-02-02"})
	h.drain()
	h.do(tok, "POST", "/api/v1/documents/"+itoa(d.ID)+"/notes", map[string]any{"text": "keep this"}, nil)
	var zipBody string
	if code := h.do(tok, "GET", "/api/v1/export", nil, &zipBody); code != 200 || len(zipBody) < 100 {
		t.Fatalf("export: %d %d bytes", code, len(zipBody))
	}
	// a second person imports it: gets their own copy, tags merged by name
	_, bob := h.user("bob@example.com")
	buf := multipartFile(t, "export.zip", []byte(zipBody))
	var res importResult
	if code := h.do(bob, "POST", "/api/v1/import", buf.body, &res, "Content-Type", buf.ctype); code != 200 {
		t.Fatalf("import: %d %+v", code, res)
	}
	if res.Documents != 1 || res.Tags != 1 {
		t.Fatalf("import result: %+v", res)
	}
	h.drain()
	var l listOut
	h.do(bob, "GET", "/api/v1/documents?q=letter", nil, &l)
	if l.Count != 1 || l.Results[0].Title != "A letter" || l.Results[0].Created != "2024-02-02" || len(l.Results[0].Tags) != 2 {
		t.Fatalf("imported doc: %+v", l.Results)
	}
	// alice still has exactly hers
	h.do(tok, "GET", "/api/v1/documents", nil, &l)
	if l.Count != 1 {
		t.Errorf("alice count %d", l.Count)
	}
	// share link works without login, expires, can be revoked
	var sh map[string]any
	if code := h.do(tok, "POST", "/api/v1/documents/"+itoa(d.ID)+"/shares", map[string]any{"days": 7}, &sh); code != 201 {
		t.Fatalf("share: %d", code)
	}
	token := sh["token"].(string)
	var page string
	if code := h.do("", "GET", "/s/"+token, nil, &page); code != 200 || !strings.Contains(page, "A letter") {
		t.Fatalf("share page: %d", code)
	}
	if code := h.do("", "GET", "/s/"+token+"/file", nil, &page); code != 200 || !strings.Contains(page, "Dear Sir") {
		t.Fatalf("share file: %d", code)
	}
	id := int64(sh["id"].(float64))
	h.do(tok, "DELETE", "/api/v1/shares/"+itoa(id), nil, nil)
	if code := h.do("", "GET", "/s/"+token, nil, nil); code != 404 {
		t.Errorf("revoked share still answers %d", code)
	}
	// Paperless fixture import
	fixture := `[{"model":"documents.tag","pk":5,"fields":{"name":"Paperless tag","color":"#ff0000","match":"","matching_algorithm":6,"is_insensitive":true,"is_inbox_tag":false}},
	{"model":"documents.correspondent","pk":2,"fields":{"name":"Old Corr","match":"","matching_algorithm":6,"is_insensitive":true}},
	{"model":"documents.note","pk":1,"fields":{"document":9,"note":"from paperless"}},
	{"model":"documents.document","pk":9,"fields":{"title":"Paperless doc","content":"hello","created":"2023-05-06T00:00:00Z","correspondent":2,"tags":[5],"original_filename":"pl.txt","__exported_file_name__":"0000009.txt"}}]`
	zipBytes := makeZip(t, map[string]string{"manifest.json": fixture, "0000009.txt": "hello from paperless export"})
	buf = multipartFile(t, "paperless.zip", zipBytes)
	if code := h.do(bob, "POST", "/api/v1/import", buf.body, &res, "Content-Type", buf.ctype); code != 200 || res.Documents != 1 {
		t.Fatalf("paperless import: %d %+v", code, res)
	}
	h.drain()
	h.do(bob, "GET", "/api/v1/documents?q=paperless", nil, &l)
	if l.Count != 1 || l.Results[0].Created != "2023-05-06" || l.Results[0].CorrespondentName != "Old Corr" || l.Results[0].NotesCount != 1 {
		t.Fatalf("paperless doc: %+v", l.Results)
	}
}

func TestConsumeFolder(t *testing.T) {
	h := newHarness(t)
	alice, _ := h.user("alice@example.com")
	bob, _ := h.user("bob@example.com")
	root := h.s.cfg.ConsumeDir
	_ = os.MkdirAll(filepath.Join(root, "bob", "Bank"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "scan.txt"), []byte("owner scan"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "bob", "Bank", "statement.txt"), []byte("bob statement"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "ignored.exe"), []byte("x"), 0o644)
	seen := map[string]seenFile{}
	h.s.cfg.ConsumePoll = 0
	h.s.consumeOnce(context.Background(), seen) // first look: remember sizes
	h.s.consumeOnce(context.Background(), seen) // second look: stable, taken
	h.drain()
	a := h.st.DocumentIDs(context.Background(), alice.ID, false)
	b := h.st.DocumentIDs(context.Background(), bob.ID, false)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("consumed: alice %v bob %v", a, b)
	}
	bd, _ := h.st.GetDocument(context.Background(), bob.ID, b[0], false)
	if len(bd.Tags) != 2 { // inbox + Bank
		t.Errorf("sub-folder tag: %v", bd.Tags)
	}
	if _, err := os.Stat(filepath.Join(root, "scan.txt")); err == nil {
		t.Errorf("consumed file not removed")
	}
	if _, err := os.Stat(filepath.Join(root, "ignored.exe")); err != nil {
		t.Errorf("unsupported file should stay")
	}
}
