package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/extract"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/storage"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// The documents folder: each person may point the app at a folder on this
// server, a WebDAV server or their Nextcloud. The app keeps a readable copy
// of every document there (the PDF/A or the original, laid out by storage
// path), renames and removes it as the document changes, and takes new files
// from the folder's Inbox. The archive itself stays in DATA_DIR: that is
// what OCR, previews and backups work from.

// serviceUses is what the app asks Pocket Core for: the person's Nextcloud account.
var serviceUses = []core.ServiceUse{{Type: "nextcloud", Reason: "Keep your documents folder in your Nextcloud and take new files from its Inbox"}}

type docFolder struct {
	uid                  int64
	key                  string
	backend              storage.Backend
	mu                   sync.Mutex // one write at a time per person
	stateMu              sync.Mutex
	lastWrite, lastInbox time.Time
	lastErr, inboxErr    string
	taken                int
	seen                 map[string]string    // inbox path → etag seen last time
	failed               map[string]time.Time // inbox path → when it failed (retried after an hour)
}

func (f *docFolder) status() map[string]any {
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	out := map[string]any{"kind": f.backend.Kind(), "where": f.backend.Describe(), "taken": f.taken}
	if !f.lastWrite.IsZero() {
		out["last_write"] = f.lastWrite.UTC().Format(time.RFC3339)
	}
	if !f.lastInbox.IsZero() {
		out["last_inbox"] = f.lastInbox.UTC().Format(time.RFC3339)
	}
	if f.lastErr != "" {
		out["error"] = f.lastErr
	}
	if f.inboxErr != "" {
		out["inbox_error"] = f.inboxErr
	}
	return out
}

func folderKey(st store.Settings) string {
	return strings.Join([]string{st.FolderKind, st.FolderPath, st.FolderURL, st.FolderUser, st.FolderName, strconv.FormatBool(st.FolderViaCore), st.FolderPassword}, "\x00")
}

// allowedFolder reports whether p is inside one of FOLDER_ROOTS.
func (s *Server) allowedFolder(p string) bool {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return false
	}
	for _, r := range s.cfg.FolderRoots {
		r = filepath.Clean(r)
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (s *Server) userEmail(ctx context.Context, uid int64) string {
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		return u.Email
	}
	return ""
}

// backendFor builds the storage backend a person's settings describe.
func (s *Server) backendFor(ctx context.Context, uid int64, st store.Settings) (storage.Backend, error) {
	ua := "PocketDocuments/" + Version
	switch st.FolderKind {
	case "folder":
		if !s.allowedFolder(st.FolderPath) {
			return nil, fmt.Errorf("the folder %q is not one this server offers (FOLDER_ROOTS)", st.FolderPath)
		}
		return storage.NewLocal(st.FolderPath, "folder", "Folder · "+st.FolderPath), nil
	case "webdav":
		user, pass := st.FolderUser, st.FolderPassword
		b, err := storage.NewWebDAV(st.FolderURL, func(context.Context) (string, string, error) { return user, pass, nil }, "WebDAV · "+st.FolderURL)
		if err != nil {
			return nil, err
		}
		b.UserAgent = ua
		return b, nil
	case "nextcloud":
		server, user, folder := st.FolderURL, st.FolderUser, firstNonEmpty(st.FolderName, "Documents")
		var creds storage.Credentials
		if st.FolderViaCore {
			if !s.core.Configured() {
				return nil, errors.New("there is no Pocket Core on this box; type your Nextcloud account in")
			}
			email := s.userEmail(ctx, uid)
			cred, err := s.core.ServiceFor(ctx, email, "nextcloud")
			if err != nil {
				return nil, fmt.Errorf("Pocket Core has no Nextcloud account for you: %w", err)
			}
			server, user = cred.URL, cred.Fields["USERNAME"]
			if user == "" {
				return nil, errors.New("your Nextcloud account in Pocket Core has no username")
			}
			creds = func(ctx context.Context) (string, string, error) {
				c, err := s.core.ServiceFor(ctx, email, "nextcloud")
				if err != nil {
					return "", "", err
				}
				return c.Fields["USERNAME"], firstNonEmpty(c.Fields["APP_PASSWORD"], c.Key), nil
			}
		} else {
			pass := st.FolderPassword
			creds = func(context.Context) (string, string, error) { return user, pass, nil }
		}
		if server == "" || user == "" {
			return nil, errors.New("Nextcloud needs its address and your username")
		}
		b, err := storage.NewNextcloud(server, user, folder, creds)
		if err != nil {
			return nil, err
		}
		b.UserAgent = ua
		return b, nil
	}
	return nil, nil
}

