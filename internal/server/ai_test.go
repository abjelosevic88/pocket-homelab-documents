package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestAIQueueCreatesAndBackfills(t *testing.T) {
	h := newHarness(t)
	u, tok := h.user("a@example.com")
	ctx := t.Context()
	fail := false
	ai, prompts := fakeAI(t, func(p string) string {
		if fail {
			return "not json"
		}
		ans, _ := json.Marshal(map[string]any{"title": "Ugovor o zalozi Athena 28.09.2026", "created": "2026-09-28", "new_correspondent": "Athena Studio",
			"new_document_type": "Ugovori", "new_tags": []string{"posao", "x", "y"}, "tag_ids": []int64{},
			"new_fields": []map[string]any{{"name": "Amount", "type": "monetary", "value": "1.500,00 EUR"}, {"name": "Contract number", "type": "string", "value": "Z-12"}, {"name": "Bad", "type": "documentlink", "value": "1"}},
			"summary": "A share pledge agreement. Signed 28 September 2026.", "reason": "contract"})
		return string(ans)
	})

	// no AI yet: the new document waits for it
	scan := "Scan " + store.Today() + " 21.19.txt"
	d, _ := h.upload(tok, scan, "UGOVOR O ZALOZI\nBanja Luka, 28.09.2026\nIznos 1.500,00 EUR", nil)
	h.drain()
	var rec store.DocAI
	if code := h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID)+"/ai", nil, &rec); code != 200 || rec.Status != "pending" {
		t.Fatalf("not waiting for the AI: %d %+v", code, rec)
	}
	h.do(tok, "PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "ai_url": ai.URL, "ai_key": "sk-test", "ai_inbox": true}, nil)
	if n := h.s.aiRound(ctx); n != 1 {
		t.Fatalf("round took %d", n)
	}
	got, _ := h.st.GetDocument(ctx, u.ID, d.ID, false)
	// a scanner's name and today's date: the AI reads the real ones
	if got.Title != "Ugovor o zalozi Athena 28.09.2026" || got.Created != "2026-09-28" {
		t.Fatalf("title/date: %q %q", got.Title, got.Created)
	}
	ty, err := h.st.FindDocumentType(ctx, u.ID, "Ugovori")
	if err != nil || got.DocumentTypeID == nil || *got.DocumentTypeID != ty.ID || got.CorrespondentID == nil {
		t.Fatalf("type/correspondent not made: %v %v %v", got.DocumentTypeID, got.CorrespondentID, err)
	}
	posao, err := h.st.FindTag(ctx, u.ID, "posao")
	if err != nil || !slices.Contains(got.Tags, posao.ID) {
		t.Fatalf("tag: %v %v", got.Tags, err)
	}
	if _, err := h.st.FindTag(ctx, u.ID, "y"); err == nil {
		t.Fatal("more than 2 new tags")
	}
	for _, id := range h.st.InboxTagIDs(ctx, u.ID) {
		if slices.Contains(got.Tags, id) {
			t.Fatal("filed document still in the Inbox")
		}
	}
	vals := map[string]string{}
	names := h.st.Names(ctx, u.ID)
	for _, f := range got.Fields {
		vals[names.Fields[f.FieldID].Name] = f.Value
	}
	if vals["Amount"] != "1500.00" || vals["Contract number"] != "Z-12" || len(vals) != 2 {
		t.Fatalf("fields: %v", vals)
	}
	h.do(tok, "GET", "/api/v1/documents/"+itoa(d.ID)+"/ai", nil, &rec)
	if rec.Status != "done" || !strings.Contains(rec.Summary, "share pledge") || len(rec.Applied) == 0 {
		t.Fatalf("record: %+v", rec)
	}

	// older documents: only what is empty, and the person's own title stays
	h.do(tok, "PUT", "/api/v1/settings", map[string]any{"ai_auto": false}, nil)
	own, _ := h.upload(tok, "Licna karta.txt", "LICNA KARTA 1.500,00", map[string]string{"created": "2020-05-05"})
	machine, _ := h.upload(tok, "IMG_2041.txt", "UGOVOR", nil)
	h.drain()
	var counts store.AICounts
	h.do(tok, "GET", "/api/v1/ai/overview", nil, &counts)
	if counts.Never != 2 || counts.Done != 1 {
		t.Fatalf("overview: %+v", counts)
	}
	var out struct{ Queued int }
	if code := h.do(tok, "POST", "/api/v1/ai/backfill", map[string]any{}, &out); code != 200 || out.Queued != 2 {
		t.Fatalf("backfill: %d %+v", code, out)
	}
	before := len(*prompts)
	h.s.aiRound(ctx)
	if len(*prompts) != before+2 {
		t.Fatalf("prompts: %d", len(*prompts)-before)
	}
	g1, _ := h.st.GetDocument(ctx, u.ID, own.ID, false)
	g2, _ := h.st.GetDocument(ctx, u.ID, machine.ID, false)
	if g1.Title != "Licna karta" || g1.Created != "2020-05-05" {
		t.Fatalf("own filing replaced: %q %q", g1.Title, g1.Created)
	}
	if g2.Title != "Ugovor o zalozi Athena 28.09.2026" {
		t.Fatalf("scanner title kept: %q", g2.Title)
	}

	// a failed run is tried again later
	fail = true
	if code := h.do(tok, "POST", "/api/v1/documents/"+itoa(own.ID)+"/ai", nil, nil); code != 502 {
		t.Fatalf("manual fail: %d", code)
	}
	h.do(tok, "GET", "/api/v1/documents/"+itoa(own.ID)+"/ai", nil, &rec)
	if rec.Status != "error" || rec.NextTryAt != "" {
		t.Fatalf("manual failure record: %+v", rec)
	}
	params, _ := json.Marshal(aiFillMode{Fill: true})
	_ = h.st.SaveDocAI(ctx, &store.DocAI{DocumentID: machine.ID, UserID: u.ID, Status: "pending", Params: params, NextTryAt: store.Now()})
	h.s.aiRound(ctx)
	h.do(tok, "GET", "/api/v1/documents/"+itoa(machine.ID)+"/ai", nil, &rec)
	if rec.Status != "error" || rec.Attempts != 1 || rec.NextTryAt == "" {
		t.Fatalf("queued failure record: %+v", rec)
	}
}

func TestAICreateOff(t *testing.T) {
	h := newHarness(t)
	u, tok := h.user("a@example.com")
	ai, prompts := fakeAI(t, func(p string) string {
		return `{"title":"T","new_correspondent":"New Co","new_document_type":"New type","new_tags":["n"],"new_fields":[{"name":"Amount","type":"monetary","value":"3"}],"summary":"s"}`
	})
	h.do(tok, "PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "ai_url": ai.URL, "ai_key": "sk-test", "ai_create": false}, nil)
	d, _ := h.upload(tok, "scan.txt", "text", nil)
	h.drain()
	got, _ := h.st.GetDocument(t.Context(), u.ID, d.ID, false)
	if got.CorrespondentID != nil || got.DocumentTypeID != nil || len(got.Fields) != 0 {
		t.Fatalf("made something while off: %+v", got)
	}
	if !strings.Contains((*prompts)[0], "Do not suggest anything new") {
		t.Fatal("prompt does not say so")
	}
}

func TestPlainNumber(t *testing.T) {
	for in, want := range map[string]string{"1.500,00 EUR": "1500.00", "€ 1,500.00": "1500.00", "30,5": "30.5", "KM 12": "12", "1.234.567": "1234567", "12.50": "12.50", "-3,20": "-3.20"} {
		if got := plainNumber(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
