package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/config"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// moveHarness starts Documents behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@.
func moveHarness(t *testing.T) *harness {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "documents:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"documents","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newHarness(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
}

func (h *harness) raw(tok, method, path, ctype string, body []byte) (int, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func exportedManifest(t *testing.T, b []byte) exportManifest {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var m exportManifest
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			rc, _ := f.Open()
			_ = json.NewDecoder(rc).Decode(&m)
			rc.Close()
		}
	}
	return m
}

func (h *harness) moveImport(tok string, data []byte) map[string]any {
	h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "documents.zip")
	_, _ = fw.Write(data)
	_ = mw.Close()
	code, out := h.raw(tok, "POST", "/api/v1/import", mw.FormDataContentType(), buf.Bytes())
	if code != 200 {
		h.t.Fatalf("import → %d %s", code, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

func TestMoveLinkScopes(t *testing.T) {
	h := moveHarness(t)
	h.user("alice@example.com")
	h.user("bob@example.com")
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/users", 403},
		{exp, "GET", "/api/v1/documents", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/import/paperless", 403},
		{imp, "POST", "/api/v1/documents", 403},
		{imp, "POST", "/api/v1/rules", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export", 403},
		{read, "POST", "/api/v1/import", 403},
	} {
		if code, body := h.raw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := h.raw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 1 || len(p.Imports) != 2 || p.Exports[0].Kind != core.KindPocket || p.Imports[0].Form != "file" {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's documents to Bob: each import adds once (documents,
// saved views, rules), and nothing of one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	h := moveHarness(t)
	alice, atok := h.user("alice@example.com")
	bob, btok := h.user("bob@example.com")
	if _, code := h.upload(atok, "lease.txt", "The lease of the flat on Main Street", nil); code != 201 {
		t.Fatalf("upload: %d", code)
	}
	h.drain()
	ctx := context.Background()
	_ = h.st.CreateSavedView(ctx, &store.SavedView{UserID: alice.ID, Name: "Contracts", Query: json.RawMessage(`{"q":"lease"}`)})
	_ = h.st.CreateRule(ctx, &store.Rule{UserID: alice.ID, Name: "Leases", Enabled: true, Trigger: "added"})

	code, export := h.raw("pcx_alice_export", "GET", "/api/v1/export", "", nil)
	if m := exportedManifest(t, export); code != 200 || len(m.Documents) != 1 || len(m.SavedViews) != 1 {
		t.Fatalf("alice's export: %d %+v", code, m)
	}
	if _, bexp := h.raw("pcx_bob_export", "GET", "/api/v1/export", "", nil); len(exportedManifest(t, bexp).Documents) != 0 || len(exportedManifest(t, bexp).SavedViews) != 0 {
		t.Fatal("bob's export holds alice's documents")
	}

	first := h.moveImport("pcx_bob_import", export)
	if first["documents"] != float64(1) {
		t.Fatalf("first import: %v", first)
	}
	second := h.moveImport("pcx_bob_import", export)
	if second["documents"] != float64(0) || second["skipped"] != float64(1) || second["tags"] != float64(0) {
		t.Fatalf("second import: %v", second)
	}
	views, _ := h.st.ListSavedViews(ctx, bob.ID)
	rules, _ := h.st.ListRules(ctx, bob.ID)
	if len(views) != 1 || len(rules) != 1 {
		t.Fatalf("bob has %d views and %d rules, want 1 and 1", len(views), len(rules))
	}
	var bl, al struct {
		Count int `json:"count"`
	}
	h.do(btok, "GET", "/api/v1/documents", nil, &bl)
	h.do(atok, "GET", "/api/v1/documents", nil, &al)
	if bl.Count != 1 || al.Count != 1 {
		t.Fatalf("documents: bob %d, alice %d", bl.Count, al.Count)
	}
	if v, _ := h.st.ListSavedViews(ctx, alice.ID); len(v) != 1 {
		t.Fatalf("alice's views changed: %d", len(v))
	}
}