// folderFor returns the person's documents folder, or nil when they chose none.
func (s *Server) folderFor(ctx context.Context, uid int64) (*docFolder, error) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	if st.FolderKind == "" || st.FolderKind == "none" {
		s.foldersMu.Lock()
		delete(s.folders, uid)
		s.foldersMu.Unlock()
		return nil, nil
	}
	key := folderKey(st)
	s.foldersMu.Lock()
	f := s.folders[uid]
	if f == nil || f.key != key {
		b, err := s.backendFor(ctx, uid, st)
		if err != nil {
			s.foldersMu.Unlock()
			return nil, err
		}
		f = &docFolder{uid: uid, key: key, backend: b, seen: map[string]string{}, failed: map[string]time.Time{}}
		s.folders[uid] = f
	}
	s.foldersMu.Unlock()
	return f, nil
}

func (f *docFolder) noteErr(err error) {
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	if err != nil {
		f.lastErr = err.Error()
		return
	}
	f.lastErr, f.lastWrite = "", time.Now()
}

// folderRel is where a document sits in the folder: its storage path (or
// {created_year}/{title}) plus the extension of the file kept there.
func (s *Server) folderRel(ctx context.Context, d *store.Document, st store.Settings) (rel, src string) {
	names := s.store.Names(ctx, d.UserID)
	tmpl := "{created_year}/{title}"
	if d.StoragePathID != nil {
		if t, ok := names.PathTemplates[*d.StoragePathID]; ok && strings.TrimSpace(t) != "" {
			tmpl = t
		}
	}
	rel = filepath.ToSlash(renderPath(tmpl, d, names))
	if st.FolderFile == "original" || !d.HasArchive {
		return rel + "." + firstNonEmpty(d.OriginalExt, "bin"), s.originalPath(d)
	}
	return rel + ".pdf", s.archivePath(d.ID)
}

