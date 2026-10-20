package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.RGBA{200, 0, 0, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Rotating an image keeps the old file as a version; a version can be restored; replace works the same.
func TestRotateVersionsReplace(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("alice@example.com")
	buf := multipartFile(t, "photo.png", pngBytes(t, 40, 20))
	var d store.Document
	if code := h.do(tok, "POST", "/api/v1/documents", buf.body, &d, "Content-Type", buf.ctype); code != 201 {
		t.Fatalf("upload %d", code)
	}
	h.drain()
	var out store.Document
	if code := h.do(tok, "POST", "/api/v1/documents/"+itoa(d.ID)+"/pages", map[string]any{"op": "rotate", "degrees": 90}, &out); code != 200 {
		t.Fatalf("rotate: %d %+v", code, out)
	}
	h.drain()
	cur, _ := h.st.GetDocument(context.Background(), 1, d.ID, false)
	f, _ := os.Open(h.s.originalPath(cur))
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil || img.Bounds().Dx() != 20 || img.Bounds().Dy() != 40 {
		t.Fatalf("not rotated: %v %v", err, img.Bounds())
	}
	var versions []store.Version
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID)+"/versions", nil, &versions)
	if len(versions) != 1 || versions[0].Reason != "rotated" {
		t.Fatalf("versions: %+v", versions)
	}
	if code := h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID)+"/versions/1/download", nil, nil); code != 200 {
		t.Errorf("download version: %d", code)
	}
	if code := h.do(tok, "POST", "/api/v1/documents/"+itoa(d.ID)+"/versions/1/restore", nil, &out); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	h.drain()
	cur, _ = h.st.GetDocument(context.Background(), 1, d.ID, false)
	f, _ = os.Open(h.s.originalPath(cur))
	img, _, _ = image.Decode(f)
	f.Close()
	if img == nil || img.Bounds().Dx() != 40 {
		t.Errorf("restore did not bring the old file back")
	}
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID)+"/versions", nil, &versions)
	if len(versions) != 2 {
		t.Errorf("restore should leave the rotated file as a version: %d", len(versions))
	}
	rep := multipartText(t, "new.txt", "replaced text content")
	if code := h.do(tok, "POST", "/api/v1/documents/"+itoa(d.ID)+"/replace", rep.body, &out, "Content-Type", rep.ctype); code != 200 || out.Mime != "text/plain" {
		t.Fatalf("replace: %d %+v", code, out)
	}
	h.drain()
	var l listOut
	h.do(tok, "GET", "/api/v1/documents?q=replaced", nil, &l)
	if l.Count != 1 {
		t.Errorf("replaced file not read: %d", l.Count)
	}
	// purge drops versions
	h.do(tok, "DELETE", "/api/v1/documents/"+itoa(d.ID), nil, nil)
	h.do(tok, "DELETE", "/api/v1/documents/"+itoa(d.ID)+"/purge", nil, nil)
	if _, err := os.Stat(filepath.Join(h.dir, "versions", itoa(d.ID))); err == nil {
		t.Errorf("versions dir still there after purge")
	}
}

func TestSharingBetweenAccounts(t *testing.T) {
	h := newHarness(t)
	_, aTok := h.user("alice@example.com")
	bob, bTok := h.user("bob@example.com")
	_, cTok := h.user("carol@example.com")
	d, _ := h.upload(aTok, "lease.txt", "Lease contract for the flat", map[string]string{"title": "Lease"})
	h.drain()
	id := itoa(d.ID)
	if code := h.do(bTok, "GET", "/api/v1/documents/"+id, nil, nil); code != 404 {
		t.Fatalf("before sharing bob gets %d", code)
	}
	var shares []map[string]any
	if code := h.do(aTok, "POST", "/api/v1/documents/"+id+"/shared_with", map[string]any{"user_id": bob.ID, "level": "view"}, &shares); code != 200 || len(shares) != 1 {
		t.Fatalf("share: %d %v", code, shares)
	}
	var got struct {
		Document store.Document `json:"document"`
	}
	if code := h.do(bTok, "GET", "/api/v1/documents/"+id, nil, &got); code != 200 || got.Document.Access != "view" || got.Document.OwnerName != "alice" {
		t.Fatalf("bob view: %d %+v", code, got.Document)
	}
	for _, p := range []string{"/download", "/text", "/history", "/notes", "/thumb"} {
		if code := h.do(bTok, "GET", "/api/v1/documents/"+id+p, nil, nil); code != 200 && !(p == "/thumb" && code == 404) {
			t.Errorf("bob GET %s → %d", p, code)
		}
	}
	if code := h.do(bTok, "PUT", "/api/v1/documents/"+id, map[string]any{"title": "x"}, nil); code != 403 {
		t.Errorf("viewer could edit: %d", code)
	}
	if code := h.do(bTok, "POST", "/api/v1/documents/"+id+"/notes", map[string]any{"text": "hi"}, nil); code != 403 {
		t.Errorf("viewer could add a note: %d", code)
	}
	if code := h.do(bTok, "DELETE", "/api/v1/documents/"+id, nil, nil); code != 404 {
		t.Errorf("viewer could delete: %d", code)
	}
	if code := h.do(bTok, "POST", "/api/v1/documents/"+id+"/shared_with", map[string]any{"user_id": 3, "level": "view"}, nil); code != 404 {
		t.Errorf("viewer could re-share: %d", code)
	}
	if code := h.do(cTok, "GET", "/api/v1/documents/"+id, nil, nil); code != 404 {
		t.Errorf("carol sees it: %d", code)
	}
	var shared listOut
	h.do(bTok, "GET", "/api/v1/shared", nil, &shared)
	if shared.Count != 1 {
		t.Errorf("shared list: %d", shared.Count)
	}
	h.do(bTok, "GET", "/api/v1/documents?shared=1", nil, &shared)
	if shared.Count != 1 {
		t.Errorf("shared filter: %d", shared.Count)
	}
	h.do(bTok, "GET", "/api/v1/documents", nil, &shared)
	if shared.Count != 0 {
		t.Errorf("shared document leaked into bob's own list")
	}
	// edit level
	h.do(aTok, "POST", "/api/v1/documents/"+id+"/shared_with", map[string]any{"user_id": bob.ID, "level": "edit"}, nil)
	var upd store.Document
	if code := h.do(bTok, "PUT", "/api/v1/documents/"+id, map[string]any{"title": "Lease (checked by Bob)"}, &upd); code != 200 || upd.Title != "Lease (checked by Bob)" {
		t.Fatalf("editor update: %d %+v", code, upd)
	}
	if code := h.do(bTok, "POST", "/api/v1/documents/"+id+"/notes", map[string]any{"text": "looks fine"}, nil); code != 201 {
		t.Errorf("editor note: %d", code)
	}
	h.do(aTok, "DELETE", "/api/v1/documents/"+id+"/shared_with/"+itoa(bob.ID), nil, nil)
	if code := h.do(bTok, "GET", "/api/v1/documents/"+id, nil, nil); code != 404 {
		t.Errorf("after unshare bob gets %d", code)
	}
}

