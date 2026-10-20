package server

import (
	"errors"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDocumentUploadReplayAndAtomicLifecycle(t *testing.T) {
	h := newHarness(t)
	u, tok := h.user("owner@example.com")
	other, _ := h.user("other@example.com")
	// No dispatcher: inspect the durable outbox directly.
	h.s.core = core.New("http://127.0.0.1:1", core.Options{})
	ctx := t.Context()
	upload := func(key, content string) (*store.Document, error) {
		f, err := os.CreateTemp(h.s.cfg.TmpDir, "upload-*.txt")
		if err != nil {
			return nil, err
		}
		name := f.Name()
		defer os.Remove(name)
		if _, err = f.WriteString(content); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		return h.s.ingest(ctx, u.ID, incoming{Path: name, Name: "receipt.txt", ExternalID: key, Source: "api"})
	}
	var wg sync.WaitGroup
	ids := make(chan int64, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := upload("invoice:1", "first receipt")
			if err != nil {
				t.Error(err)
				return
			}
			ids <- d.ID
		}()
	}
	wg.Wait()
	close(ids)
	var id int64
	for got := range ids {
		if id == 0 {
			id = got
		}
		if got != id {
			t.Error("different replay IDs", id, got)
		}
	}
	if id == 0 {
		t.Fatal("no upload")
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if err := h.st.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, table := range []string{"documents", "jobs", "pocket_outbox", "pocket_operations"} {
		if n := count(table); n != 1 {
			t.Fatalf("%s: %d", table, n)
		}
	}
	if _, err := upload("invoice:1", "changed receipt"); !errors.Is(err, core.ErrOperationConflict) {
		t.Fatal("conflict", err)
	}
	if _, err := h.st.DB.Exec(`CREATE TRIGGER reject_event BEFORE INSERT ON pocket_outbox BEGIN SELECT RAISE(ABORT,'queue unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := upload("invoice:2", "second receipt"); err == nil {
		t.Fatal("upload committed without event")
	}
	files, _ := filepath.Glob(filepath.Join(h.s.cfg.OriginalsDir, "*"))
	if len(files) != 1 || count("documents") != 1 || count("jobs") != 1 || count("pocket_operations") != 1 {
		t.Fatal("failed upload left data", files)
	}
	if code := h.do(tok, "PUT", "/api/v1/documents/"+itoa(id), map[string]any{"title": "Must roll back"}, nil); code != 500 {
		t.Fatal("update", code)
	}
	// Exercise the store hook explicitly, independent of route spelling.
	d, _ := h.st.GetDocument(ctx, u.ID, id, false)
	if err := h.st.ShareDocument(ctx, id, other.ID, u.ID, "view", h.s.documentEvent(ctx, u.ID, "documents.shared", d, nil)); err == nil {
		t.Fatal("share committed without event")
	}
	if code := h.do(tok, "DELETE", "/api/v1/documents/"+itoa(id), nil, nil); code != 500 {
		t.Fatal("delete", code)
	}
	d, _ = h.st.GetDocument(ctx, u.ID, id, false)
	if d.Title == "Must roll back" || d.DeletedAt != nil || count("document_shares") != 0 {
		t.Fatal("mutation survived failed event", d)
	}
	job, err := h.st.ClaimJob(ctx, []string{"process"})
	if err != nil || job == nil {
		t.Fatal("job", err)
	}
	if err := h.s.completeProcessing(ctx, job, d, "done", "", "documents.processed", nil); err == nil {
		t.Fatal("completion committed without event")
	}
	j, _ := h.st.GetJob(ctx, job.ID)
	if j.Status != "running" {
		t.Fatal("job committed", j.Status)
	}
	h.st.DB.Exec(`DROP TRIGGER reject_event`)
	if err := h.s.completeProcessing(ctx, job, d, "done", "", "documents.processed", nil); err != nil {
		t.Fatal(err)
	}
	if n := h.st.RequeueRunning(ctx); n != 0 {
		t.Fatal("completed job requeued", n)
	}
	second, err := upload("invoice:2", "second receipt")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID <= id+1 {
		t.Fatal("reserved ID was reused", second.ID)
	}
	if _, err := os.Stat(h.s.originalPath(second)); err != nil {
		t.Fatal(err)
	}
	if code := h.do(tok, "DELETE", "/api/v1/documents/"+itoa(id), nil, nil); code != 204 {
		t.Fatal(code)
	}
	if _, err := upload("invoice:1", "first receipt"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted replay", err)
	}
}
