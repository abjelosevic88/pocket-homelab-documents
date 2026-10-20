package server

import (
	"testing"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Two people, one box: nothing of one is reachable by the other.
func TestStrictlyPerUser(t *testing.T) {
	h := newHarness(t)
	alice, aTok := h.user("alice@example.com")
	bob, bTok := h.user("bob@example.com")
	if alice.Role != "admin" || bob.Role != "member" {
		t.Fatalf("roles: %s %s", alice.Role, bob.Role)
	}
	var tag store.Tag
	h.do(aTok, "POST", "/api/v1/tags", map[string]any{"name": "Secret"}, &tag)
	var corr store.Named
	h.do(aTok, "POST", "/api/v1/correspondents", map[string]any{"name": "Bank"}, &corr)
	var field store.CustomField
	h.do(aTok, "POST", "/api/v1/custom_fields", map[string]any{"name": "Amount", "data_type": "monetary"}, &field)
	var view store.SavedView
	h.do(aTok, "POST", "/api/v1/saved_views", map[string]any{"name": "Mine"}, &view)
	var rule store.Rule
	h.do(aTok, "POST", "/api/v1/rules", map[string]any{"name": "R"}, &rule)
	d, code := h.upload(aTok, "secret.txt", "very secret salary statement", map[string]string{"tags": itoa(tag.ID)})
	if code != 201 {
		t.Fatalf("upload %d", code)
	}
	h.drain()
	var sh map[string]any
	h.do(aTok, "POST", "/api/v1/documents/"+itoa(d.ID)+"/shares", map[string]any{}, &sh)
	id := itoa(d.ID)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/documents/" + id, ""}, {"PUT", "/api/v1/documents/" + id, `{"title":"x"}`}, {"DELETE", "/api/v1/documents/" + id, ""},
		{"POST", "/api/v1/documents/" + id + "/restore", ""}, {"DELETE", "/api/v1/documents/" + id + "/purge", ""},
		{"GET", "/api/v1/documents/" + id + "/download", ""}, {"GET", "/api/v1/documents/" + id + "/preview", ""}, {"GET", "/api/v1/documents/" + id + "/thumb", ""},
		{"GET", "/api/v1/documents/" + id + "/pages/1", ""}, {"GET", "/api/v1/documents/" + id + "/text", ""}, {"POST", "/api/v1/documents/" + id + "/reprocess", "{}"},
		{"GET", "/api/v1/documents/" + id + "/suggestions", ""}, {"POST", "/api/v1/documents/" + id + "/suggestions/apply", ""}, {"GET", "/api/v1/documents/" + id + "/similar", ""},
		{"GET", "/api/v1/documents/" + id + "/notes", ""}, {"POST", "/api/v1/documents/" + id + "/notes", `{"text":"x"}`}, {"DELETE", "/api/v1/documents/" + id + "/notes/1", ""},
		{"GET", "/api/v1/documents/" + id + "/history", ""}, {"GET", "/api/v1/documents/" + id + "/shares", ""}, {"POST", "/api/v1/documents/" + id + "/shares", "{}"},
		{"GET", "/api/v1/documents/" + id + "/jobs", ""}, {"POST", "/api/v1/documents/" + id + "/money", `{"amount":1,"account_id":1}`},
		{"PUT", "/api/v1/tags/" + itoa(tag.ID), `{"name":"y"}`}, {"DELETE", "/api/v1/tags/" + itoa(tag.ID), ""},
		{"PUT", "/api/v1/correspondents/" + itoa(corr.ID), `{"name":"y"}`}, {"DELETE", "/api/v1/correspondents/" + itoa(corr.ID), ""},
		{"PUT", "/api/v1/custom_fields/" + itoa(field.ID), `{"name":"y"}`}, {"DELETE", "/api/v1/custom_fields/" + itoa(field.ID), ""},
		{"PUT", "/api/v1/saved_views/" + itoa(view.ID), `{"name":"y"}`}, {"DELETE", "/api/v1/saved_views/" + itoa(view.ID), ""},
		{"PUT", "/api/v1/rules/" + itoa(rule.ID), `{"name":"y"}`}, {"DELETE", "/api/v1/rules/" + itoa(rule.ID), ""}, {"POST", "/api/v1/rules/" + itoa(rule.ID) + "/run", ""},
		{"DELETE", "/api/v1/shares/" + itoa(int64(sh["id"].(float64))), ""},
	} {
		var body any
		if c.body != "" {
			body = c.body
		}
		if code := h.do(bTok, c.method, c.path, body, nil); code != 404 {
			t.Errorf("bob %s %s → %d, want 404", c.method, c.path, code)
		}
	}
	if code := h.do(aTok, "GET", "/api/v1/documents/"+id, nil, nil); code != 200 {
		t.Fatalf("alice lost her document: %d", code)
	}
	var l listOut
	h.do(bTok, "GET", "/api/v1/documents?q=secret", nil, &l)
	if l.Count != 0 {
		t.Errorf("bob finds %d documents", l.Count)
	}
	h.do(bTok, "GET", "/api/v1/documents?tags="+itoa(tag.ID), nil, &l)
	if l.Count != 0 {
		t.Errorf("bob filters into alice's tag: %d", l.Count)
	}
	var tags []store.Tag
	h.do(bTok, "GET", "/api/v1/tags", nil, &tags)
	for _, x := range tags {
		if x.Name == "Secret" {
			t.Errorf("bob sees alice's tag")
		}
	}
	// bulk with alice's ids does nothing for bob
	var res map[string]int
	h.do(bTok, "POST", "/api/v1/documents/bulk", map[string]any{"ids": []int64{d.ID}, "op": "delete"}, &res)
	if res["done"] != 0 {
		t.Errorf("bob bulk-deleted alice's document")
	}
	var jobs struct {
		Jobs []store.Job `json:"jobs"`
	}
	h.do(bTok, "GET", "/api/v1/jobs", nil, &jobs)
	if len(jobs.Jobs) != 0 {
		t.Errorf("bob sees %d of alice's jobs", len(jobs.Jobs))
	}
	var dash map[string]any
	h.do(bTok, "GET", "/api/v1/dashboard", nil, &dash)
	if dash["documents"].(float64) != 0 {
		t.Errorf("bob's dashboard counts alice's documents")
	}
}