func TestScheduledRulesAndFilenameDates(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("alice@example.com")
	var tag store.Tag
	h.do(tok, "POST", "/api/v1/tags", map[string]any{"name": "Expiring"}, &tag)
	var field store.CustomField
	h.do(tok, "POST", "/api/v1/custom_fields", map[string]any{"name": "Expires", "data_type": "date"}, &field)
	var rule store.Rule
	code := h.do(tok, "POST", "/api/v1/rules", map[string]any{"name": "30 days before expiry", "enabled": true, "trigger": "scheduled",
		"filters": map[string]any{"schedule_field": "cf:" + itoa(field.ID), "schedule_offset_days": -30}, "actions": map[string]any{"add_tags": []int64{tag.ID}, "notify": true, "notify_message": "{title} expires soon"}}, &rule)
	if code != 201 {
		t.Fatalf("rule: %d", code)
	}
	// a document whose expiry is in 10 days (due: 20 days ago → fires), one in 60 days (not due)
	soon := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	later := time.Now().AddDate(0, 0, 60).Format("2006-01-02")
	d1, _ := h.upload(tok, "2024-02-02_contract.txt", "contract A", nil)
	d2, _ := h.upload(tok, "contract_20240305.txt", "contract B", nil)
	h.drain()
	h.do(tok, "PUT", "/api/v1/documents/"+itoa(d1.ID), map[string]any{"custom_fields": []map[string]any{{"field": field.ID, "value": soon}}}, nil)
	h.do(tok, "PUT", "/api/v1/documents/"+itoa(d2.ID), map[string]any{"custom_fields": []map[string]any{{"field": field.ID, "value": later}}}, nil)
	h.s.runScheduled(context.Background())
	var g struct {
		Document store.Document `json:"document"`
	}
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d1.ID), nil, &g)
	if len(g.Document.Tags) != 2 || g.Document.Created != "2024-02-02" {
		t.Errorf("rule did not fire / filename date: tags %v created %s", g.Document.Tags, g.Document.Created)
	}
	var hist []store.HistoryEntry
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d1.ID)+"/history", nil, &hist)
	notified := false
	for _, x := range hist {
		if x.Action == "notify" {
			notified = true
		}
	}
	if !notified {
		t.Errorf("no notify entry in history")
	}
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d2.ID), nil, &g)
	if len(g.Document.Tags) != 1 || g.Document.Created != "2024-03-05" {
		t.Errorf("rule fired early / filename date: tags %v created %s", g.Document.Tags, g.Document.Created)
	}
	// fires once
	h.s.runScheduled(context.Background())
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d1.ID)+"/history", nil, &hist)
	n := 0
	for _, x := range hist {
		if x.Action == "rule" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("scheduled rule fired %d times", n)
	}
	var langs struct {
		Languages []map[string]any `json:"languages"`
	}
	if code := h.do(tok, "GET", "/api/v1/ocr/languages", nil, &langs); code != 200 || len(langs.Languages) < 20 {
		t.Errorf("languages: %d %d", code, len(langs.Languages))
	}
}

var _ = multipart.NewWriter