// placeInFolder writes (or moves) the document's copy in the person's folder
// and remembers where it is. No folder chosen: nothing happens.
func (s *Server) placeInFolder(ctx context.Context, d *store.Document) error {
	f, err := s.folderFor(ctx, d.UserID)
	if err != nil || f == nil {
		return err
	}
	st, _ := s.store.GetSettings(ctx, d.UserID)
	rel, src := s.folderRel(ctx, d, st)
	if _, err := os.Stat(src); err != nil {
		return nil // nothing to place yet (OCR still running)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	b := f.backend
	// moved or renamed: move the copy, unless the file itself changed kind
	if d.MirrorPath != "" && d.MirrorPath != rel && path.Ext(d.MirrorPath) == path.Ext(rel) {
		if _, err := b.Move(cctx, d.MirrorPath, rel); err == nil {
			f.noteErr(nil)
			d.MirrorPath = rel
			return s.store.SetMirrorPath(ctx, d.ID, rel)
		} else if errors.Is(err, storage.ErrExists) {
			rel = withSuffix(rel, fmt.Sprintf(" (#%d)", d.ID))
			if _, err := b.Move(cctx, d.MirrorPath, rel); err == nil {
				f.noteErr(nil)
				d.MirrorPath = rel
				return s.store.SetMirrorPath(ctx, d.ID, rel)
			}
		}
		// fall through: write anew, drop the old one
	}
	if d.MirrorPath != "" && d.MirrorPath != rel {
		_ = b.Delete(cctx, d.MirrorPath)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if cur, err := b.Stat(cctx, rel); err == nil && d.MirrorPath == rel && cur.Size == int64(len(data)) {
		return nil // already there, same size: leave it
	}
	if d.MirrorPath != rel {
		if _, err := b.Stat(cctx, rel); err == nil {
			rel = withSuffix(rel, fmt.Sprintf(" (#%d)", d.ID)) // another document has that name
		}
	}
	if _, err := b.Write(cctx, rel, data, ""); err != nil {
		f.noteErr(err)
		return err
	}
	f.noteErr(nil)
	d.MirrorPath = rel
	return s.store.SetMirrorPath(ctx, d.ID, rel)
}

func withSuffix(rel, suffix string) string {
	ext := path.Ext(rel)
	return strings.TrimSuffix(rel, ext) + suffix + ext
}

// unplace removes the document's copy from the folder (trash, delete, folder switched off).
func (s *Server) unplace(ctx context.Context, d *store.Document) {
	if d.MirrorPath == "" {
		return
	}
	f, err := s.folderFor(ctx, d.UserID)
	if err == nil && f != nil {
		f.mu.Lock()
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := f.backend.Delete(cctx, d.MirrorPath); err != nil {
			f.noteErr(err)
		}
		cancel()
		f.mu.Unlock()
	}
	d.MirrorPath = ""
	_ = s.store.SetMirrorPath(ctx, d.ID, "")
}

// folderSyncJob writes every live document into the person's folder.
func (s *Server) folderSyncJob(ctx context.Context, j *store.Job) error {
	f, err := s.folderFor(ctx, j.UserID)
	if err != nil {
		return err
	}
	if f == nil {
		s.store.JobProgress(ctx, j.ID, "", "no documents folder chosen")
		return nil
	}
	if err := f.backend.Test(ctx); err != nil {
		f.noteErr(err)
		return err
	}
	if st, err := s.store.GetSettings(ctx, j.UserID); err == nil && st.FolderInbox {
		if inbox, _ := storage.Clean(st.FolderInboxName); inbox != "" {
			_ = f.backend.Mkdir(ctx, inbox) // so there is a place to drop files
		}
	}
	ids := s.store.DocumentIDs(ctx, j.UserID, false)
	n, failed := 0, 0
	for i, id := range ids {
		d, err := s.store.GetDocument(ctx, j.UserID, id, false)
		if err != nil {
			continue
		}
		if err := s.placeInFolder(ctx, d); err != nil {
			failed++
			s.store.JobProgress(ctx, j.ID, "", fmt.Sprintf("#%d %s: %v", id, d.Title, err))
		} else {
			n++
		}
		if i%10 == 0 {
			s.store.JobProgress(ctx, j.ID, fmt.Sprintf("%d of %d", i+1, len(ids)), "")
		}
	}
	s.store.JobProgress(ctx, j.ID, "", fmt.Sprintf("%d documents written to %s, %d failed", n, f.backend.Describe(), failed))
	if failed > 0 && n == 0 {
		return fmt.Errorf("%d documents could not be written", failed)
	}
	return nil
}

// ---- the folder's Inbox ----

// inboxLoop looks into every person's Inbox on a timer.
func (s *Server) inboxLoop(ctx context.Context) {
	t := time.NewTicker(s.cfg.FolderPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, uid := range s.store.UserIDs(ctx) {
			s.checkInbox(ctx, uid)
		}
	}
}

// checkInbox takes the files that appeared in <folder>/<inbox> since the
// last look. A file is taken once its ETag is the same on two looks (a
// Nextcloud client still uploading gives a changing one).
func (s *Server) checkInbox(ctx context.Context, uid int64) (taken int, err error) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil || !st.FolderInbox {
		return 0, err
	}
	f, err := s.folderFor(ctx, uid)
	if err != nil || f == nil {
		return 0, err
	}
	inbox, _ := storage.Clean(st.FolderInboxName)
	if inbox == "" {
		inbox = "Inbox"
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	note := func(e error) {
		f.stateMu.Lock()
		f.lastInbox = time.Now()
		f.inboxErr = ""
		if e != nil {
			f.inboxErr = e.Error()
		}
		f.stateMu.Unlock()
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	b := f.backend
	if _, err := b.Stat(cctx, inbox); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			if err := b.Mkdir(cctx, inbox); err != nil {
				note(err)
				return 0, err
			}
			note(nil)
			return 0, nil
		}
		note(err)
		return 0, err
	}
	entries, err := listUnder(cctx, b, inbox)
	if err != nil {
		note(err)
		return 0, err
	}
	seen := map[string]string{}
	for _, e := range entries {
		if e.IsDir || storage.Hidden(e.Path) || e.Size == 0 {
			continue
		}
		name := path.Base(e.Path)
		if extract.Kind(path.Ext(name)) == "" || strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		seen[e.Path] = e.ETag
		if f.seen[e.Path] != e.ETag {
			continue // first sight (or still changing): next time
		}
		if t, bad := f.failed[e.Path]; bad && time.Since(t) < time.Hour {
			continue
		}
		data, _, err := b.Read(cctx, e.Path)
		if err != nil {
			f.failed[e.Path] = time.Now()
			s.log.Warn("inbox: cannot read", "user", uid, "file", e.Path, "err", err)
			continue
		}
		tmp := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("inbox-%d-%s", uid, name))
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			continue
		}
		// sub-folders of the Inbox become tags
		var subs []string
		if dir := strings.TrimPrefix(path.Dir(e.Path), inbox); dir != "" && dir != "." {
			for _, p := range strings.Split(strings.Trim(dir, "/"), "/") {
				if p != "" {
					subs = append(subs, p)
				}
			}
		}
		source := "folder"
		if b.Kind() == "nextcloud" {
			source = "nextcloud"
		}
		d, err := s.ingest(ctx, uid, incoming{Path: tmp, Name: name, Source: source, Actor: b.Describe() + " · " + inbox, SubDirs: subs})
		var dup *ErrDuplicate
		switch {
		case err == nil:
			s.log.Info("inbox: taken", "user", uid, "file", e.Path, "document", d.ID)
			taken++
		case errors.As(err, &dup):
			s.log.Info("inbox: already in, removed from the inbox", "user", uid, "file", e.Path, "document", dup.Existing.ID)
		default:
			os.Remove(tmp)
			f.failed[e.Path] = time.Now()
			s.log.Warn("inbox: not taken", "user", uid, "file", e.Path, "err", err)
			continue
		}
		if err := b.Delete(cctx, e.Path); err != nil {
			s.log.Warn("inbox: taken but not removed from the inbox", "user", uid, "file", e.Path, "err", err)
			f.failed[e.Path] = time.Now() // don't take it again right away
		}
		delete(seen, e.Path)
	}
	f.seen = seen
	f.stateMu.Lock()
	f.taken += taken
	f.stateMu.Unlock()
	note(nil)
	return taken, nil
}

