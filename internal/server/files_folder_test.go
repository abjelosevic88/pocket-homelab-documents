package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"golang.org/x/net/webdav"
)

// fakeFiles is Pocket Core issuing pcx_ link tokens for the Documents → Files
// link, and Pocket Files answering WebDAV at /dav/ to those tokens only.
type fakeFiles struct {
	core, dav *httptest.Server
	fs        webdav.FileSystem

	mu     sync.Mutex
	issued map[string]string // pcx_ token → the person it was issued for
	asked  []string          // emails Core was asked tokens for, in order
	used   []string          // passwords Files saw, in order
	off    map[string]bool   // person → link switched off
}

func newFakeFiles(t *testing.T) *fakeFiles {
	t.Helper()
	f := &fakeFiles{fs: webdav.NewMemFS(), issued: map[string]string{}, off: map[string]bool{}}
	h := &webdav.Handler{Prefix: "/dav", FileSystem: f.fs, LockSystem: webdav.NewMemLS()}
	f.dav = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		f.mu.Lock()
		who := f.issued[pass]
		f.used = append(f.used, pass)
		f.mu.Unlock()
		if !ok || !strings.HasPrefix(pass, "pcx_") || who == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(f.dav.Close)
	f.core = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer pca_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/links/token":
			var in struct {
				User   string   `json:"user"`
				App    string   `json:"app"`
				Scopes []string `json:"scopes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.mu.Lock()
			defer f.mu.Unlock()
			f.asked = append(f.asked, in.User)
			if in.App != "files" || strings.Join(in.Scopes, " ") != "files:read files:write" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"wrong link"}`)
				return
			}
			if f.off[in.User] {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":"the link is switched off","reason":"off_person"}`)
				return
			}
			tok := "pcx_" + strconv.Itoa(len(f.issued)+1)
			f.issued[tok] = in.User
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "expires_in": 300, "scope": "files:read files:write", "app": "files", "url": f.dav.URL, "public_url": "https://drive.example"})
		case r.Method == "GET" && r.URL.Path == "/api/v1/links/status":
			f.mu.Lock()
			off := f.off[r.URL.Query().Get("user")]
			f.mu.Unlock()
			state := "on"
			if off {
				state = "off_person"
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"app": "files", "scope": "files:read", "state": state, "on": !off}, {"app": "files", "scope": "files:write", "state": state, "on": !off}})
		case r.Method == "GET" && r.URL.Path == "/api/v1/resolve":
			_ = json.NewEncoder(w).Encode(map[string]any{"slug": "files", "url": f.dav.URL, "public_url": "https://drive.example"})
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(f.core.Close)
	return f
}

func (f *fakeFiles) put(t *testing.T, p, text string) {
	t.Helper()
	ctx := context.Background()
	if i := strings.LastIndex(p, "/"); i > 0 {
		_ = f.fs.Mkdir(ctx, p[:i], 0o755)
	}
	fh, err := f.fs.OpenFile(ctx, p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fh.Write([]byte(text))
	fh.Close()
}

func (f *fakeFiles) exists(p string) bool {
	_, err := f.fs.Stat(context.Background(), p)
	return err == nil
}

func (f *fakeFiles) counts() (asked, used int, last string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.used) > 0 {
		last = f.used[len(f.used)-1]
	}
	return len(f.asked), len(f.used), last
}

