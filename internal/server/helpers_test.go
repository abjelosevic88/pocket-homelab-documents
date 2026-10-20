package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/config"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

type harness struct {
	t   *testing.T
	s   *Server
	st  *store.Store
	ts  *httptest.Server
	dir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{AuthMode: "local", SessionTTL: time.Hour, DataDir: dir, DBPath: dir + "/t.db",
		OriginalsDir: dir + "/originals", ArchiveDir: dir + "/archive", ThumbsDir: dir + "/thumbs", PagesDir: dir + "/pages", ConsumeDir: dir + "/consume", MirrorDir: dir + "/mirror", TmpDir: dir + "/tmp",
		MaxUploadBytes: 50 << 20, OCRWorkers: 1, OCRThreads: 1, OCRTimeout: time.Minute, OCRLanguage: "eng", OCRMode: "skip", OCROutputType: "pdfa", ConsumePoll: time.Hour, ConsumeDelete: true, FolderRoots: []string{dir + "/mirror"}, FolderPoll: time.Hour}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(cfg, st, log, nil)
	if err := s.core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{cfg.OriginalsDir, cfg.ArchiveDir, cfg.ThumbsDir, cfg.PagesDir, cfg.ConsumeDir, cfg.TmpDir} {
		_ = mkdir(d)
	}
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	return &harness{t: t, s: s, st: st, ts: ts, dir: dir}
}

// user makes an account (first = admin) and an API token for it.
func (h *harness) user(email string) (*store.User, string) {
	h.t.Helper()
	u, err := h.s.firstOrSystemUser(context.Background(), email, strings.Split(email, "@")[0])
	if err != nil {
		h.t.Fatal(err)
	}
	h.s.seedDefaults(context.Background(), u.ID)
	tok, _, err := h.st.CreateAPIToken(context.Background(), u.ID, "test")
	if err != nil {
		h.t.Fatal(err)
	}
	return u, tok
}

// do sends a request with a bearer token and decodes a JSON answer into out.
func (h *harness) do(tok, method, path string, body any, out any, hdr ...string) int {
	h.t.Helper()
	var rd io.Reader
	ctype := "application/json"
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		case *bytes.Buffer:
			rd = b
			ctype = ""
		default:
			j, _ := json.Marshal(body)
			rd = bytes.NewReader(j)
		}
	}
	req, _ := http.NewRequest(method, h.ts.URL+path, rd)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && len(raw) > 0 {
		if s, ok := out.(*string); ok {
			*s = string(raw)
		} else if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("%s %s: %v in %s", method, path, err, raw)
		}
	}
	return res.StatusCode
}

// upload posts a text file as a document and returns it.
func (h *harness) upload(tok, name, text string, fields map[string]string) (*store.Document, int) {
	h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("document", name)
	_, _ = fw.Write([]byte(text))
	_ = mw.Close()
	var d store.Document
	code := h.do(tok, "POST", "/api/v1/documents", &buf, &d, "Content-Type", mw.FormDataContentType())
	if code != 201 {
		return nil, code
	}
	return &d, code
}

// drain runs every queued job now, in this goroutine.
func (h *harness) drain() {
	h.t.Helper()
	for i := 0; i < 100; i++ {
		j, err := h.st.ClaimJob(context.Background(), []string{"process", "reprocess", "import_paperless", "reindex", "mirror_all", "folder_sync", "run_rule"})
		if err != nil {
			h.t.Fatal(err)
		}
		if j == nil {
			return
		}
		h.s.runJob(context.Background(), j)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func i64(n int64) *int64 { return &n }