// listUnder lists the files below one folder of a backend.
func listUnder(ctx context.Context, b storage.Backend, dir string) ([]storage.Entry, error) {
	all, err := b.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []storage.Entry
	for _, e := range all {
		if strings.HasPrefix(e.Path, dir+"/") {
			out = append(out, e)
		}
	}
	return out, nil
}

// ---- handlers ----

type folderInput struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	URL       string `json:"url"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Folder    string `json:"folder"`
	ViaCore   bool   `json:"via_core"`
	File      string `json:"file"`
	Inbox     *bool  `json:"inbox"`
	InboxName string `json:"inbox_name"`
}

func (in folderInput) apply(cur store.Settings) (store.Settings, error) {
	st := cur
	st.FolderKind = in.Kind
	st.FolderPath, st.FolderURL, st.FolderUser, st.FolderViaCore = "", "", "", false
	switch in.Kind {
	case "none", "":
		st.FolderKind = "none"
	case "folder":
		p := filepath.Clean(strings.TrimSpace(in.Path))
		if !filepath.IsAbs(p) {
			return st, errors.New("the folder must be an absolute path")
		}
		st.FolderPath = p
	case "webdav":
		u := strings.TrimRight(strings.TrimSpace(in.URL), "/")
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return st, errors.New("the WebDAV address must start with http:// or https://")
		}
		st.FolderURL, st.FolderUser = u, strings.TrimSpace(in.User)
	case "nextcloud":
		st.FolderViaCore = in.ViaCore
		if !in.ViaCore {
			u := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(in.URL), "/"), "/index.php")
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return st, errors.New("the Nextcloud address must start with http:// or https://")
			}
			st.FolderURL, st.FolderUser = u, strings.TrimSpace(in.User)
			if st.FolderUser == "" {
				return st, errors.New("your Nextcloud username is needed")
			}
		}
	default:
		return st, errors.New("unknown folder kind")
	}
	if f, err := storage.Clean(strings.TrimSpace(in.Folder)); err == nil && f != "" {
		st.FolderName = f
	} else if in.Kind == "nextcloud" {
		st.FolderName = "Documents"
	}
	if in.File == "original" {
		st.FolderFile = "original"
	} else if in.File == "archive" {
		st.FolderFile = "archive"
	}
	if in.Inbox != nil {
		st.FolderInbox = *in.Inbox
	}
	if n, err := storage.Clean(strings.TrimSpace(in.InboxName)); err == nil && n != "" && !strings.Contains(n, "/") {
		st.FolderInboxName = n
	}
	if in.Password != "" {
		st.FolderPassword = in.Password
	} else if cur.FolderKind != in.Kind || cur.FolderURL != st.FolderURL {
		st.FolderPassword = ""
	}
	return st, nil
}

func (s *Server) handleGetFolder(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	st, _ := s.store.GetSettings(ctx, uid)
	out := map[string]any{
		"kind": st.FolderKind, "path": st.FolderPath, "url": st.FolderURL, "user": st.FolderUser, "folder": st.FolderName, "via_core": st.FolderViaCore,
		"password_set": st.FolderPassword != "", "file": st.FolderFile, "inbox": st.FolderInbox, "inbox_name": st.FolderInboxName,
		"roots": s.cfg.FolderRoots, "core": s.core.Configured(), "status": map[string]any{},
	}
	if f, err := s.folderFor(ctx, uid); err != nil {
		out["status"] = map[string]any{"error": err.Error()}
	} else if f != nil {
		stt := f.status()
		stt["inbox_path"] = f.backend.Describe() + " / " + st.FolderInboxName
		out["status"] = stt
	}
	if s.core.Configured() {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if cred, err := s.core.ServiceForFresh(cctx, s.userEmail(ctx, uid), "nextcloud"); err == nil {
			out["core_nextcloud"] = map[string]any{"url": firstNonEmpty(cred.PublicURL, cred.URL), "user": cred.Fields["USERNAME"]}
		} else {
			out["core_nextcloud_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTestFolder tries a proposed folder and says what is there.
func (s *Server) handleTestFolder(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	var in folderInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	cur, _ := s.store.GetSettings(ctx, uid)
	st, err := in.apply(cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Password == "" && cur.FolderPassword != "" && st.FolderPassword == "" && cur.FolderKind == in.Kind {
		st.FolderPassword = cur.FolderPassword
	}
	if st.FolderKind == "none" {
		writeJSON(w, http.StatusOK, map[string]any{"where": "no folder", "files": 0})
		return
	}
	b, err := s.backendFor(ctx, uid, st)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := b.Test(cctx); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	entries, err := b.List(cctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	files, folders, inbox := 0, 0, 0
	for _, e := range entries {
		if e.IsDir {
			folders++
		} else {
			files++
			if strings.HasPrefix(e.Path, st.FolderInboxName+"/") {
				inbox++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"where": b.Describe(), "files": files, "folders": folders, "inbox_files": inbox})
}

func (s *Server) handlePutFolder(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	var in folderInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	cur, _ := s.store.GetSettings(ctx, uid)
	st, err := in.apply(cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if st.FolderKind != "none" {
		b, err := s.backendFor(ctx, uid, st)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		if err := b.Test(cctx); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	if st.FolderPassword == "" {
		_ = s.store.SetUserSetting(ctx, uid, "folder_password", "")
	}
	if err := s.store.SaveSettings(ctx, uid, st); err != nil {
		s.fail(w, err, "save")
		return
	}
	// the place changed: the copies in the old one are not ours to keep track of any more
	if folderKey(cur) != folderKey(st) {
		for _, id := range s.store.DocumentIDs(ctx, uid, false) {
			_ = s.store.SetMirrorPath(ctx, id, "")
		}
		s.foldersMu.Lock()
		delete(s.folders, uid)
		s.foldersMu.Unlock()
	}
	out := map[string]any{"ok": true}
	if st.FolderKind != "none" {
		if j, err := s.store.EnqueueJob(ctx, uid, nil, "folder_sync", nil); err == nil {
			out["job"] = j
			s.nudge()
		}
	}
	s.reportModes(ctx)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFolderSync(w http.ResponseWriter, r *http.Request) {
	j, err := s.store.EnqueueJob(r.Context(), me(r), nil, "folder_sync", nil)
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	s.nudge()
	writeJSON(w, http.StatusAccepted, j)
}

func (s *Server) handleFolderInbox(w http.ResponseWriter, r *http.Request) {
	// two looks: the first remembers what is there, the second takes it
	if _, err := s.checkInbox(r.Context(), me(r)); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	n, err := s.checkInbox(r.Context(), me(r))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"taken": n})
}
