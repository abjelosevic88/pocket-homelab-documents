package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// fakeAI is an OpenAI-compatible endpoint that answers with what answer
// returns for the prompt it got.
func fakeAI(t *testing.T, answer func(prompt string) string) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var prompts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
			Messages        []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Model != "gpt-5.4-mini" || in.ReasoningEffort != "low" {
			t.Errorf("model %q effort %q, want Pocket Money's gpt-5.4-mini / low", in.Model, in.ReasoningEffort)
		}
		p := in.Messages[len(in.Messages)-1].Content
		mu.Lock()
		prompts = append(prompts, p)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"model": in.Model, "choices": []any{map[string]any{"message": map[string]any{"content": answer(p)}}}})
	}))
	t.Cleanup(ts.Close)
	return ts, &prompts
}

func TestAIFillsNewDocuments(t *testing.T) {
	h := newHarness(t)
	u, tok := h.user("a@example.com")
	ctx := t.Context()
	receipts := &store.Named{UserID: u.ID, Name: "Fiskalni racuni"}
	wrong := &store.Named{UserID: u.ID, Name: "SP Wisestack"}
	for _, n := range []*store.Named{receipts, wrong} {
		if err := h.st.CreateDocumentType(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	tag := &store.Tag{UserID: u.ID, Name: "futrolica"}
	if err := h.st.CreateTag(ctx, tag); err != nil {
		t.Fatal(err)
	}
	ai, prompts := fakeAI(t, func(p string) string {
		ans, _ := json.Marshal(map[string]any{"title": "Futrolica fiskalni racun 28.10.2026", "created": "2026-10-27", "correspondent_id": nil, "new_correspondent": "Futrolica",
			"document_type_id": strconv.FormatInt(receipts.ID, 10), "storage_path_id": nil, "tag_ids": []int64{tag.ID, 99999}, "reason": "a fiscal receipt"})
		return string(ans)
	})
	var st map[string]any
	if code := h.do(tok, "PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "ai_url": ai.URL, "ai_key": "sk-test"}, &st); code != 200 {
		t.Fatalf("settings: %d %v", code, st)
	}
	if st["ai_configured"] != true || st["ai_auto"] != true || st["ai_key_set"] != true || st["ai_key"] != nil {
		t.Fatalf("settings answer: %v", st)
	}

	d, _ := h.upload(tok, "2026, 12_24.txt", "ФИСКАЛНИ РАЧУН\nFutrolica Marija Jakovljević s.p. Banja Luka\nУКУПНО 30,00", nil)
	h.drain()
	got, _ := h.st.GetDocument(ctx, u.ID, d.ID, false)
	if got.Title != "Futrolica fiskalni racun 28.10.2026" || got.Created != "2026-10-27" {
		t.Fatalf("title/date not filled: %q %q", got.Title, got.Created)
	}
	if got.DocumentTypeID == nil || *got.DocumentTypeID != receipts.ID {
		t.Fatalf("type: %v", got.DocumentTypeID)
	}
	c, err := h.st.FindCorrespondent(ctx, u.ID, "futrolica")
	if err != nil || got.CorrespondentID == nil || *got.CorrespondentID != c.ID {
		t.Fatalf("correspondent not created and set: %v %v", got.CorrespondentID, err)
	}
	if len(got.Tags) == 0 || !strings.Contains(strings.Join(func() []string {
		var s []string
		for _, id := range got.Tags {
			s = append(s, strconv.FormatInt(id, 10))
		}
		return s
	}(), ","), strconv.FormatInt(tag.ID, 10)) {
		t.Fatalf("tag not added: %v", got.Tags)
	}
	if len(*prompts) != 1 || !strings.Contains((*prompts)[0], `"Fiskalni racuni"`) || !strings.Contains((*prompts)[0], "Futrolica Marija") {
		t.Fatalf("prompt lacks the lists or the text: %v", *prompts)
	}

	// what the uploader chose stays; the next prompt shows the first one as an example
	d2, _ := h.upload(tok, "scan.txt", "ФИСКАЛНИ РАЧУН\nKonzum", map[string]string{"title": "Moj naslov", "document_type": strconv.FormatInt(wrong.ID, 10), "created": "2026-01-02"})
	h.drain()
	got, _ = h.st.GetDocument(ctx, u.ID, d2.ID, false)
	if got.Title != "Moj naslov" || got.Created != "2026-01-02" || got.DocumentTypeID == nil || *got.DocumentTypeID != wrong.ID {
		t.Fatalf("uploader's choices replaced: %q %q %v", got.Title, got.Created, got.DocumentTypeID)
	}
	if len(*prompts) != 2 || !strings.Contains((*prompts)[1], "Futrolica fiskalni racun 28.10.2026 | 2026-10-27 | Futrolica | Fiskalni racuni") {
		t.Fatalf("second prompt lacks the example: %v", (*prompts)[len(*prompts)-1])
	}

	// "Fill in with AI" replaces them
	var res aiFillResult
	if code := h.do(tok, "POST", "/api/v1/documents/"+strconv.FormatInt(d2.ID, 10)+"/ai", nil, &res); code != 200 || len(res.Applied) == 0 {
		t.Fatalf("fill: %d %v", code, res)
	}
	got, _ = h.st.GetDocument(ctx, u.ID, d2.ID, false)
	if got.Title != "Futrolica fiskalni racun 28.10.2026" || *got.DocumentTypeID != receipts.ID {
		t.Fatalf("manual fill did not replace: %q %v", got.Title, *got.DocumentTypeID)
	}

	// switched off: nothing is asked
	h.do(tok, "PUT", "/api/v1/settings", map[string]any{"ai_auto": false}, nil)
	h.upload(tok, "x.txt", "Racun", nil)
	h.drain()
	if len(*prompts) != 3 {
		t.Fatalf("AI ran while off: %d prompts", len(*prompts))
	}
}