// TestFolderInPocketFiles: the documents folder in Pocket Files through Core
// writes the copies and takes the Inbox exactly as Nextcloud does, with a
// link token for that person as the WebDAV password, asked for anew in time.
func TestFolderInPocketFiles(t *testing.T) {
	h := newHarness(t)
	owner, tok := h.user("owner@example.com")
	_, otherTok := h.user("other@example.com")
	ff := newFakeFiles(t)
	h.s.core = core.New(ff.core.URL, core.Options{Token: "pca_test"})
	h.s.coreOK = true // registered: the settings offer Core-only places

	var info map[string]any
	h.do(tok, "GET", "/api/v1/folder", nil, &info)
	if info["core"] != true || info["core_files"] != true || info["core_files_url"] != "https://drive.example" {
		t.Fatalf("folder info: %v", info)
	}

	d, code := h.upload(tok, "receipt.txt", "Receipt from the bakery", map[string]string{"title": "Bakery"})
	if d == nil {
		t.Fatalf("upload: %d", code)
	}
	h.drain()
	if code := h.do(tok, "PUT", "/api/v1/folder", map[string]any{"kind": "files", "folder": "Papers", "file": "original", "inbox": true, "inbox_name": "Inbox", "password": "ignored"}, nil); code != 200 {
		t.Fatalf("set folder: %d", code)
	}
	h.drain() // folder_sync
	got, err := h.st.GetDocument(context.Background(), owner.ID, d.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.MirrorPath == "" || !ff.exists("/Papers/"+got.MirrorPath) {
		t.Fatalf("copy not written to Pocket Files: %q", got.MirrorPath)
	}
	if !ff.exists("/Papers/Inbox") {
		t.Errorf("inbox folder not made")
	}
	st, _ := h.st.GetSettings(context.Background(), owner.ID)
	if st.FolderKind != "files" || !st.FolderViaCore || st.FolderPassword != "" || st.FolderName != "Papers" {
		t.Errorf("settings: %+v", st)
	}
	h.do(tok, "GET", "/api/v1/folder", nil, &info)
	if status, _ := info["status"].(map[string]any); status["kind"] != "files" || status["where"] != "Pocket Files · Papers" || status["error"] != nil {
		t.Errorf("status: %v", info["status"])
	}

	// the Inbox: a file there becomes a document (sub-folders are tags) and leaves the Inbox
	ff.put(t, "/Papers/Inbox/Bills/scan.txt", "Electricity bill October 2026")
	var took map[string]int
	if code := h.do(tok, "POST", "/api/v1/folder/inbox", nil, &took); code != 200 || took["taken"] != 1 {
		t.Fatalf("inbox: %d %v", code, took)
	}
	h.drain()
	var l listOut
	h.do(tok, "GET", "/api/v1/documents?q=electricity", nil, &l)
	if l.Count != 1 || l.Results[0].Source != "files" || len(l.Results[0].Tags) == 0 {
		t.Fatalf("inbox document: %+v", l.Results)
	}
	if ff.exists("/Papers/Inbox/Bills/scan.txt") {
		t.Errorf("taken file still in the inbox")
	}

	// one token serves many requests while it is young; every one was for this person
	asked, used, _ := ff.counts()
	if asked == 0 || used < 3*asked {
		t.Errorf("tokens not reused: %d asked for %d requests", asked, used)
	}
	ff.mu.Lock()
	for _, who := range ff.asked {
		if who != "owner@example.com" {
			t.Errorf("token asked for %q", who)
		}
	}
	ff.mu.Unlock()

	// an old token is replaced: every request now carries a fresh one
	old := filesTokenReuse
	filesTokenReuse = 0
	t.Cleanup(func() { filesTokenReuse = old })
	asked0, used0, last0 := ff.counts()
	if code := h.do(tok, "POST", "/api/v1/folder/inbox", nil, nil); code != 200 {
		t.Fatalf("inbox again: %d", code)
	}
	asked1, used1, last1 := ff.counts()
	if asked1-asked0 != used1-used0 || last1 == last0 {
		t.Errorf("tokens not refreshed: %d asked for %d requests", asked1-asked0, used1-used0)
	}

	// the person switches the link off in Core: shown like the other links
	ff.mu.Lock()
	ff.off["owner@example.com"] = true
	ff.mu.Unlock()
	var e map[string]any
	if code := h.do(tok, "POST", "/api/v1/folder/inbox", nil, &e); code != 502 || !strings.Contains(fmt.Sprint(e["error"]), "Pocket Files through Pocket Core: you switched the Documents → Files link off") {
		t.Errorf("link off: %d %v", code, e)
	}
	h.do(tok, "GET", "/api/v1/folder", nil, &info)
	if info["core_files"] != false || !strings.Contains(info["core_files_error"].(string), "Documents → Files link off") {
		t.Errorf("link off info: %v", info)
	}

	// someone else's folder is untouched
	h.do(otherTok, "GET", "/api/v1/folder", nil, &info)
	if info["kind"] != "none" {
		t.Errorf("other person's folder: %v", info["kind"])
	}
}

// Without Pocket Core there is no Pocket Files to choose.
func TestFolderInPocketFilesNeedsCore(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("owner@example.com")
	var info map[string]any
	h.do(tok, "GET", "/api/v1/folder", nil, &info)
	if info["core"] != false || info["core_files"] != nil {
		t.Errorf("folder info: %v", info)
	}
	var e map[string]string
	if code := h.do(tok, "PUT", "/api/v1/folder", map[string]any{"kind": "files", "folder": "Documents"}, &e); code != 400 || !strings.Contains(e["error"], "no Pocket Core") {
		t.Errorf("files without core: %d %v", code, e)
	}
}
